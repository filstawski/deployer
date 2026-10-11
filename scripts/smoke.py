"""Exercise built CLI/server binaries on loopback; no Docker or external network."""
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def run(*args, expected=0):
    result = subprocess.run(args, cwd=ROOT, capture_output=True, text=True, timeout=15)
    if result.returncode != expected:
        raise AssertionError(f"{args[0]} failed ({result.returncode}): {result.stderr}")
    return result.stdout


with tempfile.TemporaryDirectory(prefix="deployer-smoke-") as temporary:
    folder = Path(temporary)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    address = f"http://127.0.0.1:{port}"
    config = folder / "server.yaml"
    config.write_text(f"version: 1\nlisten: 127.0.0.1:{port}\nstate_dir: {folder / 'state'}\nbase_domain: localhost\n")
    token = folder / "operator.token"
    server = str(ROOT / "bin/deployerd")
    cli = str(ROOT / "bin/deployer")
    run(server, "token", "bootstrap", "--config", str(config), "--output", str(token))
    assert token.stat().st_mode & 0o077 == 0

    def command(*args, expected=0):
        output = run(cli, "--server", address, "--token-file", str(token), "--contexts-file", str(folder / "contexts.json"), "--json", *args, expected=expected)
        assert token.read_text().strip() not in output
        return json.loads(output)

    for iteration in range(2):
        with (folder / "server.log").open("a") as log:
            process = subprocess.Popen([server, "serve", "--config", str(config)], cwd=ROOT, stdout=log, stderr=log)
            try:
                for _ in range(100):
                    if process.poll() is not None:
                        raise AssertionError("server exited during startup")
                    try:
                        with urllib.request.urlopen(address + "/healthz", timeout=0.2) as response:
                            assert response.status == 200
                        break
                    except (OSError, urllib.error.URLError):
                        time.sleep(0.05)
                else:
                    raise AssertionError("server did not become ready")

                try:
                    urllib.request.urlopen(address + "/v1/projects", timeout=1)
                    raise AssertionError("anonymous request was accepted")
                except urllib.error.HTTPError as error:
                    assert error.code == 401

                assert command("server", "status")["data"]["api_version"] == 1
                if iteration == 0:
                    project = command("project", "add", "smoke", "--repo", "git@example.com:team/demo.git", "--idempotency-key", "smoke-create")["data"]
                    retried = command("project", "add", "smoke", "--repo", "git@example.com:team/demo.git", "--idempotency-key", "smoke-create")["data"]
                    assert project["id"] == retried["id"]
                    operation = command("project", "verify", "smoke", "--idempotency-key", "smoke-verify")["data"]
                    failure = command("operation", "inspect", operation["id"], "--wait", expected=2)
                    assert failure["error"]["code"] == "UNSUPPORTED_FEATURE"
                else:
                    projects = command("project", "list")["data"]
                    assert len(projects) == 1 and projects[0]["name"] == "smoke"
                    persisted = command("operation", "inspect", operation["id"])["data"]
                    assert persisted["status"] == "failed"
            finally:
                process.send_signal(signal.SIGTERM)
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    raise AssertionError("server did not shut down gracefully")
            assert process.returncode == 0

print("PASS: built CLI/API authentication, idempotency, durable operations, restart persistence, and graceful shutdown")
