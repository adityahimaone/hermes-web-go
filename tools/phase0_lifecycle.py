#!/usr/bin/env python3
"""Run one journey against two fresh, isolated source servers."""
from __future__ import annotations

import argparse
import json
import os
import signal
import socket
import subprocess
import sys
import tempfile
import time
import uuid
import urllib.request
from pathlib import Path
from typing import Any

from phase0_harness import compare, load, record, replay


class LifecycleError(RuntimeError):
    def __init__(self, message: str, pid: int | None = None):
        super().__init__(message)
        self.pid = pid


def _free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def _wait_ready(base: str, process: subprocess.Popen[Any], timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise LifecycleError(f"source server exited before readiness (status {process.returncode})", process.pid)
        try:
            with urllib.request.urlopen(base + "/health", timeout=1) as response:
                if response.status == 200:
                    return
        except Exception:
            time.sleep(0.1)
    raise LifecycleError(f"source server readiness timed out after {timeout:g}s", process.pid)


def _group_gone(process: subprocess.Popen[Any]) -> bool:
    try:
        os.killpg(process.pid, 0)
    except ProcessLookupError:
        return True
    except PermissionError:
        return False
    try:
        result = subprocess.run(
            ["ps", "-axo", "pid=,pgid=,stat="],
            check=True, capture_output=True, text=True,
        )
    except (OSError, subprocess.SubprocessError):
        return False
    for line in result.stdout.splitlines():
        fields = line.split()
        if len(fields) >= 3 and fields[1] == str(process.pid) and not fields[2].startswith("Z"):
            return False
    return True




def _stop(process: subprocess.Popen[Any], pid_file: Path, timeout: float) -> None:
    group_exists = not _group_gone(process)
    if group_exists:
        try: os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError: pass
    try:
        process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        if not _group_gone(process):
            try: os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError: pass
        process.wait(timeout=timeout)
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline and not _group_gone(process):
        time.sleep(0.05)
    if not _group_gone(process):
        raise LifecycleError("source server process group remains after shutdown", process.pid)
    pid_file.unlink(missing_ok=True)
    if process.returncode not in (0, -signal.SIGTERM, -signal.SIGINT):
        raise LifecycleError(f"source server shutdown status {process.returncode}", process.pid)


def _has_http_error(path: Path) -> bool:
    return any("error" in row for row in load(path))


def _remove_owned_tree(root: Path) -> list[str]:
    failures: list[str] = []
    if not root.is_symlink() and not root.exists():
        return failures
    def remove(path: Path) -> None:
        try:
            if path.is_symlink() or path.is_file(): path.unlink()
            elif path.is_dir():
                for child in path.iterdir(): remove(child)
                path.rmdir()
            else: path.unlink()
        except OSError as exc:
            failures.append(f"{path}: {exc}")
    remove(root)
    return failures


def run_lifecycle(
    *, source: Path, python: Path, server_script: Path, journey: Path,
    output_dir: Path, timeout: float = 30, reject_http_errors: bool = True,
    retain_roots: bool = False,
) -> dict[str, Any]:
    source, python, server_script, journey = map(Path, (source, python, server_script, journey))
    if output_dir.is_symlink() or (output_dir.exists() and not output_dir.is_dir()):
        raise LifecycleError("output_dir must be a directory")
    spec = json.loads(journey.read_text())
    if not isinstance(spec, list) or not spec or any(not isinstance(item, dict) or "path" not in item for item in spec):
        raise LifecycleError("journey must be a non-empty JSON array of request objects")
    if output_dir.is_symlink() or (output_dir.exists() and not output_dir.is_dir()):
        raise LifecycleError("output_dir must be a directory")
    if output_dir.exists() and any(output_dir.iterdir()):
        output_dir = output_dir / f"run-{time.strftime('%Y%m%dT%H%M%S')}-{uuid.uuid4().hex[:8]}"
    try: output_dir.mkdir(parents=True, exist_ok=False)
    except OSError as exc: raise LifecycleError(f"output_dir setup failed: {exc}") from exc
    fixtures = [output_dir / "server-a.jsonl", output_dir / "server-b.jsonl"]
    roots: list[Path] = []
    try:
        for name in ("a", "b"):
            roots.append(Path(tempfile.mkdtemp(prefix=f"phase0-{name}-", dir=output_dir)))
        return _run_servers(
            source=source, python=python, server_script=server_script, spec=spec,
            output_dir=output_dir, fixtures=fixtures, roots=roots, timeout=timeout,
            reject_http_errors=reject_http_errors, fixture_root=journey.parent,
        )
    finally:
        if not retain_roots:
            failures = [failure for root in roots for failure in _remove_owned_tree(root)]
            if failures:
                raise LifecycleError("cleanup failed: " + "; ".join(failures))


def _run_servers(*, source, python, server_script, spec, output_dir, fixtures, roots, timeout, reject_http_errors, fixture_root):
    state_dirs, homes, workspaces, pid_files = [], [], [], []
    for root in roots:
        state, home, workspace = root / "state", root / "home", root / "workspace"
        state.mkdir(); home.mkdir(); workspace.mkdir()
        state_dirs.append(state); homes.append(home); workspaces.append(workspace)
        pid_files.append(root / "server.pid")

    for index in range(2):
        port = _free_port(); base = f"http://127.0.0.1:{port}"
        env = os.environ.copy()
        env.update({
            "HERMES_HOME": str(homes[index]),
            "HERMES_WEBUI_STATE_DIR": str(state_dirs[index]),
            "HERMES_WEBUI_HOST": "127.0.0.1",
            "HERMES_WEBUI_PORT": str(port),
            "HERMES_WEBUI_SKIP_ONBOARDING": "1",
            "HERMES_WEBUI_PRESERVE_ENV": "1",
            "PHASE0_WORKSPACE": str(workspaces[index]),
            "HERMES_WEBUI_DEFAULT_WORKSPACE": str(workspaces[index]),
            "HERMES_WEBUI_WORKSPACES": str(workspaces[index]),
        })
        log = (roots[index] / "server.log").open("wb")
        process = subprocess.Popen([str(python), str(server_script)], cwd=source, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        pid_files[index].write_text(str(process.pid))
        failure: BaseException | None = None
        try:
            _wait_ready(base, process, timeout)
            if index == 0:
                record(base, spec, fixtures[index], {"<workspace>": str(workspaces[index])}, fixture_root=fixture_root)
            else:
                replay(base, fixtures[0], fixtures[index], {"<workspace>": str(workspaces[index])}, fixture_root=fixture_root)
            if reject_http_errors and _has_http_error(fixtures[index]):
                raise LifecycleError(f"server {'AB'[index]} journey contains HTTP/transport error", process.pid)
        except BaseException as exc:
            failure = exc
        try:
            _stop(process, pid_files[index], timeout)
        except BaseException as exc:
            failure = failure or exc
        finally:
            log.close()
        if failure:
            if isinstance(failure, LifecycleError):
                raise failure
            raise LifecycleError(str(failure), process.pid) from failure

    ok, message = compare(load(fixtures[0]), load(fixtures[1]))
    if not ok:
        raise LifecycleError(message)
    return {
        "match": True, "message": message,
        "output_dir": str(output_dir),
        "fixtures": [str(path) for path in fixtures],
        "state_dirs": [str(path) for path in state_dirs],
        "home_dirs": [str(path) for path in homes],
        "workspaces": [str(path) for path in workspaces],
        "pid_files": [str(path) for path in pid_files],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--python", type=Path, required=True)
    parser.add_argument("--server-script", type=Path)
    parser.add_argument("--journey", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=30)
    args = parser.parse_args()
    server_script = args.server_script or args.source / "server.py"
    try:
        result = run_lifecycle(source=args.source, python=args.python, server_script=server_script, journey=args.journey, output_dir=args.output_dir, timeout=args.timeout)
    except LifecycleError as exc:
        print(f"lifecycle failed: {exc}", file=sys.stderr)
        return 1
    print(result["message"])
    print(f"artifacts: {result['output_dir']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
