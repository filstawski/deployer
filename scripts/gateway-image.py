#!/usr/bin/env python3
"""Build the gateway for the connected Linux Docker daemon, without a Go base image."""
import json
import os
import pathlib
import subprocess

root = pathlib.Path(__file__).resolve().parents[1]
def run(*args, **kwargs):
    return subprocess.check_output(args, cwd=root, text=True, **kwargs).strip()
info = json.loads(run('docker', 'info', '--format', '{{json .}}'))
if info['OSType'] != 'linux':
    raise SystemExit('Deployer requires a Linux Docker engine')
arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(info['Architecture'])
if not arch:
    raise SystemExit('Unsupported Docker architecture: ' + info['Architecture'])
env = dict(os.environ, GOOS='linux', GOARCH=arch, CGO_ENABLED='0')
context = root / '.cache' / 'gateway-image'
context.mkdir(parents=True, exist_ok=True)
subprocess.run(['go', 'build', '-trimpath', '-o', str(context / 'gateway'), './cmd/deployer-gateway'], cwd=root, env=env, check=True)
(context / 'Dockerfile').write_text('FROM scratch\nCOPY gateway /gateway\nUSER 65532:65532\nENTRYPOINT ["/gateway"]\n')
subprocess.run(['docker', 'build', '--network', 'none', '-t', 'deployer-gateway:dev', str(context)], cwd=root, check=True)
fixture = root / '.cache' / 'docker-fixture'
fixture.mkdir(parents=True, exist_ok=True)
subprocess.run(['go', 'build', '-trimpath', '-o', str(fixture / 'hello'), './examples/hello'], cwd=root, env=env, check=True)
print('Gateway image and test fixture built for linux/' + arch)
