#!/usr/bin/env python3
"""Exercise the HTTPS connection helper without a real credential or network."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('connect_client', Path(__file__).with_name('connect-client.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ConnectTests(unittest.TestCase):
    def exercise(self, fail=False, existing=False):
        with tempfile.TemporaryDirectory() as temp:
            home = Path(temp)
            binary = home / 'deployer'
            binary.touch()
            secret = 'dpl_' + 'a' * 64
            calls = []

            def run(args, **kwargs):
                calls.append(args)
                self.assertNotIn(secret, ' '.join(args))
                self.assertNotIn('DEPLOYER_TOKEN_FILE', kwargs['env'])
                if 'status' in args:
                    self.assertEqual(args[args.index('--server') + 1], 'https://api.example.com')
                    token = Path(args[args.index('--token-file') + 1])
                    self.assertEqual(token.read_text().strip(), secret)
                    self.assertEqual(token.stat().st_mode & 0o777, 0o600)
                    if fail:
                        return SimpleNamespace(returncode=3, stdout=json.dumps({'error': {'message': 'invalid token'}}))
                    data = {'name': 'deployer', 'api_version': 1}
                elif 'list' in args:
                    data = {'servers': {'pilot': {}} if existing else {}}
                else:
                    data = {}
                return SimpleNamespace(returncode=0, stdout=json.dumps({'data': data}))

            with patch.object(module.Path, 'home', return_value=home), \
                 patch.object(module.shutil, 'which', return_value=str(binary)), \
                 patch.object(module.sys, 'argv', ['connect-client.py', '--url', 'https://api.example.com']), \
                 patch.object(module.sys.stdin, 'isatty', return_value=True), \
                 patch.object(module.getpass, 'getpass', return_value=secret) as prompt, \
                 patch.object(module.subprocess, 'run', side_effect=run), \
                 contextlib.redirect_stdout(io.StringIO()) as out, \
                 contextlib.redirect_stderr(io.StringIO()):
                if existing:
                    with self.assertRaises(SystemExit):
                        module.connect()
                    prompt.assert_not_called()
                elif fail:
                    with self.assertRaisesRegex(RuntimeError, 'invalid token'):
                        module.connect()
                else:
                    module.connect()
                self.assertNotIn(secret, out.getvalue())
            files = list((home / '.config/deployer/credentials').glob('*'))
            if fail or existing:
                self.assertEqual(files, [])
                self.assertFalse(any('add' in args for args in calls))
            else:
                self.assertEqual([p.name for p in files], ['pilot.token'])
                self.assertEqual(files[0].stat().st_mode & 0o777, 0o600)
                self.assertTrue(any('add' in args and '--url' in args and '--ssh' not in args for args in calls))

    def test_https_connection_and_secret_handling(self):
        self.exercise()

    def test_failed_authentication_removes_temporary_secret(self):
        self.exercise(fail=True)

    def test_existing_context_is_not_overwritten(self):
        self.exercise(existing=True)


if __name__ == '__main__':
    unittest.main()
