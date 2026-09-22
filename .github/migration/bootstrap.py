#!/usr/bin/env python3
"""One-time, checksum-verified replacement of staged repository contents."""
import argparse
import base64
import hashlib
import io
import json
import lzma
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

SOURCE_SHA = '884c169415afd0549253e2d2c6d01549815ab24dbbff7b46dc12a849e181dcf5'
EXPECTED = '8e0b2f2f8447a9e549df26695f7c67ffb270598c84777796aae03648365cf374'
ASSETS = {
    'assets/trynet-icon.png': '2565f082e840d82346e2d233a4d5ae3e0345f14cd4edc25f1de3e99fe7302594',
    'assets/trynet.ico': '8b9e233d1a80107d2951b8a8256bdc62baed20e6f9bfaa767a83012c48681382',
}

def check(data, expected):
    actual = hashlib.sha256(data).hexdigest()
    if actual != expected:
        raise RuntimeError(f'Checksum mismatch: {actual} != {expected}')

def digest(root):
    h = hashlib.sha256()
    for path in sorted(root.rglob('*')):
        rel = path.relative_to(root)
        if path.is_file() and rel.parts[0] not in ('.github', 'VERSION') and '__pycache__' not in rel.parts and path.suffix != '.pyc':
            h.update(rel.as_posix().encode() + b'\0' + hashlib.sha256(path.read_bytes()).digest())
    return h.hexdigest()

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace', default='.')
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()
    root = Path(args.workspace).resolve()
    migration = root / '.github/migration'
    staged = root / '.trynet-bootstrap'
    if not args.dry_run and not (root / '.git').exists():
        raise RuntimeError('Refusing replacement outside a git checkout')
    chunks = [p.read_text().strip() for p in sorted(staged.glob('payload-*'))]
    if len(chunks) != 15 or len(chunks[6]) != 11999:
        raise RuntimeError('Unexpected staged source layout')
    chunks[6] = chunks[6][:11124] + 'C' + chunks[6][11124:]
    source = base64.b64decode(''.join(chunks), validate=True)
    check(source, SOURCE_SHA)
    with tempfile.TemporaryDirectory(prefix='trynet-restore-') as temporary:
        candidate = Path(temporary) / 'source'
        candidate.mkdir()
        with tarfile.open(fileobj=io.BytesIO(source), mode='r:xz') as archive:
            archive.extractall(candidate, filter='data')
        encoded = ''.join(p.read_text().strip() for p in sorted(staged.glob('part-*')))
        compressed = base64.b64decode(encoded[:len(encoded) // 4 * 4])
        decoder = lzma.LZMADecompressor()
        raw = bytearray()
        for offset in range(0, len(compressed), 128):
            try:
                raw.extend(decoder.decompress(compressed[offset:offset + 128]))
            except lzma.LZMAError:
                break
        restored = set()
        with tarfile.open(fileobj=io.BytesIO(raw), mode='r|') as archive:
            for member in archive:
                name = member.name.removeprefix('./')
                if name not in ASSETS:
                    continue
                data = archive.extractfile(member).read()
                check(data, ASSETS[name])
                destination = candidate / name
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(data)
                restored.add(name)
                if restored == set(ASSETS):
                    break
        if restored != set(ASSETS):
            raise RuntimeError('Original assets were not recovered')
        shutil.copy2(candidate / 'assets/trynet-icon.png', candidate / 'internal/fileshare/web/icon.png')
        ico = (candidate / 'assets/trynet.ico').read_bytes()
        recipe = json.loads((migration / 'syso-recipe.json').read_text())
        syso = b''.join(base64.b64decode(item['data']) if 'data' in item else ico[item['ico_offset']:item['ico_offset'] + item['size']] for item in recipe)
        check(syso, '5a128e48cef39614906c63c8668adbb5ec280fbaa603881f362457498315745d')
        (candidate / 'rsrc_windows_amd64.syso').write_bytes(syso)
        for name in ('docs.patch', 'code.patch', 'tooling.patch'):
            patch = (migration / name).read_text().replace('metadata["unpacked_archive"]], raw', 'metadata["unpacked_archive"], raw')
            subprocess.run(['git', 'apply', '--no-index', '-'], input=patch, text=True, cwd=candidate, check=True)
        shutil.rmtree(candidate / '.github', ignore_errors=True)
        (candidate / '.release-trigger').unlink(missing_ok=True)
        (candidate / 'VERSION').unlink(missing_ok=True)
        if digest(candidate) != EXPECTED:
            raise RuntimeError('Final source manifest mismatch: ' + digest(candidate))
        shutil.copytree(root / '.github', candidate / '.github')
        print('Verified source, original assets, documentation and release scripts:', EXPECTED, flush=True)
        if args.dry_run:
            return
        for path in root.iterdir():
            if path.name == '.git':
                continue
            if path.is_dir() and not path.is_symlink():
                shutil.rmtree(path)
            else:
                path.unlink()
        for path in candidate.iterdir():
            if path.is_dir():
                shutil.copytree(path, root / path.name)
            else:
                shutil.copy2(path, root / path.name)
        subprocess.run(['git', 'config', 'user.name', 'github-actions[bot]'], cwd=root, check=True)
        subprocess.run(['git', 'config', 'user.email', '41898282+github-actions[bot]@users.noreply.github.com'], cwd=root, check=True)
        subprocess.run(['git', 'add', '-A'], cwd=root, check=True)
        subprocess.run(['git', 'commit', '-m', 'feat: replace legacy project with verified TryNet CLI source'], cwd=root, check=True)
        subprocess.run(['git', 'push', 'origin', 'HEAD:main'], cwd=root, check=True)

if __name__ == '__main__':
    main()
