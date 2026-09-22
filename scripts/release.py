#!/usr/bin/env python3
"""Build six CGO-free targets, verify UPX, and package binaries with licenses."""
from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = [(os_, arch) for os_ in ("windows", "linux", "darwin") for arch in ("amd64", "arm64")]
PACKED_TARGETS = {("windows", "amd64"), ("linux", "amd64"), ("linux", "arm64")}


def run(args: list[str], *, env: dict[str, str] | None = None) -> str:
    return subprocess.check_output(args, cwd=ROOT, env=env, text=True, stderr=subprocess.STDOUT)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def licenses() -> dict[str, bytes]:
    """Include the licenses shipped by pinned modules and the Go toolchain."""
    result = {"licenses/Go/LICENSE": (Path(run(["go", "env", "GOROOT"]).strip()) / "LICENSE").read_bytes()}
    text = run(["go", "list", "-m", "-json", "all"])
    decoder = json.JSONDecoder()
    while text.strip():
        module, end = decoder.raw_decode(text.lstrip())
        text = text.lstrip()[end:]
        if module.get("Main"):
            continue
        directory = module.get("Dir")
        if not directory:
            details = json.loads(run(["go", "mod", "download", "-json", module["Path"] + "@" + module["Version"]]))
            directory = details["Dir"]
        found = []
        for path in sorted(Path(directory).iterdir()):
            if path.is_file() and path.name.upper().startswith(("LICENSE", "NOTICE", "COPYING", "PATENTS")):
                name = "licenses/" + module["Path"] + "@" + module["Version"] + "/" + path.name
                result[name] = path.read_bytes()
                found.append(name)
        if not found:
            raise RuntimeError("No license found for dependency: " + module["Path"])
    return result


def package(path: Path, binary: Path, docs: dict[str, bytes], metadata: dict) -> None:
    entries = {**docs, binary.name: binary.read_bytes(), "BUILDINFO.json": (json.dumps(metadata, indent=2) + "\n").encode()}
    if path.suffix == ".zip":
        with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
            for name, data in sorted(entries.items()):
                info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
                info.compress_type = zipfile.ZIP_DEFLATED
                info.external_attr = (0o100755 if name == binary.name else 0o100644) << 16
                archive.writestr(info, data)
    else:
        with path.open("wb") as output, gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=0, compresslevel=9) as gz:
            with tarfile.open(fileobj=gz, mode="w") as archive:
                for name, data in sorted(entries.items()):
                    info = tarfile.TarInfo(name)
                    info.size = len(data)
                    info.mode = 0o755 if name == binary.name else 0o644
                    info.mtime = 0
                    archive.addfile(info, io.BytesIO(data))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", args.version):
        parser.error("invalid release version")
    if not re.fullmatch(r"[0-9a-f]{40}", args.commit):
        parser.error("commit must be a full Git SHA")
    upx = shutil.which("upx")
    if not upx:
        raise RuntimeError("Release builds require UPX; install the checksum-pinned version from the workflow")
    dist, work = ROOT / "dist", ROOT / ".release-work"
    for directory in (dist, work):
        shutil.rmtree(directory, ignore_errors=True)
        directory.mkdir()
    docs = {name: (ROOT / name).read_bytes() for name in ("README.md", "README.zh-CN.md", "LICENSE")}
    docs.update(licenses())
    report = {"version": args.version, "commit": args.commit, "go": run(["go", "version"]).strip(), "upx": run([upx, "--version"]).splitlines()[0], "targets": []}
    flags = "-s -w -X trynet/internal/buildinfo.Version=" + args.version + " -X trynet/internal/buildinfo.Commit=" + args.commit
    for goos, goarch in TARGETS:
        target = goos + "-" + goarch
        directory = work / target
        directory.mkdir()
        name = "trynet.exe" if goos == "windows" else "trynet"
        raw = directory / name
        env = {**os.environ, "CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch}
        print("Building", target, flush=True)
        print(run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags=" + flags, "-o", str(raw), "."], env=env), end="")
        metadata = {"version": args.version, "commit": args.commit, "goos": goos, "goarch": goarch, "cgo": False,
                    "unpacked_bytes": raw.stat().st_size, "unpacked_sha256": sha256(raw), "packed": False}
        final = raw
        if (goos, goarch) in PACKED_TARGETS:
            packed = directory / "packed" / name
            packed.parent.mkdir()
            print(run([upx, "--best", "--lzma", "-o", str(packed), str(raw)]), end="")
            print(run([upx, "-t", str(packed)]), end="")
            if packed.stat().st_size >= raw.stat().st_size:
                raise RuntimeError("UPX did not reduce size for " + target)
            final = packed
            metadata["packed"] = True
            metadata["packing_note"] = "UPX --best --lzma; integrity checked with upx -t"
        else:
            metadata["packing_note"] = "macOS intentionally not UPX-packed" if goos == "darwin" else "Windows ARM64 is delivered unpacked"
        metadata["binary_bytes"] = final.stat().st_size
        metadata["binary_sha256"] = sha256(final)
        extension = ".zip" if goos == "windows" else ".tar.gz"
        base = "trynet-v" + args.version + "-" + target
        metadata["archive"] = base + extension
        package(dist / metadata["archive"], final, docs, metadata)
        if metadata["packed"]:
            metadata["unpacked_archive"] = base + "-unpacked" + extension
            fallback = {**metadata, "packed": False, "binary_bytes": raw.stat().st_size, "binary_sha256": sha256(raw), "packing_note": "Unpacked compatibility build"}
            package(dist / metadata["unpacked_archive"], raw, docs, fallback)
        report["targets"].append(metadata)
        print(json.dumps(metadata), flush=True)
    (dist / "BUILDINFO.json").write_text(json.dumps(report, indent=2) + "\n")
    checksums = [sha256(path) + "  " + path.name for path in sorted(dist.iterdir()) if path.is_file()]
    (dist / "SHA256SUMS.txt").write_text("\n".join(checksums) + "\n")
    lines = ["# TryNet v" + args.version, "", "Go CLI with HTTP and WebSocket forwarding. WebSocket support was added in CLI v1.1.0; the v1.0.0 CLI returned 501 for upgrades. Older v1.0.x Wails application releases belong to the previous project.", "",
             "Run `trynet -port 3000`, then connect to `wss://<the-printed-host>.trycloudflare.com/ws` when the local service provides `/ws`. No extra WebSocket flag is needed.", "",
             "Source commit: `" + args.commit + "`", "", "| Target | Unpacked executable | Delivered executable | UPX |", "|---|---:|---:|---|"]
    for target in report["targets"]:
        lines.append("| {goos}/{goarch} | {unpacked_bytes:,} bytes | {binary_bytes:,} bytes | {status} |".format(**target, status="Yes" if target["packed"] else "No"))
    lines += ["", "Each package includes English/Chinese documentation and license files. `-unpacked` packages are compatibility alternatives, not debug builds.", "",
              "All six targets pass native unit tests and executable smoke checks before publication. The packaged Linux amd64 UPX and unpacked executables also pass real public WSS acceptance tests through temporary Cloudflare quick tunnels before publication.", "",
              "WebSocket tests cover text/binary, 1 MiB messages, fragmentation, ping/pong, close codes, 16 concurrent clients, origin authentication rejection and socket cleanup. Extension negotiation can fall back to uncompressed frames; the test report records the actual outcome. No raw TCP/UDP/SSH support or automatic session recovery is implied.", "",
              "Windows executables are not Authenticode-signed; macOS builds are not Developer ID-signed or notarized. UPX is executable compression, not encryption or a security boundary. If security software rejects a packed binary, use the unpacked package; do not disable protection.", "",
              "Verify downloads with SHA256SUMS.txt. Named/custom-domain provisioning remains experimental and unverified against a live Cloudflare account; public quick-tunnel WSS tests do not validate those account API operations."]
    (dist / "release-notes.md").write_text("\n".join(lines) + "\n")


if __name__ == "__main__":
    main()
