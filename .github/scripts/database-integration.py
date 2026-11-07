#!/usr/bin/env python3
"""Opt-in CI-only database journeys against sequential, disposable containers.

Run only after the workflow's cloud execution gate has passed. This script never
installs Docker or contacts an operator database. Fixture credentials, profiles,
data and dump files exist only for this run. It does not test remote TLS or prove
that every server edition, authentication system or native utility is supported.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import resource
import secrets
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock
import uuid


IMAGES = {
    "postgres": ("postgres:18@sha256:e3d24f6906d0760bf4da0dae872415391ededefad23fd471c47dc53b0ddf3c42", 5432),
    "mysql": ("mysql:8.4@sha256:a81d043930e51745522bd5c430961cf8286f0a23f0a9a2a6e0108f7ea71b0902", 3306),
    "mariadb": ("mariadb:11.8@sha256:c4124d1afa875e47f2892edf26b482fa54f796e2f342d3717a04ca091a22a9bd", 3306),
    "mongodb": ("mongo:8.0@sha256:b9924c330a8bbc5800fe9c69f04a84e221eca7b28fd049a0354147d9908fa142", 27017),
    "redis": ("redis:8@sha256:f245f4c83b84a9e9a0588da1c9456e579455310f0945d5db52cd2ba27f2000cc", 6379),
}
VENDOR = {"postgres": "pg_dump", "mysql": "mysqldump", "mariadb": "mariadb-dump", "mongodb": "mongodump", "redis": "redis-cli"}
DATABASE = "le_fixture"
READER = "le_reader"
VALUE = "owned-fixture-value"
ENGINE_SECONDS = 300
MAX_DUMP = 1 << 20
DIAGNOSTIC_LIMIT = 2 << 20
MONGO_SCRIPT_COMMAND = 'umask 077; cat > /tmp/le-fixture-script.js && exec mongosh --quiet --norc --host 127.0.0.1 --port "$1" --file /tmp/le-fixture-script.js'


class Failure(Exception):
    """A safe phase name, never subprocess output, a URI or credentials."""


def base_environment() -> dict[str, str]:
    # Use the runner's local daemon, never an ambient remote Docker context or
    # an operator account's endpoint/credentials passed to the tested process.
    names = ("PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "TEMP", "TMP")
    environment = {name: os.environ[name] for name in names if name in os.environ}
    environment["DOCKER_HOST"] = "unix:///var/run/docker.sock"
    return environment


def require(condition: bool, phase: str) -> None:
    if not condition:
        raise Failure(phase)


def diagnostic_file_limit() -> None:
    # Child-only enforcement, including writes before the parent can inspect them.
    resource.setrlimit(resource.RLIMIT_FSIZE, (DIAGNOSTIC_LIMIT, DIAGNOSTIC_LIMIT))


def limited_run(args: list[str], **kwargs) -> subprocess.CompletedProcess:
    with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
        result = subprocess.run(args, stdout=stdout, stderr=stderr,
                                preexec_fn=diagnostic_file_limit, **kwargs)
        stdout.seek(0)
        stderr.seek(0)
        return subprocess.CompletedProcess(args, result.returncode,
            stdout.read(DIAGNOSTIC_LIMIT + 1), stderr.read(DIAGNOSTIC_LIMIT + 1))


def rewrite_vendor_args(engine: str, args: list[str], internal_port: int,
                        profile: tuple[str, str] | None, external_port: int) -> list[str]:
    """Only adapt the fixture endpoint/profile; preserve actual CLI dump flags."""
    out = list(args)
    if engine in ("mysql", "mariadb"):
        require(sum(a.startswith("--host=") for a in args)==1 and "--host=127.0.0.1" in args
                and sum(a.startswith("--port=") for a in args)==1 and f"--port={external_port}" in args,
                "native selected TCP endpoint")
    if engine=="redis":
        require(args.count("-h")==1 and args.count("-p")==1, "native selected Redis endpoint")
        require(args[args.index("-h")+1]=="127.0.0.1" and args[args.index("-p")+1]==str(external_port),
                "native selected Redis endpoint")
    for index, argument in enumerate(out):
        if engine in ("mysql", "mariadb"):
            if argument.startswith("--host="):
                out[index] = "--host=127.0.0.1"
            elif argument.startswith("--port="):
                out[index] = f"--port={internal_port}"
        if engine == "redis" and index and out[index - 1] in ("-h", "-p"):
            out[index] = "127.0.0.1" if out[index - 1] == "-h" else str(internal_port)
        if profile:
            for flag in ("--defaults-file=", "--config="):
                if argument == flag + profile[0]:
                    out[index] = flag + profile[1]
    return out


def vendor_wrapper(config: dict, args: list[str]) -> None:
    """Entry point for an owner-private, generated native-client adapter."""
    engine = config["engine"]
    profile = tuple(config["profile"]) if config.get("profile") else None
    args = rewrite_vendor_args(engine, args, config["port"], profile,config["external_port"])
    environment = os.environ.copy()
    environment["DOCKER_HOST"] = "unix:///var/run/docker.sock"
    environment.pop("DOCKER_CONTEXT", None)
    if engine == "postgres":
        require(environment.get("PGHOST")=="127.0.0.1" and environment.get("PGPORT")==str(config["external_port"]),"native selected PostgreSQL endpoint")
        environment["PGHOST"] = "127.0.0.1"
        environment["PGPORT"] = str(config["port"])
    names = ("PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE",
             "PGSERVICEFILE", "PGCONNECT_TIMEOUT", "PGOPTIONS", "PGSSLMODE",
             "PGSSLROOTCERT", "REDISCLI_AUTH")
    command = [config["docker"], "exec"]
    for name in names:
        if name in environment:
            command += ["--env", name]
    command += [config["container"], VENDOR[engine], *args]
    os.execve(config["docker"], command, environment)


class Engine:
    def __init__(self, binary: Path, scratch: Path, engine: str, docker: str):
        self.binary, self.scratch, self.engine, self.docker = binary, scratch, engine, docker
        self.image, self.internal_port = IMAGES[engine]
        self.name = "le-db-fixture-" + uuid.uuid4().hex
        self.container: str | None = None
        self.creation_attempted = False
        self.cid_file = self.scratch / "container.cid"
        self.root_password, self.reader_password = secrets.token_hex(20), secrets.token_hex(20)
        self.deadline = time.monotonic() + ENGINE_SECONDS
        self.port = 0
        self.profile: Path | None = None
        self.inside_profile = "/tmp/le-fixture-profile"

    def run(self, args: list[str], phase: str, *, env: dict | None = None,
            input: bytes | None = None, seconds: float = 30,
            success: bool = True) -> subprocess.CompletedProcess:
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise Failure(phase + " (engine deadline)")
        try:
            result = limited_run(args, env=env if env is not None else base_environment(), input=input,
                                    timeout=min(seconds, remaining), check=False)
        except (OSError, subprocess.TimeoutExpired):
            raise Failure(phase + " (process failed or timed out)") from None
        require(len(result.stdout) <= 2 << 20 and len(result.stderr) <= 2 << 20,
                phase + " (unexpected diagnostics volume)")
        if success:
            require(result.returncode == 0, phase)
        return result

    def execute(self, args: list[str], phase: str, *, input: bytes | None = None,
                environment: dict[str, str] | None = None, success: bool = True,
                seconds: float = 15) -> subprocess.CompletedProcess:
        env = base_environment()
        env.update(environment or {})
        command = [self.docker, "exec", "-i"]
        for key in environment or {}:
            command += ["--env", key]
        return self.run(command + [self.container, *args], phase, env=env,
                        input=input, success=success, seconds=seconds)

    def start(self) -> None:
        self.run([self.docker, "pull", "--quiet", self.image], "pull pinned image", seconds=180)
        environment = base_environment()
        extra: list[str] = []
        if self.engine == "postgres":
            environment["POSTGRES_PASSWORD"] = self.root_password
            extra = ["--env", "POSTGRES_PASSWORD"]
        elif self.engine in ("mysql", "mariadb"):
            key = "MYSQL_ROOT_PASSWORD" if self.engine == "mysql" else "MARIADB_ROOT_PASSWORD"
            environment[key] = self.root_password
            extra = ["--env", key]
        elif self.engine == "mongodb":
            environment.update(MONGO_INITDB_ROOT_USERNAME="root", MONGO_INITDB_ROOT_PASSWORD=self.root_password)
            extra = ["--env", "MONGO_INITDB_ROOT_USERNAME", "--env", "MONGO_INITDB_ROOT_PASSWORD"]
        command = [self.docker, "create", "--cidfile", str(self.cid_file), "--pull=never", "--name", self.name,
                   "--memory=1g", "--cpus=1", "--pids-limit=256", "--restart=no",
                   "--log-driver=none", "-p", f"127.0.0.1::{self.internal_port}", *extra, self.image]
        if self.engine == "redis":
            # The config is copied while the container is stopped. Its fixture
            # password is never an argv value, including inside the container.
            command += ["sh", "-c", "chown redis:redis /data/le-fixture-redis.conf && chmod 600 /data/le-fixture-redis.conf && exec docker-entrypoint.sh redis-server /data/le-fixture-redis.conf"]
        require(not os.path.lexists(self.cid_file), "fresh private container ID file")
        self.creation_attempted = True
        self.run(command, "create owned container", env=environment)
        self.container = self.created_id()
        require(self.container is not None, "validate created container ID file")
        if self.engine == "redis":
            config = self.scratch / "redis-fixture.conf"
            # Hex fixture credentials need no Redis-config escaping. The host
            # file and its stopped-container copy are owner-private throughout.
            descriptor = os.open(config, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "w") as file:
                file.write('bind 0.0.0.0\nprotected-mode yes\nport 6379\nsave ""\nappendonly no\ndir /data\ndbfilename dump.rdb\nmaxmemory 64mb\nmaxmemory-policy noeviction\nrequirepass ' + self.root_password + '\n')
            self.run([self.docker, "cp", str(config), self.container + ":/data/le-fixture-redis.conf"], "copy private Redis configuration")
        self.run([self.docker, "start", self.container], "start owned container")
        binding = self.run([self.docker, "port", self.container, f"{self.internal_port}/tcp"], "read owned loopback port")
        match = re.fullmatch(r"127\.0\.0\.1:(\d+)\s*", binding.stdout.decode("ascii", errors="ignore"))
        require(match is not None and 0 < int(match[1]) <= 65535, "enforce owned loopback binding")
        self.port = int(match[1])
        self.ready()

    def ready(self) -> None:
        # Finite readiness polls are setup checks, not retries of failed tests.
        last_reason="unknown"
        for _ in range(60):
            if self.engine == "postgres":
                result = self.execute(["pg_isready", "-h", "127.0.0.1", "-p", str(self.internal_port), "-U", "postgres"], "readiness", success=False, seconds=5)
            elif self.engine in ("mysql", "mariadb"):
                result = self.sql("SELECT 1", readiness=True)
            elif self.engine == "mongodb":
                result = self.mongo("db.adminCommand({ping:1})", readiness=True)
            else:
                result = self.execute(["redis-cli", "-h", "127.0.0.1", "-p", str(self.internal_port), "PING"], "readiness", environment={"REDISCLI_AUTH": self.root_password}, success=False, seconds=5)
            if result.returncode == 0 and (self.engine != "redis" or b"PONG" in result.stdout):
                return
            text=(result.stdout+result.stderr).lower()
            last_reason="client-failed"
            for needle,label in [(b"enxio","stdin-device"),(b"enoent","missing-path"),(b"econnrefused","connection-refused"),(b"authentication failed","authentication"),(b"not running","container-stopped"),(b"not found","client-unavailable"),(b"unknown option","client-option")]:
                if needle in text:last_reason=label;break
            time.sleep(min(0.5, max(0, self.deadline - time.monotonic())))
        # Only fixed classifications are emitted; raw output can contain
        # credentials, database data or complete command lines.
        state=self.run([self.docker,"inspect","--format","{{.State.ExitCode}} {{.State.OOMKilled}}",self.container],"read owned fixture termination state",seconds=5,success=False)
        termination="unknown"
        if re.fullmatch(rb"-?\d{1,4} (true|false)\s*",state.stdout):
            termination=state.stdout.decode("ascii").strip()
        raise Failure("readiness exhausted ("+last_reason+"; exit/oom="+termination+")")

    def sql(self, query: str, *, database: str | None = None,
            readiness: bool = False, data: bytes | None = None) -> subprocess.CompletedProcess:
        if self.engine == "postgres":
            command = ["psql", "-X", "--no-password", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1",
                       "-p", str(self.internal_port), "-U", "postgres", "-d", database or "postgres", "-At"]
            environment = {"PGPASSWORD": self.root_password}
        else:
            client = "mysql" if self.engine == "mysql" else "mariadb"
            command = [client, "--protocol=TCP", "--host=127.0.0.1", f"--port={self.internal_port}", "--user=root", "--batch", "--skip-column-names"]
            if database:
                command += [database]
            environment = {"MYSQL_PWD": self.root_password}
        return self.execute(command, "SQL readiness" if readiness else "fixture SQL", input=data if data is not None else query.encode(), environment=environment, success=not readiness, seconds=5 if readiness else 20)

    def mongo(self, code: str, *, readiness: bool = False) -> subprocess.CompletedProcess:
        prelude = 'db.getSiblingDB("admin").auth("root", process.env.LE_FIXTURE_ROOT_PASSWORD);'
        # Docker exec stdin may be a socket, which cannot be opened as a file.
        # Stage only synthetic fixture code in a private container-owned file.
        # Using mongosh's script loader preserves its async command handling.
        return self.execute(["sh","-c",MONGO_SCRIPT_COMMAND,"owned-fixture",str(self.internal_port)],
                            "Mongo readiness" if readiness else "fixture Mongo operation",
                            input=(prelude + code).encode(),
                            environment={"LE_FIXTURE_ROOT_PASSWORD": self.root_password, "MONGOSH_DISABLE_TELEMETRY": "1"},
                            success=not readiness, seconds=5 if readiness else 20)

    def seed(self) -> None:
        if self.engine == "postgres":
            self.sql(f"CREATE DATABASE {DATABASE}; CREATE ROLE {READER} LOGIN PASSWORD '{self.reader_password}';")
            self.sql(f"CREATE TABLE fixture_records (id integer PRIMARY KEY, label text); INSERT INTO fixture_records VALUES (1,'{VALUE}'); GRANT USAGE ON SCHEMA public TO {READER}; GRANT SELECT ON fixture_records TO {READER};", database=DATABASE)
        elif self.engine in ("mysql", "mariadb"):
            self.sql(f"CREATE DATABASE {DATABASE}; CREATE TABLE {DATABASE}.fixture_records (id integer PRIMARY KEY,label varchar(80)) ENGINE=InnoDB; INSERT INTO {DATABASE}.fixture_records VALUES (1,'{VALUE}'); CREATE USER '{READER}'@'%' IDENTIFIED BY '{self.reader_password}'; GRANT SELECT ON {DATABASE}.* TO '{READER}'@'%';")
        elif self.engine == "mongodb":
            self.mongo(f'let f=db.getSiblingDB("{DATABASE}"); f.fixture_records.insertOne({{id:1,label:"{VALUE}"}}); f.createUser({{user:"{READER}",pwd:"{self.reader_password}",roles:[{{role:"read",db:"{DATABASE}"}}]}});')
        else:
            self.execute(["redis-cli", "-h", "127.0.0.1", "-p", str(self.internal_port), "SET", "fixture:key", VALUE], "seed Redis", environment={"REDISCLI_AUTH": self.root_password})
            self.execute(["redis-cli", "-h", "127.0.0.1", "-p", str(self.internal_port), "-n","2","SET", "fixture:other", VALUE], "seed second Redis logical database", environment={"REDISCLI_AUTH": self.root_password})

    def cli(self, action: str, request: bytes | None = None, *, extra: list[str] | None = None,
            success: bool = True, seconds: float = 15) -> subprocess.CompletedProcess:
        environment = base_environment()
        environment.update(LOCALHOST_DB_USERNAME="" if self.engine == "redis" else READER,
                           LOCALHOST_DB_PASSWORD=self.root_password if self.engine == "redis" else self.reader_password)
        command = [str(self.binary), "db", action, self.engine, "--address", f"127.0.0.1:{self.port}",
                   "--database", "0" if self.engine == "redis" else DATABASE, "--json"]
        if self.engine == "mongodb" and action == "query":
            command += ["--collection", "fixture_records"]
        return self.run(command + (extra or []), "CLI " + action, env=environment, input=request,
                        success=success, seconds=seconds)

    def query_and_schema(self) -> None:
        if self.engine in ("postgres", "mysql", "mariadb"):
            request = b"SELECT id,label FROM fixture_records ORDER BY id"
        elif self.engine == "mongodb":
            request = b'{"id":1}'
        else:
            request = b'["GET","fixture:key"]'
        result = json.loads(self.cli("query", request).stdout)
        require(result.get("data_mode") == "live" and len(result.get("rows", [])) == 1, "query live row shape")
        if self.engine in ("postgres", "mysql", "mariadb"):
            require(result["rows"] == [[1, VALUE]], "query fixture values")
        elif self.engine == "mongodb":
            require(result["rows"][0][0].get("label") == VALUE, "query fixture document")
        else:
            require(result["rows"] == [[VALUE]], "query fixture key")
        schema = json.loads(self.cli("schema").stdout)
        require(schema.get("data_mode") == "live" and bool(schema.get("rows")), "schema metadata rows")
        if self.engine in ("postgres", "mysql", "mariadb"):
            require(any("fixture_records" in row for row in schema["rows"]), "schema fixture table")
        elif self.engine == "mongodb":
            require(any(row[0] == "fixture_records" for row in schema["rows"]), "schema fixture collection")
        else:
            require(any(row[0] == "dbsize" and row[1] == 1 for row in schema["rows"]), "schema fixture keyspace")

        denied = (b"DELETE FROM fixture_records" if self.engine in ("postgres", "mysql", "mariadb")
                  else b'{"$where":"function(){return true}"}' if self.engine == "mongodb"
                  else b'["SET","fixture:key","changed"]')
        failed = self.cli("query", denied, success=False)
        require(failed.returncode != 0 and not failed.stdout, "denied mutation emits no rows")
        # A CTE write reaches PostgreSQL's extended protocol/read-only boundary.
        if self.engine == "postgres":
            failed = self.cli("query", b"WITH changed AS (DELETE FROM fixture_records RETURNING *) SELECT * FROM changed", success=False)
            require(failed.returncode != 0 and not failed.stdout, "PostgreSQL read-only transaction")
        if self.engine in ("postgres", "mysql", "mariadb"):
            sleep = b"SELECT pg_sleep(30)" if self.engine == "postgres" else b"SELECT SLEEP(30) FROM fixture_records"
            started = time.monotonic()
            failed = self.cli("query", sleep, success=False, seconds=12)
            require(failed.returncode != 0 and not failed.stdout and time.monotonic() - started < 10, "server/client query timeout")
        unchanged = json.loads(self.cli("query", request).stdout)
        require(unchanged["rows"] == result["rows"], "denied mutation preserves fixture")

    def make_wrapper(self) -> Path:
        profile_pair = None
        if self.engine in ("mysql", "mariadb", "mongodb"):
            self.profile = self.scratch / "vendor-profile.conf"
            if self.engine == "mongodb":
                external = f"mongodb://root:{self.root_password}@127.0.0.1:{self.port}/{DATABASE}?directConnection=true&authSource=admin&authMechanism=SCRAM-SHA-256"
                internal = external.replace(f":{self.port}/", f":{self.internal_port}/")
                self.profile.write_text("uri: " + json.dumps(external) + "\n")
                inside = self.scratch / "inside-vendor-profile.conf"
                inside.write_text("uri: " + json.dumps(internal) + "\n")
            else:
                self.profile.write_text(f"[client]\nuser=root\npassword={self.root_password}\n")
                inside = self.profile
            self.profile.chmod(0o600)
            inside.chmod(0o600)
            self.run([self.docker, "cp", str(inside), self.container + ":" + self.inside_profile], "copy owned vendor profile")
            self.execute(["chmod", "600", self.inside_profile], "protect owned vendor profile")
            profile_pair = [str(self.profile), self.inside_profile]
        config = {"engine": self.engine, "container": self.container, "port": self.internal_port,
                  "profile": profile_pair, "docker": self.docker,"external_port":self.port}
        wrapper = self.scratch / "vendor-wrapper"
        wrapper.write_text("#!" + sys.executable + "\nimport runpy\n" +
                           "module=runpy.run_path(" + repr(str(Path(__file__).resolve())) + ")\n" +
                           "module['vendor_wrapper'](" + repr(config) + ", __import__('sys').argv[1:])\n")
        wrapper.chmod(0o700)
        return wrapper

    def backup_and_restore(self) -> None:
        wrapper = self.make_wrapper()
        output = self.scratch / "fixture.dump"
        extra = ["--binary", str(wrapper), "--output", str(output), "--max-bytes", str(MAX_DUMP), "--timeout", "30s"]
        if self.profile:
            extra += ["--credentials-file", str(self.profile)]
        report = json.loads(self.cli("backup", extra=extra, seconds=40).stdout)
        require(report.get("complete") is True and report.get("bytes") == output.stat().st_size and 0 < output.stat().st_size <= MAX_DUMP, "native backup completion")
        require(output.stat().st_mode & 0o077 == 0, "private backup permissions")
        data = output.read_bytes()
        if self.engine == "postgres":
            require(data.startswith(b"PGDMP"), "PostgreSQL native format")
        elif self.engine in ("mysql", "mariadb"):
            require(b"CREATE TABLE" in data and VALUE.encode() in data, "SQL native format")
        elif self.engine == "redis":
            require(data.startswith(b"REDIS") and report.get("database") == "all logical databases", "Redis whole-instance native format")
        else:
            require(len(data) > 16, "Mongo native archive")
        before = hashlib.sha256(data).digest()
        failed = self.cli("backup", extra=extra, success=False, seconds=40)
        require(failed.returncode != 0 and hashlib.sha256(output.read_bytes()).digest() == before, "existing backup preserved")
        partial = self.scratch / "partial.dump"
        bounded = list(extra)
        bounded[bounded.index("--output") + 1] = str(partial)
        bounded[bounded.index("--max-bytes") + 1] = "32"
        failed = self.cli("backup", extra=bounded, success=False, seconds=40)
        require(failed.returncode != 0 and partial.is_file() and partial.stat().st_size <= 32, "backup byte cap preserves partial file")
        incomplete = json.loads(failed.stdout)
        require(incomplete.get("complete") is False, "partial backup never complete")
        self.run([self.docker, "cp", str(output), self.container + ":/tmp/le-fixture.dump"], "copy owned restore artifact")
        if self.engine == "postgres":
            self.sql("CREATE DATABASE le_restored;")
            self.execute(["pg_restore", "--exit-on-error", "--no-owner", "--no-privileges", "--host=127.0.0.1", f"--port={self.internal_port}", "--username=postgres", "--dbname=le_restored", "/tmp/le-fixture.dump"], "restore PostgreSQL", environment={"PGPASSWORD": self.root_password}, seconds=30)
            restored = self.sql("SELECT label FROM fixture_records WHERE id=1;", database="le_restored")
            require(restored.stdout.strip() == VALUE.encode(), "restored PostgreSQL value")
        elif self.engine in ("mysql", "mariadb"):
            self.sql(f"DROP DATABASE {DATABASE};")
            self.sql("", data=data)
            restored = self.sql(f"SELECT label FROM {DATABASE}.fixture_records WHERE id=1;")
            require(restored.stdout.strip() == VALUE.encode(), "restored SQL value")
        elif self.engine == "mongodb":
            self.mongo(f'db.getSiblingDB("{DATABASE}").dropDatabase();')
            self.execute(["mongorestore", "--config=" + self.inside_profile, "--archive=/tmp/le-fixture.dump", "--quiet", "--numParallelCollections=1"], "restore Mongo archive", seconds=30)
            restored = self.mongo(f'print(db.getSiblingDB("{DATABASE}").fixture_records.findOne({{id:1}}).label);')
            require(restored.stdout.strip().endswith(VALUE.encode()), "restored Mongo value")
        else:
            self.execute(["sh", "-c", "cp /tmp/le-fixture.dump /data/dump.rdb && chown redis:redis /data/dump.rdb && chmod 600 /data/dump.rdb"], "stage owned Redis restore")
            self.run([self.docker, "restart", "--time=5", self.container], "restart owned Redis with native RDB", seconds=15)
            self.ready()
            restored = self.execute(["redis-cli", "-h", "127.0.0.1", "-p", str(self.internal_port), "GET", "fixture:key"], "verify Redis restore", environment={"REDISCLI_AUTH": self.root_password})
            require(restored.stdout.strip() == VALUE.encode(), "restored Redis value")
            restored_other=self.execute(["redis-cli","-h","127.0.0.1","-p",str(self.internal_port),"-n","2","GET","fixture:other"],"verify second Redis logical database restore",environment={"REDISCLI_AUTH":self.root_password})
            require(restored_other.stdout.strip()==VALUE.encode(),"restored whole Redis instance")

    def created_id(self) -> str | None:
        # A name (even a random one) is not proof of ownership. Docker writes
        # this private --cidfile only after creating our container. A collision
        # or a failed create without a CID never authorizes deleting by name.
        try:
            descriptor = os.open(self.cid_file, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        except FileNotFoundError:
            return None
        except OSError:
            raise Failure("read created container ID file") from None
        with os.fdopen(descriptor, "rb") as file:
            info = os.fstat(file.fileno())
            require(stat.S_ISREG(info.st_mode) and info.st_size <= 128, "validate created container ID file")
            data = file.read(129)
        require(re.fullmatch(rb"[0-9a-f]{64}\n?", data) is not None, "validate created container ID file")
        return data.decode("ascii").strip()

    def cleanup(self) -> None:
        if not self.creation_attempted:
            return
        container = self.container or self.created_id()
        if container is None:
            return  # Failed create/name collision: no confirmed owned resource.
        require(re.fullmatch(r"[0-9a-f]{64}", container) is not None, "validate cleanup container ID")
        try:
            result = limited_run([self.docker, "rm", "--force", "--volumes", container],
                                    env=base_environment(), timeout=15, check=False)
        except (OSError, subprocess.TimeoutExpired):
            raise Failure("cleanup owned container " + self.name) from None
        require(result.returncode == 0, "cleanup owned container " + self.name)


class OfflineChecks(unittest.TestCase):
    def test_cleanup_never_uses_an_unconfirmed_name(self):
        with tempfile.TemporaryDirectory(prefix="le-db-selfcheck-") as temporary:
            fixture = Engine(Path("/unused/le"), Path(temporary), "postgres", "/unused/docker")
            fixture.creation_attempted = True  # Simulate a failed name collision.
            with mock.patch.object(subprocess, "run") as run:
                fixture.cleanup()
                self.assertFalse(run.called)
            fixture.cid_file.write_text("not-a-container-id")
            with mock.patch.object(subprocess, "run") as run:
                with self.assertRaises(Failure):
                    fixture.cleanup()
                self.assertFalse(run.called)

    def test_cleanup_recovers_only_the_created_cid_after_partial_start(self):
        with tempfile.TemporaryDirectory(prefix="le-db-selfcheck-") as temporary:
            fixture = Engine(Path("/unused/le"), Path(temporary), "postgres", "/unused/docker")
            fixture.creation_attempted = True
            identifier = "a" * 64
            fixture.cid_file.write_text(identifier + "\n")
            with mock.patch.dict(globals(),limited_run=mock.Mock(return_value=subprocess.CompletedProcess([], 0, b"", b""))):
                fixture.cleanup()
                self.assertEqual(limited_run.call_args.args[0], ["/unused/docker", "rm", "--force", "--volumes", identifier])

    def test_mongo_seed_credentials_use_stdin_not_argv(self):
        with tempfile.TemporaryDirectory(prefix="le-db-selfcheck-") as temporary:
            fixture = Engine(Path("/unused/le"), Path(temporary), "mongodb", "/unused/docker")
            with mock.patch.object(fixture, "execute") as execute:
                fixture.seed()
                command = execute.call_args.args[0]
                self.assertEqual(command[:3],["sh","-c",MONGO_SCRIPT_COMMAND])
                self.assertEqual(command[-1],str(fixture.internal_port))
                self.assertTrue(fixture.reader_password not in " ".join(command))
                self.assertTrue(fixture.root_password not in " ".join(command))
                self.assertTrue(fixture.reader_password.encode() in execute.call_args.kwargs["input"])

    def test_redis_private_config_is_copied_before_start_without_password_argv(self):
        with tempfile.TemporaryDirectory(prefix="le-db-selfcheck-") as temporary:
            fixture = Engine(Path("/unused/le"), Path(temporary), "redis", "/unused/docker")
            identifier = "b" * 64
            commands = []

            def run(args, phase, **kwargs):
                commands.append(list(args))
                if args[1] == "create":
                    self.assertIn("--cidfile", args)
                    fixture.cid_file.write_text(identifier + "\n")
                output = b"127.0.0.1:43210\n" if args[1] == "port" else b""
                return subprocess.CompletedProcess(args, 0, output, b"")

            with mock.patch.object(fixture, "run", side_effect=run), mock.patch.object(fixture, "ready"):
                fixture.start()
            operations = [command[1] for command in commands]
            self.assertLess(operations.index("create"), operations.index("cp"))
            self.assertLess(operations.index("cp"), operations.index("start"))
            self.assertTrue(all(fixture.root_password not in " ".join(command) for command in commands))
            config = Path(temporary) / "redis-fixture.conf"
            self.assertEqual(config.stat().st_mode & 0o777, 0o600)
            self.assertTrue("requirepass " + fixture.root_password in config.read_text())

    def test_native_flags_survive_endpoint_mapping(self):
        args = ["--defaults-file=/private/profile", "--protocol=TCP", "--host=127.0.0.1", "--port=45678", "--single-transaction", "--quick", "--skip-lock-tables", "--databases", "--", DATABASE]
        changed = rewrite_vendor_args("mysql", args, 3306, ("/private/profile", "/tmp/profile"),45678)
        self.assertEqual(changed[0], "--defaults-file=/tmp/profile")
        self.assertEqual(changed[3], "--port=3306")
        self.assertEqual(changed[4:], args[4:])
        self.assertEqual(args[3], "--port=45678")

    def test_redis_mapping_preserves_snapshot_options(self):
        args = ["-h", "127.0.0.1", "-p", "45678", "-n", "0", "-t", "5", "--rdb", "-"]
        self.assertEqual(rewrite_vendor_args("redis", args, 6379, None,45678), args[:3] + ["6379"] + args[4:])

    def test_endpoint_mapping_cannot_hide_wrong_cli_port(self):
        with self.assertRaises(Failure):
            rewrite_vendor_args("mysql",["--host=127.0.0.1","--port=1"],3306,None,45678)
        with self.assertRaises(Failure):
            rewrite_vendor_args("redis",["-h","other.invalid","-p","45678"],6379,None,45678)

    def test_diagnostics_are_capped_during_execution(self):
        result=limited_run([sys.executable,"-c","import sys; sys.stdout.buffer.write(b'x'*3145728)"],timeout=5,check=False)
        self.assertNotEqual(result.returncode,0)
        self.assertLessEqual(len(result.stdout),DIAGNOSTIC_LIMIT)

    def test_all_images_are_immutable(self):
        self.assertEqual(len(IMAGES), 5)
        for image, port in IMAGES.values():
            self.assertRegex(image, r"^[\w.-]+:[\w.-]+@sha256:[0-9a-f]{64}$")
            self.assertTrue(0 < port < 65536)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", nargs="?", help="absolute path to the already-built CLI")
    parser.add_argument("--self-test", action="store_true", help="offline mapping/manifest checks; does not use Docker")
    args = parser.parse_args()
    if args.self_test:
        suite = unittest.defaultTestLoader.loadTestsFromTestCase(OfflineChecks)
        return 0 if unittest.TextTestRunner(verbosity=2).run(suite).wasSuccessful() else 1
    if not args.binary or not Path(args.binary).is_absolute() or not os.access(args.binary, os.X_OK):
        parser.error("an absolute executable CLI path is required")
    if not sys.platform.startswith("linux") or os.environ.get("CI") != "true":
        parser.error("real-engine execution requires the explicitly gated Linux CI runner")
    docker = shutil.which("docker")
    if not docker:
        parser.error("Docker is required in the gated CI runner; this script does not install it")
    for engine in IMAGES:
        with tempfile.TemporaryDirectory(prefix="le-db-integration-") as temporary:
            fixture = Engine(Path(args.binary), Path(temporary), engine, docker)
            failed = False
            try:
                fixture.start()
                fixture.seed()
                fixture.query_and_schema()
                fixture.backup_and_restore()
                print(engine + ": query/schema/denial/backup/restore passed", flush=True)
            except (Failure, ValueError, KeyError, TypeError, OSError) as error:
                # Unknown exceptions can contain subprocess arguments or queried
                # data; only our static phase names may be reported.
                print(engine + ": " + (str(error) if isinstance(error, Failure) else "fixture verification failed"), file=sys.stderr, flush=True)
                failed = True
            finally:
                try:
                    fixture.cleanup()
                except Failure as error:
                    print(str(error), file=sys.stderr, flush=True)
                    failed = True
            if failed:
                return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
