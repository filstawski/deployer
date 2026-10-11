#!/usr/bin/env python3
"""Exercise release bootstrap in temporary directories; no network or root."""
import hashlib
import os
from pathlib import Path
import platform
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
version = 'v0.1.0-test'
target_os = {'Darwin': 'darwin', 'Linux': 'linux'}[platform.system()]
target_arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64'}[platform.machine()]
asset = f'deployer_{version}_{target_os}_{target_arch}'
with tempfile.TemporaryDirectory(prefix='deployer-installer-test-') as tmp:
    folder = Path(tmp)
    release = folder / 'release files'
    release.mkdir()
    destination = folder / 'client bin'
    executable = release / asset
    executable.write_text('#!/bin/sh\nprintf "test release\\n"\n')
    (release / 'VERSION').write_text(version + '\n')
    checksum = hashlib.sha256(executable.read_bytes()).hexdigest()
    (release / 'SHA256SUMS').write_text(checksum + '  ' + asset + '\n')
    args = ['sh', str(root / 'install.sh'), 'client', '--from', str(release), '--bin-dir', str(destination)]
    def run(command=args, ok=True):
        r = subprocess.run(command, cwd=root, capture_output=True, text=True, timeout=10)
        assert (r.returncode == 0) == ok, (r.returncode, r.stdout, r.stderr)
        return r
    run()
    assert (destination / 'deployer').stat().st_mode & 0o111
    assert subprocess.check_output([str(destination / 'deployer')], text=True).strip() == 'test release'
    run()  # repeat succeeds
    before = (destination / 'deployer').read_bytes()
    executable.write_text('tampered payload')
    run(ok=False)
    assert (destination / 'deployer').read_bytes() == before
    executable.write_bytes(before)
    (release / 'SHA256SUMS').write_text((checksum + '  ' + asset + '\n') * 2)
    run(ok=False)
    (release / 'SHA256SUMS').write_text(checksum + '  ' + asset + '\n')
    (destination / 'deployer').unlink()
    unrelated = folder / 'unrelated'
    unrelated.write_text('keep me')
    (destination / 'deployer').symlink_to(unrelated)
    run(ok=False)
    assert unrelated.read_text() == 'keep me'
    run(args + ['--version', '../../bad'], ok=False)
    (destination / 'deployer').unlink()
    (destination / '.deployer-install').unlink()
    (destination / 'deployer').write_text('unmanaged command')
    run(ok=False)
print('PASS: installer fresh/repeat, checksum failure, duplicate checksum, symlink, invalid version, and unrelated file protection')
