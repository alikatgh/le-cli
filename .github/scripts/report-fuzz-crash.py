#!/usr/bin/env python3
"""Lossless, bounded Go crash-corpus logs and new-file-only offline restoration."""
import argparse
import base64
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys


MARKER = "LE_FUZZ_CORPUS_V1 "
HEADER = b"go test fuzz v1\n"
CHUNK_BYTES = 3 * 1024
MAX_FILES = 32
# Go 1.27.3's worker.go permits 100 MiB shared inputs, not a tiny corpus.
# This smaller explicit reporting bound keeps base64 logs below 16 MiB.
# Unexpectedly large corpora fail clearly instead of silently truncating.
MAX_CORPUS_BYTES = 8 * 1024 * 1024
MAX_LOG_BYTES = 16 * 1024 * 1024
SUMMARY_BUDGET = 900 * 1024  # safely below GitHub's 1 MiB per-step limit
TARGETS = {
    "./scan/": ("FuzzParsePSUserLines", "FuzzParsePSCommandLines", "FuzzPortOf"),
    "./intel/": ("FuzzParseDockerPorts", "FuzzBrewFormula", "FuzzWordMatch"),
}
SHA = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})\Z")
DIGEST = re.compile(r"[0-9a-f]{64}\Z")
NAME = re.compile(r"[0-9a-f]{16}\Z")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def target_parts(package, target):
    require(package in TARGETS and target in TARGETS[package], "unsupported package/target pair")
    return (package[2:-1], "testdata", "fuzz", target)


@contextmanager
def directory_fd(root, parts, create=False):
    """Walk relative to held directory fds; never follow a symlink component."""
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    fd = os.open(root, flags)
    try:
        for part in parts:
            if create:
                try:
                    os.mkdir(part, 0o755, dir_fd=fd)
                except FileExistsError:
                    pass
            child = os.open(part, flags, dir_fd=fd)
            os.close(fd)
            fd = child
        yield fd
    finally:
        os.close(fd)


def corpus_valid(name, data):
    digest = hashlib.sha256(data).hexdigest()
    require(NAME.fullmatch(name) is not None, "corpus filename is not Go's 16-hex hash")
    require(data.startswith(HEADER), "corpus is missing the Go fuzz v1 header")
    require(name == digest[:16], "corpus filename/hash mismatch")
    return digest


def read_corpus(root, parts):
    files = []
    total = 0
    try:
        with directory_fd(root, parts) as directory:
            with os.scandir(directory) as entries:
                names = []
                for entry in entries:
                    require(len(names) < MAX_FILES, "corpus exceeds the 32-file reporting bound")
                    require(NAME.fullmatch(entry.name) is not None, "unexpected file in the current target's corpus")
                    names.append(entry.name)
            for name in sorted(names):
                fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
                with os.fdopen(fd, "rb") as source:
                    info = os.fstat(source.fileno())
                    require(stat.S_ISREG(info.st_mode), "corpus entry is not a regular file")
                    require(info.st_size <= MAX_CORPUS_BYTES - total, "corpus exceeds the 8 MiB reporting bound")
                    data = source.read(MAX_CORPUS_BYTES - total + 1)
                total += len(data)
                require(total <= MAX_CORPUS_BYTES, "corpus exceeds the 8 MiB reporting bound")
                digest = corpus_valid(name, data)
                files.append(("/".join((*parts, name)), digest, data))
    except FileNotFoundError:
        # Only a missing target directory is a normal no-corpus result.
        # A file disappearing after directory enumeration is an incomplete report.
        if files or 'names' in locals():
            raise ValueError("corpus changed during reporting") from None
        return []
    return files


def replay_command(package, target, name):
    return f"go test -run '^{target}/{name}$' {package}"


def make_records(package, target, source_sha, files):
    records = [{"kind": "begin", "version": 1, "package": package, "target": target,
                "source_sha": source_sha, "files": len(files),
                "bytes": sum(len(data) for _, _, data in files)}]
    for path, digest, data in files:
        records.append({"kind": "file", "path": path, "sha256": digest, "size": len(data),
                        "chunks": (len(data) + CHUNK_BYTES - 1) // CHUNK_BYTES,
                        "replay": replay_command(package, target, path.rsplit("/", 1)[1])})
        for index, start in enumerate(range(0, len(data), CHUNK_BYTES)):
            records.append({"kind": "chunk", "path": path, "index": index,
                            "base64": base64.b64encode(data[start:start + CHUNK_BYTES]).decode("ascii")})
    records.append({"kind": "end", "files": records[0]["files"], "bytes": records[0]["bytes"]})
    return records


def encode_records(records):
    text = "".join(MARKER + json.dumps(record, ensure_ascii=True, separators=(",", ":")) + "\n"
                   for record in records)
    require(len(text.encode("utf-8")) <= MAX_LOG_BYTES, "encoded report exceeds the 16 MiB log bound")
    return text


def write_summary(package, target, source_sha, files, payload):
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if not summary_path:
        return
    run_id = os.environ.get("GITHUB_RUN_ID", "")
    run_link = (f"[Workflow run](https://github.com/alikatgh/le-cli/actions/runs/{run_id})"
                if re.fullmatch(r"[0-9]+", run_id) else "Workflow run link unavailable")
    text = (f"### Fuzz crash corpus: `{target}`\n\n{run_link} · commit `{source_sha}`\n\n"
            f"{len(files)} complete corpus file(s) for `{package}`. "
            "Save this **report crash corpus** step's plain-text log as `crash.log`.\n\n"
            "In a checkout of the recorded commit, restore new files only:\n\n"
            f"```sh\npython3 .github/scripts/report-fuzz-crash.py restore --log crash.log --source-sha {source_sha}\n```\n\n"
            "Then run each exact `replay` command in the report. No corpus means the failing step produced none.\n")
    fd = os.open(summary_path, os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
    with os.fdopen(fd, "a", encoding="utf-8") as output:
        info = os.fstat(output.fileno())
        require(stat.S_ISREG(info.st_mode), "step summary is not a regular file")
        if payload and info.st_size + len(text.encode("utf-8")) + len(payload.encode("utf-8")) + 64 <= SUMMARY_BUDGET:
            text += "\nComplete encoded corpus (also in the step log):\n\n```text\n" + payload + "```\n"
        elif payload:
            text += "\nPayload is in the step log; omitted here to stay below the summary limit.\n"
        require(info.st_size + len(text.encode("utf-8")) <= SUMMARY_BUDGET, "step summary would exceed its safe 900 KiB bound")
        output.write(text)


def report(package, target):
    parts = target_parts(package, target)
    source_sha = os.environ.get("GITHUB_SHA", "")
    require(SHA.fullmatch(source_sha) is not None, "GITHUB_SHA is missing or invalid")
    files = read_corpus(Path.cwd(), parts)
    payload = encode_records(make_records(package, target, source_sha, files)) if files else ""
    # Validate the summary bound before claiming a complete report in the log.
    write_summary(package, target, source_sha, files, payload)
    if payload:
        sys.stdout.write(payload)
    else:
        print(f"LE_FUZZ_REPORT No corpus files for {package} {target}; the original failure remains failed.")


def exact_keys(record, keys):
    require(type(record) is dict and set(record) == set(keys.split()), "invalid report record fields")


def integer(value):
    return type(value) is int


def decode_report(text, expected_sha):
    require(len(text.encode("utf-8")) <= MAX_LOG_BYTES, "input log exceeds the 16 MiB bound")
    records = []
    for line in text.splitlines():
        if MARKER in line:
            records.append(json.loads(line.partition(MARKER)[2]))
    require(len(records) >= 3, "complete corpus report not found")
    begin, end = records[0], records[-1]
    exact_keys(begin, "kind version package target source_sha files bytes")
    require(begin["kind"] == "begin" and type(begin["version"]) is int and begin["version"] == 1, "unsupported report version")
    parts = target_parts(begin["package"], begin["target"])
    require(SHA.fullmatch(expected_sha) is not None and begin["source_sha"] == expected_sha, "source SHA mismatch or invalid SHA")
    require(integer(begin["files"]) and 1 <= begin["files"] <= MAX_FILES, "invalid corpus file count")
    require(integer(begin["bytes"]) and 0 < begin["bytes"] <= MAX_CORPUS_BYTES, "invalid corpus byte count")
    exact_keys(end, "kind files bytes")
    require(integer(end["files"]) and integer(end["bytes"]) and
            end == {"kind": "end", "files": begin["files"], "bytes": begin["bytes"]}, "incomplete report end marker")
    files = []
    seen = set()
    cursor = 1
    while cursor < len(records) - 1:
        entry = records[cursor]
        cursor += 1
        exact_keys(entry, "kind path sha256 size chunks replay")
        require(entry["kind"] == "file" and type(entry["path"]) is str, "invalid corpus file record")
        path = entry["path"]
        prefix = "/".join(parts) + "/"
        require(path.startswith(prefix), "corpus path escapes the selected target")
        name = path[len(prefix):]
        require(NAME.fullmatch(name) is not None and path not in seen, "unsafe or duplicate corpus path")
        require(type(entry["sha256"]) is str and DIGEST.fullmatch(entry["sha256"]) is not None, "invalid corpus SHA256")
        require(integer(entry["size"]) and 0 < entry["size"] <= MAX_CORPUS_BYTES, "invalid corpus size")
        require(integer(entry["chunks"]) and entry["chunks"] == (entry["size"] + CHUNK_BYTES - 1) // CHUNK_BYTES, "invalid chunk count")
        require(entry["replay"] == replay_command(begin["package"], begin["target"], name), "invalid replay command")
        chunks = []
        for index in range(entry["chunks"]):
            require(cursor < len(records) - 1, "missing corpus chunk")
            chunk = records[cursor]
            cursor += 1
            exact_keys(chunk, "kind path index base64")
            require(chunk["kind"] == "chunk" and chunk["path"] == path and integer(chunk["index"]) and chunk["index"] == index, "out-of-order corpus chunk")
            require(type(chunk["base64"]) is str and len(chunk["base64"]) <= 4 * ((CHUNK_BYTES + 2) // 3), "invalid base64 chunk")
            data = base64.b64decode(chunk["base64"], validate=True)
            require(len(data) == min(CHUNK_BYTES, entry["size"] - index * CHUNK_BYTES), "invalid chunk length")
            chunks.append(data)
        data = b"".join(chunks)
        require(corpus_valid(name, data) == entry["sha256"], "corpus SHA256 mismatch")
        seen.add(path)
        files.append((parts, name, data, entry["replay"]))
    require(len(files) == begin["files"] and sum(len(f[2]) for f in files) == begin["bytes"], "incomplete corpus report")
    return files


def restore(log_path, source_sha):
    fd = os.open(log_path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as source:
        require(stat.S_ISREG(os.fstat(source.fileno()).st_mode), "input log is not a regular file")
        raw = source.read(MAX_LOG_BYTES + 1)
    require(len(raw) <= MAX_LOG_BYTES, "input log exceeds the 16 MiB bound")
    files = decode_report(raw.decode("utf-8"), source_sha)
    root = Path.cwd()
    # Check every destination before creating anything; even identical existing
    # files are preserved. Held directory fds and exclusive opens also block races.
    for parts, name, _, _ in files:
        try:
            with directory_fd(root, parts) as directory:
                try:
                    os.stat(name, dir_fd=directory, follow_symlinks=False)
                except FileNotFoundError:
                    continue
                raise ValueError("restore refuses an existing destination")
        except FileNotFoundError:
            pass
    for parts, name, data, replay in files:
        with directory_fd(root, parts, create=True) as directory:
            fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o644, dir_fd=directory)
            with os.fdopen(fd, "wb") as output:
                output.write(data)
        print(f"LE_FUZZ_RESTORED {'/'.join((*parts, name))} sha256={hashlib.sha256(data).hexdigest()}")
        print(f"LE_FUZZ_REPLAY {replay}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    reporter = commands.add_parser("report")
    reporter.add_argument("--package", required=True)
    reporter.add_argument("--target", required=True)
    restorer = commands.add_parser("restore")
    restorer.add_argument("--log", required=True)
    restorer.add_argument("--source-sha", required=True)
    args = parser.parse_args()
    try:
        if args.command == "report":
            report(args.package, args.target)
        else:
            restore(args.log, args.source_sha)
    except (ValueError, OSError, UnicodeError, TypeError) as error:
        # JSON escaping prevents untrusted filenames/data from injecting log commands.
        print("LE_FUZZ_ERROR Complete corpus " + ("report" if args.command == "report" else "restore") +
              " FAILED: " + json.dumps(str(error), ensure_ascii=True), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
