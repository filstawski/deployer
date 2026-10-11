#!/usr/bin/env python3
"""Save a server-issued token privately and connect a local CLI over HTTPS."""
import argparse
import getpass
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit


def connect():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', required=True)
    parser.add_argument('--name', default='pilot')
    args = parser.parse_args()
    url = urlsplit(args.url)
    if url.scheme != 'https' or not url.hostname or url.username or url.password or url.query or url.fragment or url.path not in ('', '/'):
        parser.error('--url must be an HTTPS server origin')
    if not re.fullmatch(r'[a-z][a-z0-9-]{0,47}', args.name):
        parser.error('--name must be a short lowercase name')
    executable = shutil.which('deployer') or str(Path.home() / '.local/bin/deployer')
    if not Path(executable).is_file():
        parser.error('install the Deployer CLI first')
    if not sys.stdin.isatty():
        parser.error('run this in a terminal; the token prompt is hidden')
    # Do not silently inherit a different endpoint or credential from the shell.
    env = dict(os.environ)
    env.pop('DEPLOYER_SERVER', None)
    env.pop('DEPLOYER_TOKEN_FILE', None)

    def cli(*command):
        result = subprocess.run([executable, *command, '--json'], env=env, text=True, capture_output=True)
        try:
            envelope = json.loads(result.stdout)
        except ValueError:
            raise RuntimeError('CLI did not return a valid response') from None
        if result.returncode:
            raise RuntimeError(envelope.get('error', {}).get('message', 'CLI command failed'))
        return envelope['data']

    contexts = cli('server', 'list')
    if args.name in contexts['servers']:
        parser.error('that server context already exists; select another --name')
    directory = Path.home() / '.config/deployer/credentials'
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    path = directory / (args.name + '.token')
    if path.exists() or path.is_symlink():
        parser.error('credential destination already exists; select another --name')
    secret = getpass.getpass('Paste your Deployer laptop token (hidden): ').strip()
    if not re.fullmatch(r'dpl_[a-f0-9]{64}', secret):
        parser.error('invalid Deployer token')
    temporary = None
    saved = False
    try:
        with tempfile.NamedTemporaryFile(mode='w', dir=directory, prefix='.connect-', delete=False) as f:
            temporary = Path(f.name)
            os.fchmod(f.fileno(), 0o600)
            f.write(secret + '\n')
        status = cli('--server', args.url, '--token-file', str(temporary), 'server', 'status')
        if status.get('name') != 'deployer' or status.get('api_version') != 1:
            raise RuntimeError('the endpoint did not identify as a compatible Deployer server')
        # Link atomically, without replacing an existing credential.
        os.link(temporary, path)
        saved = True
        cli('server', 'add', args.name, '--url', args.url, '--credential-file', str(path))
    except Exception:
        if saved:
            path.unlink()
        raise
    finally:
        if temporary:
            temporary.unlink(missing_ok=True)
        secret = None
    cli('server', 'use', args.name)
    print('Connected to', args.url)
    print('Token stored in', path, 'with owner-only permissions.')
    print('Try: deployer list')


if __name__ == '__main__':
    try:
        connect()
    except (RuntimeError, OSError) as exc:
        sys.exit(str(exc))
    except (KeyboardInterrupt, EOFError):
        sys.exit('Connection cancelled.')
