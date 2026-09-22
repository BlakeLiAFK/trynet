#!/usr/bin/env python3
"""Check the actual release archives, executable hashes, version, and help."""
from __future__ import annotations
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import zipfile


def check(dist: Path, archive_name: str, expected: dict) -> None:
    archive_path = dist / archive_name
    checksums = dict(line.split("  ", 1)[::-1] for line in (dist / "SHA256SUMS.txt").read_text().splitlines())
    assert hashlib.sha256(archive_path.read_bytes()).hexdigest() == checksums[archive_name], "archive checksum mismatch"
    with tempfile.TemporaryDirectory() as temp:
        root = Path(temp)
        if archive_path.suffix == ".zip":
            with zipfile.ZipFile(archive_path) as archive:
                for name in archive.namelist():
                    assert not Path(name).is_absolute() and ".." not in Path(name).parts
                archive.extractall(root)
        else:
            with tarfile.open(archive_path) as archive:
                archive.extractall(root, filter="data")
        for name in ["LICENSE", "README.md", "README.zh-CN.md", "BUILDINFO.json", "licenses/Go/LICENSE"]:
            assert (root / name).is_file(), "missing " + name
        info = json.loads((root / "BUILDINFO.json").read_text())
        assert info["version"] == expected["version"] and info["commit"] == expected["commit"]
        executable = root / ("trynet.exe" if info["goos"] == "windows" else "trynet")
        assert hashlib.sha256(executable.read_bytes()).hexdigest() == info["binary_sha256"], "executable checksum mismatch"
        executable.chmod(0o755)
        result = subprocess.run([str(executable), "-version"], capture_output=True, text=True, timeout=15, check=True)
        assert "v" + info["version"] in result.stdout and info["commit"] in result.stdout, result.stdout
        help_result = subprocess.run([str(executable), "-h"], capture_output=True, text=True, timeout=15, check=True)
        assert "-version" in help_result.stdout + help_result.stderr
        print("PASS", archive_name, result.stdout.strip())


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", required=True)
    parser.add_argument("--dist", type=Path, default=Path("dist"))
    args = parser.parse_args()
    native = subprocess.check_output(["go", "env", "GOOS", "GOARCH"], text=True).split()
    assert "-".join(native) == args.target, ("unexpected runner architecture", native, args.target)
    report = json.loads((args.dist / "BUILDINFO.json").read_text())
    target = next(t for t in report["targets"] if t["goos"] + "-" + t["goarch"] == args.target)
    check(args.dist, target["archive"], target)
    if "unpacked_archive" in target:
        check(args.dist, target["unpacked_archive"], target)


if __name__ == "__main__":
    main()
