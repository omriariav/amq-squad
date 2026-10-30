#!/usr/bin/env python3
"""Create a normalized archive and per-file integrity manifest; no binaries in git."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import tarfile

p = argparse.ArgumentParser()
p.add_argument('tree', type=Path)
p.add_argument('version')
p.add_argument('executable')
p.add_argument('entry_args', nargs='*')
p.add_argument('--tool', action='append', default=[], metavar='NAME=PATH',
               help='helper pi looks up in <agent-dir>/bin (fd, rg); PATH is inside the tree')
a = p.parse_args()
root = a.tree.resolve()
out = Path(__file__).resolve().parent / 'assets'
files = []
with (out / 'payload.tar.gz').open('wb') as raw:
    with gzip.GzipFile(filename='', fileobj=raw, mode='wb', mtime=0) as gz:
        with tarfile.open(fileobj=gz, mode='w', format=tarfile.PAX_FORMAT) as tar:
            for f in sorted(root.rglob('*')):
                if f.is_symlink() and not f.resolve().is_relative_to(root):
                    raise SystemExit(f'symlink escapes payload tree: {f}')
                if f.is_dir():
                    if f.is_symlink():
                        raise SystemExit(f'directory symlink unsupported: {f}')
                    continue
                if not f.is_file():
                    raise SystemExit(f'not a regular file: {f}')
                data = f.read_bytes()
                name = f.relative_to(root).as_posix()
                mode = 0o555 if f.stat().st_mode & 0o111 else 0o444
                info = tarfile.TarInfo(name)
                info.size, info.mode = len(data), mode
                tar.addfile(info, io.BytesIO(data))
                files.append(dict(path=name, sha256=hashlib.sha256(data).hexdigest(), size=len(data), mode=mode))
manifest = dict(version=a.version, platform='darwin-arm64',
                sha256=hashlib.sha256((out / 'payload.tar.gz').read_bytes()).hexdigest(),
                executable=a.executable, args=a.entry_args,
                tools=dict(t.split('=', 1) for t in a.tool), files=files)
(out / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(json.dumps(dict(payload_bytes=(out / 'payload.tar.gz').stat().st_size,
                     unpacked_bytes=sum(f['size'] for f in files), files=len(files))))
