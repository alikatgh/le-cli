#!/usr/bin/env python3
"""Offline lossless transport, bounds and filesystem-guard regressions."""
import base64
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


sys.dont_write_bytecode = True
SCRIPT = Path(__file__).with_name("report-fuzz-crash.py")
spec = importlib.util.spec_from_file_location("fuzz_report", SCRIPT)
reporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reporter)
SHA = "a" * 40
PACKAGE = "./scan/"
TARGET = "FuzzParsePSUserLines"
PARTS = ("scan", "testdata", "fuzz", TARGET)


class FuzzCrashReportTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="fuzz-crash-report-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.checkout = self.root / "checkout"
        self.checkout.mkdir()

    def corpus(self, data, root=None, parts=PARTS):
        directory = (root or self.checkout).joinpath(*parts)
        directory.mkdir(parents=True, exist_ok=True)
        path = directory / hashlib.sha256(data).hexdigest()[:16]
        path.write_bytes(data)
        return path

    def run_cli(self, *args, root=None, **environment):
        env = {"GITHUB_SHA": SHA, "GITHUB_RUN_ID": "123456", "PYTHONDONTWRITEBYTECODE": "1"}
        if "HOME" in os.environ:
            env["HOME"] = os.environ["HOME"]
        env.update(environment)
        return subprocess.run([sys.executable, str(SCRIPT), *args], cwd=root or self.checkout,
                              env=env, text=True, capture_output=True, timeout=10, check=False)

    def log_for(self, data=None):
        self.corpus(data or reporter.HEADER + b'string("replay")\n')
        result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout

    def save_log(self, text):
        log = self.root / "crash.log"
        log.write_text(text)
        return log

    def destination(self, name="restore"):
        root = self.root / name
        root.mkdir()
        return root

    def test_binary_unicode_chunk_decode_hash_and_safe_output(self):
        data = reporter.HEADER + bytes(range(256)) * 40 + "Дьөгүөр 日本語 ::error::".encode()
        path = self.corpus(data)
        summary = self.root / "summary.md"
        result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET,
                              GITHUB_STEP_SUMMARY=str(summary))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        self.assertTrue(all(line.startswith(reporter.MARKER) for line in result.stdout.splitlines()))
        records = [json.loads(line[len(reporter.MARKER):]) for line in result.stdout.splitlines()]
        chunks = [r for r in records if r["kind"] == "chunk"]
        self.assertGreater(len(chunks), 1)
        self.assertTrue(all(len(r["base64"]) <= 4096 for r in chunks))
        self.assertEqual(b"".join(base64.b64decode(r["base64"], validate=True) for r in chunks), data)
        decoded = reporter.decode_report(result.stdout, SHA)
        self.assertEqual(decoded[0][:3], (PARTS, path.name, data))
        self.assertEqual(records[1]["sha256"], hashlib.sha256(data).hexdigest())
        self.assertEqual(decoded[0][3], f"go test -run '^{TARGET}/{path.name}$' ./scan/")
        text = summary.read_text()
        self.assertIn("https://github.com/alikatgh/le-cli/actions/runs/123456", text)
        self.assertIn(SHA, text)
        self.assertIn(reporter.MARKER, text)
        self.assertLess(summary.stat().st_size, reporter.SUMMARY_BUDGET)

    def test_multiple_files_and_only_the_current_target(self):
        expected = [reporter.HEADER + b'string("one")\n', reporter.HEADER + b'string("two")\n']
        for data in expected:
            self.corpus(data)
        other = self.checkout / "scan/testdata/fuzz/FuzzPortOf"
        other.mkdir()
        (other / "not-a-corpus-file").write_text("DO-NOT-READ-OTHER-TARGET")
        log = self.log_for(expected[0])
        files = reporter.decode_report(log, SHA)
        self.assertEqual(sorted(f[2] for f in files), sorted(expected))
        self.assertNotIn("DO-NOT-READ-OTHER-TARGET", log)
        source = self.save_log(log)
        destination = self.destination()
        result = self.run_cli("restore", "--log", str(source), "--source-sha", SHA, root=destination)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sorted(p.read_bytes() for p in destination.joinpath(*PARTS).iterdir()), sorted(expected))

    def test_no_corpus_is_explicit_and_successful_reporting(self):
        summary = self.root / "summary.md"
        result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET,
                              GITHUB_STEP_SUMMARY=str(summary))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("No corpus files", result.stdout)
        self.assertNotIn(reporter.MARKER, result.stdout)
        self.assertIn("0 complete corpus file(s)", summary.read_text())

    def test_oversize_report_fails_before_any_payload(self):
        directory = self.checkout.joinpath(*PARTS)
        directory.mkdir(parents=True)
        # Sparse fixture: exercise the real size guard without allocating 8 MiB.
        with (directory / ("b" * 16)).open("wb") as output:
            output.truncate(reporter.MAX_CORPUS_BYTES + 1)
        result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn("8 MiB reporting bound", result.stderr)
        self.assertIn("Complete corpus report FAILED", result.stderr)

    def test_too_many_files_fail_without_partial_report(self):
        for index in range(reporter.MAX_FILES + 1):
            self.corpus(reporter.HEADER + f'string("{index}")\n'.encode())
        result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn("32-file", result.stderr)

    def test_summary_omits_large_payload_and_preserves_existing_bytes(self):
        files = [("/".join((*PARTS, "b" * 16)), "b" * 64, reporter.HEADER)]
        summary = self.root / "summary.md"
        summary.write_text("existing\n")
        with patch.dict(os.environ, {"GITHUB_STEP_SUMMARY": str(summary), "GITHUB_RUN_ID": "123"}), \
                patch.object(reporter, "SUMMARY_BUDGET", 2048):
            reporter.write_summary(PACKAGE, TARGET, SHA, files, "x" * 4000)
        text = summary.read_text()
        self.assertTrue(text.startswith("existing\n"))
        self.assertIn("Payload is in the step log", text)
        self.assertNotIn("x" * 100, text)
        self.assertLess(summary.stat().st_size, 2048)
        summary.write_text("x" * 2040)
        with patch.dict(os.environ, {"GITHUB_STEP_SUMMARY": str(summary)}), \
                patch.object(reporter, "SUMMARY_BUDGET", 2048):
            with self.assertRaisesRegex(ValueError, "summary would exceed"):
                reporter.write_summary(PACKAGE, TARGET, SHA, files, "")
        self.assertEqual(summary.read_text(), "x" * 2040)

    def test_report_rejects_symlink_components_and_symlink_files(self):
        data = reporter.HEADER + b'string("outside")\n'
        outside = self.root / "outside"
        path = self.corpus(data, root=outside)
        for index, component in enumerate(PARTS):
            with self.subTest(component=component):
                root = self.destination("source-" + str(index))
                parent = root.joinpath(*PARTS[:index])
                parent.mkdir(parents=True, exist_ok=True)
                (parent / component).symlink_to(outside.joinpath(*PARTS[:index + 1]), target_is_directory=True)
                result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET, root=root)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(result.stdout, "")
        directory = self.checkout.joinpath(*PARTS)
        directory.mkdir(parents=True)
        (directory / path.name).symlink_to(path)
        result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(path.read_bytes(), data)

    def test_report_rejects_unexpected_names_headers_and_nonregular_files(self):
        for index, kind in enumerate(("name", "header", "hash", "directory", "fifo")):
            with self.subTest(kind=kind):
                root = self.destination("invalid-" + str(index))
                directory = root.joinpath(*PARTS)
                directory.mkdir(parents=True)
                if kind == "name":
                    (directory / "unexpected").write_bytes(reporter.HEADER)
                elif kind == "header":
                    self.corpus(b"PRIVATE-CONTENT-NOT-A-CORPUS", root=root)
                elif kind == "hash":
                    (directory / ("b" * 16)).write_bytes(reporter.HEADER)
                elif kind == "directory":
                    (directory / ("b" * 16)).mkdir()
                else:
                    os.mkfifo(directory / ("b" * 16))
                result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET, root=root)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(result.stdout, "")
                self.assertNotIn("PRIVATE-CONTENT", result.stderr)

    def test_restore_timestamped_log_is_lossless_and_only_prints_replay(self):
        data = reporter.HEADER + bytes(range(256)) * 20
        log = self.log_for(data)
        timestamped = "noise before\n" + "".join("2026-11-05T00:00:00Z " + line + "\n" for line in log.splitlines()) + "noise after\n"
        source = self.save_log(timestamped)
        destination = self.destination()
        result = self.run_cli("restore", "--log", str(source), "--source-sha", SHA, root=destination)
        self.assertEqual(result.returncode, 0, result.stderr)
        name = hashlib.sha256(data).hexdigest()[:16]
        self.assertEqual(destination.joinpath(*PARTS, name).read_bytes(), data)
        self.assertIn(f"LE_FUZZ_REPLAY go test -run '^{TARGET}/{name}$' ./scan/", result.stdout)

    def test_restore_checks_all_existing_destinations_before_writing(self):
        self.corpus(reporter.HEADER + b'string("one")\n')
        self.corpus(reporter.HEADER + b'string("two")\n')
        result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET)
        source = self.save_log(result.stdout)
        files = reporter.decode_report(result.stdout, SHA)
        destination = self.destination()
        parent = destination.joinpath(*PARTS)
        parent.mkdir(parents=True)
        existing = parent / files[-1][1]
        existing.write_bytes(b"preserve existing bytes")
        result = self.run_cli("restore", "--log", str(source), "--source-sha", SHA, root=destination)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(existing.read_bytes(), b"preserve existing bytes")
        self.assertEqual(list(parent.iterdir()), [existing])

    def test_restore_rejects_tampering_missing_chunks_paths_and_source_sha(self):
        log = self.log_for(reporter.HEADER + b'x' * (reporter.CHUNK_BYTES * 2))
        original = [json.loads(line[len(reporter.MARKER):]) for line in log.splitlines()]
        changes = (
            lambda r: r[0].update(package="../scan/"),
            lambda r: r[0].update(target="FuzzUnknown"),
            lambda r: r[1].update(path="scan/testdata/fuzz/" + TARGET + "/../escape"),
            lambda r: r[1].update(path="/tmp/escape"),
            lambda r: r[1].update(sha256="b" * 64),
            lambda r: r[1].update(size=True),
            lambda r: r[2].update(base64="::error::invalid"),
            lambda r: r[2].update(index=1),
            lambda r: r.pop(2),
            lambda r: r.pop(),
            lambda r: r.append(r[0]),
            lambda r: r[0].update(files=2),
            lambda r: r[-1].update(files=True),
        )
        for index, change in enumerate(changes):
            with self.subTest(change=index):
                records = copy.deepcopy(original)
                change(records)
                with self.assertRaises((ValueError, TypeError)):
                    reporter.decode_report(reporter.encode_records(records), SHA)
        with self.assertRaisesRegex(ValueError, "source SHA"):
            reporter.decode_report(log, "b" * 40)
        with patch.object(reporter, "MAX_LOG_BYTES", 64):
            with self.assertRaisesRegex(ValueError, "16 MiB"):
                reporter.decode_report("x" * 65, SHA)

    def test_restore_rejects_symlink_parent_leaf_and_input(self):
        log = self.log_for()
        source = self.save_log(log)
        files = reporter.decode_report(log, SHA)
        outside = self.root / "outside"
        outside.mkdir()
        for index, component in enumerate(PARTS):
            with self.subTest(component=component):
                destination = self.destination("restore-" + str(index))
                parent = destination.joinpath(*PARTS[:index])
                parent.mkdir(parents=True, exist_ok=True)
                (parent / component).symlink_to(outside, target_is_directory=True)
                result = self.run_cli("restore", "--log", str(source), "--source-sha", SHA, root=destination)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(list(outside.iterdir()), [])
        destination = self.destination("restore-leaf")
        parent = destination.joinpath(*PARTS)
        parent.mkdir(parents=True)
        (parent / files[0][1]).symlink_to(outside / "missing")
        result = self.run_cli("restore", "--log", str(source), "--source-sha", SHA, root=destination)
        self.assertEqual(result.returncode, 1)
        self.assertTrue((parent / files[0][1]).is_symlink())
        source_link = self.root / "log-link"
        source_link.symlink_to(source)
        result = self.run_cli("restore", "--log", str(source_link), "--source-sha", SHA, root=destination)
        self.assertEqual(result.returncode, 1)

    def test_invalid_source_sha_and_target_are_clear_failures(self):
        result = self.run_cli("report", "--package", PACKAGE, "--target", TARGET, GITHUB_SHA="invalid")
        self.assertEqual(result.returncode, 1)
        self.assertIn("GITHUB_SHA", result.stderr)
        result = self.run_cli("report", "--package", "../scan/", "--target", TARGET)
        self.assertEqual(result.returncode, 1)
        self.assertIn("unsupported", result.stderr)


if __name__ == "__main__":
    unittest.main()
