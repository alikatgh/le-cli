#!/usr/bin/env python3
"""Verify the six GoReleaser snapshot archives without extracting their contents."""
import argparse
import gzip
import hashlib
import io
from pathlib import Path
import re
import stat
import sys
import tarfile
import zipfile


PLATFORMS = {(system, arch) for system in ("darwin", "linux", "windows")
             for arch in ("amd64", "arm64")}
ARCHIVE_NAME = re.compile(r"le_([A-Za-z0-9][A-Za-z0-9.+_-]*)_(darwin|linux|windows)_(amd64|arm64)\.(tar\.gz|zip)")

# These exceed the current CLI packages while bounding each of the six archives.
MAX_COMPRESSED_BYTES = 32 << 20
MAX_EXPANDED_BYTES = 64 << 20
MAX_MEMBER_BYTES = 32 << 20
MAX_MEMBERS = 128
MAX_TAR_METADATA_BYTES = 64 << 10
READ_CHUNK = 64 << 10


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def entry_name(name):
    require(name and not name.startswith("/") and "\\" not in name,
            "unsafe archive path")
    parts = (name[:-1] if name.endswith("/") else name).split("/")
    require(all(part and not part.startswith(".") and ":" not in part
                and not any(ord(char) < 32 or ord(char) == 127 for char in part)
                for part in parts), "unsafe or hidden archive path")
    return "/".join(parts)


def archive_size(path):
    size = path.stat().st_size
    require(0 < size <= MAX_COMPRESSED_BYTES, "archive compressed-size limit exceeded")
    return size


def bounded_tar_buffer(raw):
    buffer = io.BytesIO()
    try:
        with gzip.GzipFile(fileobj=raw, mode="rb") as decoded:
            while True:
                chunk = decoded.read(min(READ_CHUNK, MAX_EXPANDED_BYTES - buffer.tell() + 1))
                if not chunk:
                    break
                require(buffer.tell() + len(chunk) <= MAX_EXPANDED_BYTES,
                        "archive expanded-size limit exceeded")
                buffer.write(chunk)
        buffer.seek(0)
        return buffer
    except Exception:
        buffer.close()
        raise


def preflight_tar(buffer, allowed_members):
    # Inspect fixed-size headers with the stdlib decoder before TarFile can
    # consume PAX/GNU payloads or cache an unrestricted member list.
    metadata = {tarfile.XHDTYPE, tarfile.XGLTYPE, tarfile.GNUTYPE_LONGNAME, tarfile.GNUTYPE_LONGLINK}
    with buffer.getbuffer() as data:
        offset = headers = members = 0
        while offset < len(data):
            require(offset + tarfile.BLOCKSIZE <= len(data), "truncated tar header")
            block = data[offset:offset + tarfile.BLOCKSIZE].tobytes()
            if block == bytes(tarfile.BLOCKSIZE):
                for start in range(offset, len(data), READ_CHUNK):
                    require(not data[start:start + READ_CHUNK].tobytes().strip(b"\0"),
                            "nonzero trailing tar data")
                return
            headers += 1
            require(headers <= 3 * allowed_members, "too many tar headers")
            info = tarfile.TarInfo.frombuf(block, "utf-8", "surrogateescape")
            require(0 <= info.size <= MAX_MEMBER_BYTES, "archive member-size limit exceeded")
            if info.type in metadata:
                require(info.size <= MAX_TAR_METADATA_BYTES, "tar metadata-size limit exceeded")
            else:
                members += 1
                require(members <= allowed_members, "too many archive entries")
            offset += tarfile.BLOCKSIZE * (1 + (info.size + tarfile.BLOCKSIZE - 1) // tarfile.BLOCKSIZE)
            require(offset <= len(data), "truncated tar member")


def consume_member(source, size, expected=None, name="document"):
    consumed = 0
    while True:
        chunk = source.read(min(READ_CHUNK, size - consumed + 1))
        if not chunk:
            break
        require(consumed + len(chunk) <= size, "archive member exceeded declared size")
        if expected is not None:
            require(chunk == expected[consumed:consumed + len(chunk)], name + " does not match source bytes")
        consumed += len(chunk)
    require(consumed == size, "truncated archive member")


def verify_archive(path, binary, man_pages, documents):
    expected = {binary, *man_pages, *documents}
    allowed_members = len(expected) + 1  # Optional explicit man/ directory.
    require(allowed_members <= MAX_MEMBERS, "archive member-count limit exceeded")
    compressed_size = archive_size(path)
    seen = set()
    files = set()
    expanded = 0

    def member(name, is_directory, is_regular, size):
        nonlocal expanded
        name = entry_name(name)
        require(name not in seen, "duplicate archive entry: " + name)
        seen.add(name)
        require(0 <= size <= MAX_MEMBER_BYTES, "archive member-size limit exceeded")
        expanded += size
        require(expanded <= MAX_EXPANDED_BYTES, "archive expanded-size limit exceeded")
        if is_directory:
            require(name == "man" and size == 0, "unexpected archive directory: " + name)
            return None
        require(is_regular, "nonregular archive entry: " + name)
        require(name in expected, "unexpected archive file: " + name)
        require(size > 0, "empty archive file: " + name)
        files.add(name)
        if name in documents:
            require(size == len(documents[name]), name + " does not match source bytes")
        return name

    with path.open("rb") as raw:
        if path.suffix == ".zip":
            with zipfile.ZipFile(raw) as archive:
                infos = archive.infolist()
                require(len(infos) <= allowed_members, "too many archive entries")
                payloads = []
                for info in infos:
                    # ZipInfo.filename may have silently truncated an embedded
                    # NUL (or normalized separators). Validate the original too.
                    entry_name(info.orig_filename)
                    require(info.orig_filename == info.filename, "normalized ZIP member name")
                    require(0 <= info.compress_size <= compressed_size, "invalid compressed member size")
                    kind = stat.S_IFMT(info.external_attr >> 16)
                    name = member(info.orig_filename, info.is_dir() and kind in (0, stat.S_IFDIR),
                                  not info.is_dir() and kind in (0, stat.S_IFREG), info.file_size)
                    if name is not None:
                        payloads.append((name, info))
                require(files == expected, "missing archive files: " + ", ".join(sorted(expected - files)))
                # Metadata limits are established before any decompression/CRC
                # read. Chunked streams replace the unbounded testzip() pass.
                for name, info in payloads:
                    with archive.open(info) as source:
                        consume_member(source, info.file_size, documents.get(name), name)
        else:
            with bounded_tar_buffer(raw) as buffer:
                preflight_tar(buffer, allowed_members)
                with tarfile.open(fileobj=buffer, mode="r:") as archive:
                    payloads = []
                    for info in archive:
                        require(len(seen) < allowed_members, "too many archive entries")
                        name = member(info.name, info.isdir(), info.isfile(), info.size)
                        if name is not None:
                            payloads.append((name, info))
                    require(files == expected, "missing archive files: " + ", ".join(sorted(expected - files)))
                    for name, info in payloads:
                        with archive.extractfile(info) as source:
                            consume_member(source, info.size, documents.get(name), name)


def verify_snapshot(dist, source):
    documents = {}
    for name in ("LICENSE", "README.md", "THIRD_PARTY_NOTICES.txt"):
        path = source / name
        require(path.is_file() and not path.is_symlink(), "source document unavailable: " + name)
        documents[name] = path.read_bytes()
        require(documents[name], "empty source document: " + name)
    man_pages = {path.relative_to(source).as_posix() for path in (source / "man").glob("*.1")
                 if path.is_file() and not path.is_symlink()}
    require(man_pages, "expected committed man pages")
    archives = sorted([*dist.glob("*.tar.gz"), *dist.glob("*.zip")])
    require(len(archives) == 6, "expected four tarballs and two Windows zips")
    matrix = set()
    versions = set()
    for path in archives:
        match = ARCHIVE_NAME.fullmatch(path.name)
        require(match is not None and path.is_file() and not path.is_symlink(), "unexpected archive name/type")
        archive_size(path)  # Bound input before checksum reads as well as parsing.
        version, system, arch, extension = match.groups()
        require(extension == ("zip" if system == "windows" else "tar.gz"), "incorrect archive format")
        require((system, arch) not in matrix, "duplicate archive platform/architecture")
        matrix.add((system, arch))
        versions.add(version)
    require(matrix == PLATFORMS and len(versions) == 1, "archive platform/architecture/version set changed")
    checksums = {}
    for line in (dist / "checksums.txt").read_text().splitlines():
        match = re.fullmatch(r"([0-9a-fA-F]{64})\s+\*?(\S+)", line)
        require(match is not None, "malformed archive checksum")
        value, name = match.groups()
        require(name not in checksums, "duplicate archive checksum")
        checksums[name] = value.lower()
    require(set(checksums) == {path.name for path in archives}, "archive checksum set changed")
    for path in archives:
        require(path.stat().st_size > 0 and digest(path) == checksums[path.name], "archive checksum mismatch")
        binary = "le.exe" if path.suffix == ".zip" else "le"
        verify_archive(path, binary, man_pages, documents)
    formula = (dist / "homebrew/Formula/le.rb").read_text()
    require("on_macos" in formula and "on_linux" in formula, "Formula must support macOS and Linux")
    for path in archives:
        if path.name.endswith(".tar.gz"):
            require(path.name in formula and checksums[path.name] in formula,
                    "Formula must reference all four tarballs and their checksums")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", type=Path, default=Path("dist"))
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parents[2])
    args = parser.parse_args()
    try:
        verify_snapshot(args.dist, args.source)
    except (OSError, ValueError, tarfile.TarError, zipfile.BadZipFile, RuntimeError, EOFError) as error:
        print("Snapshot packaging validation failed: " + str(error), file=sys.stderr)
        return 1
    print("Verified six snapshot archives, source-matching LICENSE/README, safe entries, binaries/man pages, checksums, and the macOS/Linux Formula.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
