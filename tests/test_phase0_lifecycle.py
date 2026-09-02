import json
from pathlib import Path
import sys

import pytest

sys.path.insert(0, str(Path(__file__).parents[1] / "tools"))
from phase0_lifecycle import LifecycleError, run_lifecycle
from phase0_lifecycle import _stop


def _fake_server(path: Path) -> None:
    path.write_text("""
import json, os, signal
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

state = Path(os.environ['HERMES_WEBUI_STATE_DIR'])
home = Path(os.environ['HERMES_HOME'])
workspace = Path(os.environ['PHASE0_WORKSPACE'])
assert state.exists() and not any(state.iterdir())
assert home.exists() and not any(home.iterdir())
workspace.mkdir(parents=True, exist_ok=True)

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != '/health':
            self.send_error(404); return
        self.send_response(200); self.send_header('Content-Type', 'application/json'); self.end_headers()
        self.wfile.write(json.dumps({'status':'ok','sessions':0}).encode())
    def log_message(self, *_args): pass

server = ThreadingHTTPServer(('127.0.0.1', int(os.environ['HERMES_WEBUI_PORT'])), Handler)
signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
server.serve_forever()
""")


def test_lifecycle_uses_distinct_fresh_state_and_stops_both_servers(tmp_path):
    server = tmp_path / "server.py"; _fake_server(server)
    journey = tmp_path / "journey.json"; journey.write_text(json.dumps([{"path": "/health"}]))
    result = run_lifecycle(
        source=tmp_path,
        python=Path(sys.executable),
        server_script=server,
        journey=journey,
        output_dir=tmp_path / "output",
        timeout=5,
    )
    assert result["match"] is True
    assert result["state_dirs"][0] != result["state_dirs"][1]
    assert all(not Path(pid_file).exists() for pid_file in result["pid_files"])


def test_lifecycle_stops_owned_server_when_record_fails(tmp_path):
    server = tmp_path / "server.py"; _fake_server(server)
    journey = tmp_path / "journey.json"; journey.write_text(json.dumps([{"path": "/missing"}]))
    with pytest.raises(LifecycleError) as exc:
        run_lifecycle(
            source=tmp_path,
            python=Path(sys.executable),
            server_script=server,
            journey=journey,
            output_dir=tmp_path / "output",
            timeout=5,
            reject_http_errors=True,
        )
    assert exc.value.pid is not None
    import os
    with pytest.raises(OSError):
        os.kill(exc.value.pid, 0)


def test_stop_signals_group_after_parent_exits(tmp_path):
    import os
    import subprocess
    child = subprocess.Popen([sys.executable, "-c", "import os,time; child=os.fork(); time.sleep(30) if child == 0 else os._exit(0)"], start_new_session=True, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    child.wait(timeout=5)
    pid_file = tmp_path / "server.pid"; pid_file.write_text(str(child.pid))
    _stop(child, pid_file, 2)
    assert not pid_file.exists()


@pytest.mark.parametrize("kind", ["file", "symlink"])
def test_lifecycle_rejects_non_directory_output(tmp_path, kind):
    target = tmp_path / "target"; target.mkdir()
    output = tmp_path / "output"
    if kind == "file": output.write_text("x")
    else: output.symlink_to(target, target_is_directory=True)
    with pytest.raises(LifecycleError, match="output_dir"):
        run_lifecycle(source=tmp_path, python=Path(sys.executable), server_script=tmp_path / "none", journey=tmp_path / "none", output_dir=output)