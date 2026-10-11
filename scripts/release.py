#!/usr/bin/env python3
"""Build self-contained GitHub Release assets. Does not publish anything."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument('--version', required=True)
p.add_argument('--output', default='dist')
p.add_argument('--targets', default='darwin/arm64,darwin/amd64,linux/arm64,linux/amd64')
a = p.parse_args()
if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?', a.version):
    p.error('version must be a v-prefixed semantic release version')
output = Path(a.output).resolve()
if output.exists() and any(output.iterdir()):
    p.error('output directory must be empty; release assets are immutable')
targets = a.targets.split(',')
if any(x not in ('darwin/arm64', 'darwin/amd64', 'linux/arm64', 'linux/amd64') for x in targets) or len(set(targets)) != len(targets):
    p.error('unsupported or duplicate target')
output.mkdir(parents=True, exist_ok=True)
(root / '.cache').mkdir(exist_ok=True)
installer_bytes = (root / 'install.sh').read_bytes()
with tempfile.TemporaryDirectory(prefix='release-source-', dir=root / '.cache') as temporary:
    snapshot = Path(temporary)
    for name in ('cmd', 'internal', 'examples'):
        shutil.copytree(root / name, snapshot / name)
    for name in ('go.mod', 'go.sum'):
        shutil.copyfile(root / name, snapshot / name)
    source_digest = hashlib.sha256()
    for file in sorted(snapshot.rglob('*')):
        if file.is_file():
            source_digest.update(str(file.relative_to(snapshot)).encode() + b'\0' + file.read_bytes())
    for target in targets:
        goos, goarch = target.split('/')
        binaries = ['deployer', 'deployerd', 'deployer-gateway'] if goos == 'linux' else ['deployer']
        env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED='0')
        env.setdefault('GOCACHE', str(root / '.cache/go-build'))
        env.setdefault('GOMODCACHE', str(root / '.cache/go-mod'))
        for binary in binaries:
            filename = f'{binary}_{a.version}_{goos}_{goarch}'
            subprocess.run(['go', 'build', '-trimpath', '-ldflags', f'-s -w -X deployer/internal/version.Version={a.version}', '-o', str(output / filename), './cmd/' + binary], cwd=snapshot, env=env, check=True)
            print(filename, flush=True)
(output / 'VERSION').write_text(a.version + '\n')
(output / 'install.sh').write_bytes(installer_bytes)
(output / 'release.json').write_text(json.dumps({'version': a.version, 'repository': 'filstawski/deployer', 'targets': targets, 'source_sha256': source_digest.hexdigest()}, indent=2) + '\n')
checksums = []
for file in sorted(output.iterdir()):
    checksums.append(hashlib.sha256(file.read_bytes()).hexdigest() + '  ' + file.name)
(output / 'SHA256SUMS').write_text('\n'.join(checksums) + '\n')
print('Release files ready in', output, '(not published)')
