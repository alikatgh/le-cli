#!/usr/bin/env python3
"""Offline archive-gate fixtures; no compilers, release tools, or network."""
import gzip
import importlib.util
import io
from pathlib import Path
import re
import stat
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import warnings
import zipfile


sys.dont_write_bytecode = True
SCRIPT = Path(__file__).with_name("verify-release-snapshot.py")
SPEC = importlib.util.spec_from_file_location("release_snapshot", SCRIPT)
VALIDATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VALIDATOR)


class ReleaseSnapshotTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="release-snapshot-fixture-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.source = self.root / "source"
        self.source.mkdir()
        (self.source / "man").mkdir()
        self.documents = {"LICENSE": b"MIT license fixture\n", "README.md": b"# Install fixture\n", "THIRD_PARTY_NOTICES.txt": b"Third-party license fixture\n"}
        for name, data in self.documents.items():
            (self.source / name).write_bytes(data)
        (self.source / "man/le.1").write_bytes(b".TH LE 1\n")
        self.dist = self.root / "dist"
        (self.dist / "homebrew/Formula").mkdir(parents=True)

    def package(self, extension=None, mutate=None, directory=False):
        for system, arch in sorted(VALIDATOR.PLATFORMS):
            suffix = "zip" if system == "windows" else "tar.gz"
            name = "le_0.1.28-SNAPSHOT_" + system + "_" + arch + "." + suffix
            binary = "le.exe" if system == "windows" else "le"
            entries = [(binary, b"owned nonexecutable binary fixture\n", "file"),
                       *[(name, data, "file") for name, data in self.documents.items()],
                       ("man/le.1", b".TH LE 1\n", "file")]
            if directory:
                entries.append(("man/", b"", "directory"))
            if mutate and suffix == extension and arch == "amd64":
                entries = mutate(entries)
            path = self.dist / name
            if suffix == "zip":
                with zipfile.ZipFile(path, "w") as archive:
                    for name, data, kind in entries:
                        info = zipfile.ZipInfo(name)
                        info.create_system = 3
                        mode = {"file": stat.S_IFREG | 0o644, "directory": stat.S_IFDIR | 0o755,
                                "symlink": stat.S_IFLNK | 0o777}[kind]
                        info.external_attr = mode << 16
                        archive.writestr(info, data)
            else:
                with tarfile.open(path, "w:gz") as archive:
                    for name, data, kind in entries:
                        info = tarfile.TarInfo(name)
                        if kind == "symlink":
                            info.type, info.linkname = tarfile.SYMTYPE, "owned-fixture-target"
                        elif kind == "directory":
                            info.type = tarfile.DIRTYPE
                        else:
                            info.size = len(data)
                        archive.addfile(info, io.BytesIO(data) if kind == "file" else None)
        self.manifest()

    def manifest(self):
        archives = sorted([*self.dist.glob("*.tar.gz"), *self.dist.glob("*.zip")])
        checksums = {path.name: VALIDATOR.digest(path) for path in archives}
        (self.dist / "checksums.txt").write_text("".join(value + "  " + name + "\n"
                                                        for name, value in checksums.items()))
        formula = "on_macos\non_linux\n" + "\n".join(name + " " + value for name, value in checksums.items()
                                                          if name.endswith(".tar.gz"))
        (self.dist / "homebrew/Formula/le.rb").write_text(formula)

    def verify(self):
        VALIDATOR.verify_snapshot(self.dist, self.source)

    def test_complete_six_archive_snapshot_passes(self):
        self.package()
        self.verify()

    def test_safe_man_directory_entries_pass(self):
        self.package(directory=True)
        self.verify()

    def test_missing_each_document_is_rejected_in_each_format(self):
        for extension in ("tar.gz", "zip"):
            for missing in self.documents:
                with self.subTest(extension=extension, missing=missing):
                    self.package(extension, lambda entries: [entry for entry in entries if entry[0] != missing])
                    with self.assertRaisesRegex(ValueError, "missing archive files: " + re.escape(missing)):
                        self.verify()

    def test_changed_document_bytes_are_rejected_in_each_format(self):
        for extension in ("tar.gz", "zip"):
            for changed in self.documents:
                with self.subTest(extension=extension, changed=changed):
                    self.package(extension, lambda entries: [(name, b"x" * len(data), kind)
                                 if name == changed else (name, data, kind) for name, data, kind in entries])
                    with self.assertRaisesRegex(ValueError, re.escape(changed) + " does not match source bytes"):
                        self.verify()

    def test_hidden_and_unsafe_paths_are_rejected_in_each_format(self):
        for extension in ("tar.gz", "zip"):
            for unsafe in (".env", "man/.private", "../LICENSE", "/LICENSE", "man/../README.md",
                           "man\\le.1", "C:/LICENSE", "man//le.1"):
                with self.subTest(extension=extension, unsafe=unsafe):
                    self.package(extension, lambda entries: entries + [(unsafe, b"owned fixture", "file")])
                    with self.assertRaisesRegex(ValueError, "unsafe.*archive path"):
                        self.verify()

    def test_nonregular_documents_are_rejected_in_each_format(self):
        for extension in ("tar.gz", "zip"):
            with self.subTest(extension=extension):
                self.package(extension, lambda entries: [(name, b"", "symlink") if name == "LICENSE"
                                                         else (name, data, kind) for name, data, kind in entries])
                with self.assertRaisesRegex(ValueError, "nonregular archive entry: LICENSE"):
                    self.verify()

    def test_duplicate_entries_are_rejected_in_each_format(self):
        for extension in ("tar.gz", "zip"):
            with self.subTest(extension=extension):
                with warnings.catch_warnings():
                    warnings.filterwarnings("ignore", message="Duplicate name: 'README.md'", category=UserWarning)
                    self.package(extension, lambda entries: entries + [("README.md", self.documents["README.md"], "file")])
                with self.assertRaisesRegex(ValueError, "duplicate archive entry: README"):
                    self.verify()

    def test_incomplete_platform_matrix_is_rejected(self):
        self.package()
        next(self.dist.glob("*windows_amd64.zip")).unlink()
        with self.assertRaisesRegex(ValueError, "expected four tarballs and two Windows zips"):
            self.verify()

    def test_compressed_limit_precedes_snapshot_checksum_reads(self):
        self.package()
        with mock.patch.object(VALIDATOR, "MAX_COMPRESSED_BYTES", 1), \
                mock.patch.object(VALIDATOR, "digest", side_effect=AssertionError("checksum read")):
            with self.assertRaisesRegex(ValueError, "compressed-size limit"):
                self.verify()

    def test_bad_checksum_is_rejected(self):
        self.package()
        path = self.dist / "checksums.txt"
        lines = path.read_text().splitlines()
        lines[0] = "0" * 64 + "  " + lines[0].split()[1]
        path.write_text("\n".join(lines) + "\n")
        with self.assertRaisesRegex(ValueError, "archive checksum mismatch"):
            self.verify()

    def test_formula_still_requires_all_four_tarballs(self):
        self.package()
        (self.dist / "homebrew/Formula/le.rb").write_text("on_macos\non_linux\n")
        with self.assertRaisesRegex(ValueError, "all four tarballs and their checksums"):
            self.verify()

    def test_cli_success_and_missing_document_failure(self):
        self.package()
        command = [sys.executable, "-B", str(SCRIPT), "--dist", str(self.dist), "--source", str(self.source)]
        result = subprocess.run(command, capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Verified six snapshot archives", result.stdout)
        self.package("zip", lambda entries: [entry for entry in entries if entry[0] != "LICENSE"])
        result = subprocess.run(command, capture_output=True, text=True, check=False)
        self.assertEqual(result.returncode, 1)
        self.assertIn("missing archive files: LICENSE", result.stderr)
        self.assertEqual(result.stdout, "")


class MemoryArchive:
    def __init__(self, data, suffix):
        self.data = data
        self.suffix = suffix

    def stat(self):
        return type("ArchiveStat", (), {"st_size": len(self.data)})()

    def open(self, mode):
        assert mode == "rb"
        return io.BytesIO(self.data)


class EarlyArchiveRejectionTests(unittest.TestCase):
    def setUp(self):
        self.documents = {"LICENSE": b"MIT fixture\n", "README.md": b"Readme fixture\n"}
        self.entries = [("le.exe", b"binary fixture"), *self.documents.items(), ("man/le.1", b".TH LE 1\n")]
        self.allowed_members = len(self.entries) + 1

    def zip_bytes(self, entries=None):
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, "w") as archive:
            for name, data in self.entries if entries is None else entries:
                # Assigning after construction preserves a raw NUL in both ZIP
                # headers, reproducing ZipInfo's read-time name truncation.
                info = zipfile.ZipInfo(name.split("\0", 1)[0])
                info.filename = name
                info.create_system = 3
                info.external_attr = (stat.S_IFREG | 0o644) << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(info, data)
        return buffer.getvalue()

    def verify(self, data, suffix=".zip"):
        VALIDATOR.verify_archive(MemoryArchive(data, suffix), "le.exe" if suffix == ".zip" else "le",
                                 {"man/le.1"}, self.documents)

    def test_raw_zip_nul_path_rejected_before_payload_reads(self):
        entries = [("LICENSE\0../../.hidden", data) if name == "LICENSE" else (name, data)
                   for name, data in self.entries]
        data = self.zip_bytes(entries)
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            info = next(info for info in archive.infolist() if info.filename == "LICENSE")
            self.assertEqual(info.orig_filename, "LICENSE\0../../.hidden")
        with mock.patch.object(zipfile.ZipFile, "open", side_effect=AssertionError("payload read")):
            with self.assertRaisesRegex(ValueError, "unsafe.*archive path"):
                self.verify(data)

    def test_zip_normalization_mismatch_rejected_before_payload_reads(self):
        data = self.zip_bytes()
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            infos = archive.infolist()
        infos[0].filename = "normalized-name"
        with mock.patch.object(zipfile.ZipFile, "infolist", return_value=infos), \
                mock.patch.object(zipfile.ZipFile, "open", side_effect=AssertionError("payload read")):
            with self.assertRaisesRegex(ValueError, "normalized ZIP member name"):
                self.verify(data)

    def test_compressed_limit_rejected_before_opening_either_format(self):
        for suffix in (".zip", ".gz"):
            with self.subTest(suffix=suffix), mock.patch.object(VALIDATOR, "MAX_COMPRESSED_BYTES", 8):
                archive = MemoryArchive(b"x" * 9, suffix)
                with mock.patch.object(archive, "open", side_effect=AssertionError("archive opened")):
                    with self.assertRaisesRegex(ValueError, "compressed-size limit"):
                        VALIDATOR.verify_archive(archive, "le", {"man/le.1"}, self.documents)

    def test_zip_member_limit_precedes_crc_reads(self):
        data = bytearray(self.zip_bytes())
        central = data.index(b"PK\x01\x02")
        struct.pack_into("<I", data, central + 24, VALIDATOR.MAX_MEMBER_BYTES + 1)
        with mock.patch.object(zipfile.ZipFile, "open", side_effect=AssertionError("payload read")):
            with self.assertRaisesRegex(ValueError, "member-size limit"):
                self.verify(data)

    def test_zip_total_expansion_limit_precedes_crc_reads(self):
        data = self.zip_bytes()
        limit = sum(len(payload) for _, payload in self.entries) - 1
        with mock.patch.object(VALIDATOR, "MAX_EXPANDED_BYTES", limit), \
                mock.patch.object(zipfile.ZipFile, "open", side_effect=AssertionError("payload read")):
            with self.assertRaisesRegex(ValueError, "expanded-size limit"):
                self.verify(data)

    def test_zip_count_limit_precedes_crc_reads(self):
        data = self.zip_bytes(self.entries + [("extra-" + str(i), b"fixture") for i in range(129)])
        with mock.patch.object(zipfile.ZipFile, "open", side_effect=AssertionError("payload read")):
            with self.assertRaisesRegex(ValueError, "too many archive entries"):
                self.verify(data)

    def test_hard_member_count_limit_precedes_archive_open(self):
        archive = MemoryArchive(self.zip_bytes(), ".zip")
        with mock.patch.object(VALIDATOR, "MAX_MEMBERS", self.allowed_members - 1), \
                mock.patch.object(archive, "open", side_effect=AssertionError("archive opened")):
            with self.assertRaisesRegex(ValueError, "member-count limit"):
                VALIDATOR.verify_archive(archive, "le.exe", {"man/le.1"}, self.documents)

    def test_tar_expansion_limit_precedes_tar_parser(self):
        data = gzip.compress(b"x" * 65)
        with mock.patch.object(VALIDATOR, "MAX_EXPANDED_BYTES", 64), \
                mock.patch.object(tarfile, "open", side_effect=AssertionError("tar parser entered")):
            with self.assertRaisesRegex(ValueError, "expanded-size limit"):
                self.verify(data, ".gz")

    def test_tar_declared_member_limit_precedes_tar_parser(self):
        info = tarfile.TarInfo("le")
        info.size = VALIDATOR.MAX_MEMBER_BYTES + 1
        data = gzip.compress(info.tobuf() + bytes(1024))
        with mock.patch.object(tarfile, "open", side_effect=AssertionError("tar parser entered")):
            with self.assertRaisesRegex(ValueError, "member-size limit"):
                self.verify(data, ".gz")

    def test_tar_metadata_size_limit_precedes_payload_parser(self):
        info = tarfile.TarInfo("PaxHeader")
        info.type, info.size = tarfile.XHDTYPE, VALIDATOR.MAX_TAR_METADATA_BYTES + 1
        data = gzip.compress(info.tobuf() + bytes(1024))
        with mock.patch.object(tarfile, "open", side_effect=AssertionError("tar parser entered")):
            with self.assertRaisesRegex(ValueError, "metadata-size limit"):
                self.verify(data, ".gz")

    def test_tar_thousand_members_stop_at_the_budget_before_parser(self):
        data = gzip.compress(b"".join(tarfile.TarInfo("member-" + str(i)).tobuf() for i in range(1000)))
        original = tarfile.TarInfo.frombuf
        with mock.patch.object(tarfile, "open", side_effect=AssertionError("tar parser entered")), \
                mock.patch.object(tarfile.TarInfo, "frombuf", wraps=original) as headers:
            with self.assertRaisesRegex(ValueError, "too many archive entries"):
                self.verify(data, ".gz")
            self.assertLessEqual(headers.call_count, self.allowed_members + 1)

    def test_tar_metadata_chain_stops_before_recursive_parser(self):
        info = tarfile.TarInfo("PaxHeader")
        info.type = tarfile.XHDTYPE
        data = gzip.compress(info.tobuf() * 1000)
        original = tarfile.TarInfo.frombuf
        with mock.patch.object(tarfile, "open", side_effect=AssertionError("tar parser entered")), \
                mock.patch.object(tarfile.TarInfo, "frombuf", wraps=original) as headers:
            with self.assertRaisesRegex(ValueError, "too many tar headers"):
                self.verify(data, ".gz")
            self.assertLessEqual(headers.call_count, 3 * self.allowed_members)

    def test_supported_pax_metadata_still_passes(self):
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode="w:gz", format=tarfile.PAX_FORMAT) as archive:
            for name, data in self.entries:
                info = tarfile.TarInfo("le" if name == "le.exe" else name)
                info.pax_headers = {"mtime": "1234567890.123456789"}
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
        self.verify(buffer.getvalue(), ".gz")

    def test_zip_reads_are_chunked_and_crc_failure_remains_rejected(self):
        data = self.zip_bytes()
        original = zipfile.ZipExtFile.read
        requests = []
        def read(source, size=-1):
            requests.append(size)
            return original(source, size)
        with mock.patch.object(zipfile.ZipExtFile, "read", read):
            self.verify(data)
        self.assertTrue(requests)
        self.assertTrue(all(0 < size <= VALIDATOR.READ_CHUNK for size in requests))
        data = bytearray(data)
        central = data.index(b"PK\x01\x02")
        struct.pack_into("<I", data, central + 16, 0xDEADBEEF)
        with self.assertRaises(zipfile.BadZipFile):
            self.verify(data)


if __name__ == "__main__":
    unittest.main()
