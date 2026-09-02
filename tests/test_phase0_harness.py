import json
from pathlib import Path
import pytest
import sys
sys.path.insert(0, str(Path(__file__).parents[1] / "tools"))
from phase0_harness import compare, redact, normalize, parse_sse


def test_redacts_secrets_and_normalizes_volatile_values():
    data = {"Authorization": "Bearer abc", "session_id": "abcdef123456", "nested": {"api_key": "secret"}, "text": "id abcdef123456"}
    clean = redact(data)
    assert clean["Authorization"] == "<REDACTED>"
    assert clean["nested"]["api_key"] == "<REDACTED>"
    assert normalize(clean)["session_id"] == "<VOLATILE_1>"
    normalized = json.dumps(normalize(clean))
    assert '"session_id": "<VOLATILE_1>"' in normalized
    assert "id abcdef123456" in normalized


def test_compare_preserves_relative_ids_but_ignores_values():
    expected = [{"response": {"session_id": "one", "stream_id": "s1", "body": "2026-01-01T00:00:00Z"}}]
    actual = [{"response": {"session_id": "two", "stream_id": "s2", "body": "2026-02-02T00:00:00Z"}}]
    assert compare(expected, actual)[0]


def test_compare_detects_shape_change():
    ok, message = compare([{"response": {"status": 200}}], [{"response": {"status": 500}}])
    assert not ok
    assert "exchange 0" in message


def test_replay_keeps_json_body_and_valid_base64_fixture_encoding():
    from phase0_harness import request_body
    assert request_body({"body": {"prompt": "hello"}}) == b'{"prompt":"hello"}'
    assert request_body({"body_base64": 'eyJvayI6dHJ1ZX0=', "body_base64_safe": True}) == b'{"ok":true}'


def test_replay_rejects_invalid_base64_fixture():
    import binascii
    import pytest
    from phase0_harness import request_body
    with pytest.raises((ValueError, binascii.Error)):
        request_body({"body_base64": "***="})


def test_request_boundaries_reject_incomplete_rows_with_value_error(tmp_path):
    from phase0_harness import replay, validate_journey
    with pytest.raises(ValueError, match="path"):
        validate_journey([{"method": "GET"}])
    fixture = tmp_path / "bad.jsonl"
    fixture.write_text(json.dumps({"replay_request": {"method": "GET"}}) + "\n")
    with pytest.raises(ValueError, match="path"):
        replay("http://127.0.0.1:1", fixture, tmp_path / "out.jsonl")


def test_record_fails_closed_on_transport_error(tmp_path):
    from phase0_harness import record
    with pytest.raises(RuntimeError, match="record request failed"):
        record("http://127.0.0.1:1", [{"method": "GET", "path": "/health"}], tmp_path / "out.jsonl")
    assert not (tmp_path / "out.jsonl").exists()


def test_body_fixture_requires_trusted_root_and_rejects_escape(tmp_path):
    from phase0_harness import request_body
    root = tmp_path / "fixtures"; root.mkdir()
    (root / "ok.bin").write_bytes(b"safe")
    assert request_body({"body_fixture": "ok.bin"}, fixture_root=root) == b"safe"
    with pytest.raises(ValueError): request_body({"body_fixture": str(root / "ok.bin")}, fixture_root=root)
    with pytest.raises(ValueError): request_body({"body_fixture": "../secret"}, fixture_root=root)


def test_base64_requires_explicit_safe_marker():
    from phase0_harness import request_body
    with pytest.raises(ValueError): request_body({"body_base64": "***="})


def test_body_base64_type_and_size_fail_with_value_error():
    from phase0_harness import MAX_BODY_BYTES, request_body
    with pytest.raises(ValueError, match="body_base64"):
        request_body({"body_base64": 123, "body_base64_safe": True})
    with pytest.raises(ValueError, match="body_base64"):
        request_body({"body_base64": "YQ==", "body_base64_safe": "yes"})
    with pytest.raises(ValueError, match="exceeds"):
        request_body({"body_base64": "YQ==", "body_base64_safe": True}, max_body_bytes=0)


def test_record_rejects_oversized_response_body(tmp_path):
    import threading
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    from phase0_harness import record
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200); self.end_headers(); self.wfile.write(b"too large")
        def log_message(self, *_args): pass
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        with pytest.raises(RuntimeError, match="body exceeds"):
            record(f"http://127.0.0.1:{server.server_port}", [{"path": "/"}], tmp_path / "out", max_body_bytes=3)
    finally:
        server.shutdown(); server.server_close()


def test_record_rejects_symlink_output(tmp_path):
    from phase0_harness import record
    target = tmp_path / "target"; target.write_text("sentinel")
    link = tmp_path / "out"; link.symlink_to(target)
    with pytest.raises(ValueError, match="symlink"):
        record("http://127.0.0.1:1", [{"path": "/"}], link)


def test_normalization_preserves_arbitrary_hex_content():
    from phase0_harness import normalize
    assert normalize({"text": "abcdef123456", "session_id": "abcdef123456"})["text"] == "abcdef123456"


def test_volatile_mapping_is_shared_and_inconsistent_relationship_fails():
    expected = [{"response": {"session_id": "same"}}, {"response": {"session_id": "same"}}]
    actual = [{"response": {"session_id": "one"}}, {"response": {"session_id": "two"}}]
    assert not compare(expected, actual)[0]


def test_redacts_secrets_embedded_in_arbitrary_strings():
    value = "GET /x?access_token=topsecret&keep=yes header X-Api-Key: *** body {\"password\":\"pw\"}"
    cleaned = redact(value)
    assert "topsecret" not in cleaned and "abc123" not in cleaned and '"password":"pw"' not in cleaned
    assert "keep=yes" in cleaned


def test_redacts_entire_bearer_value_in_embedded_strings():
    cleaned = redact("prefix Authorization: Bearer topsecret-token, suffix")
    assert "topsecret-token" not in cleaned
    assert "Bearer <REDACTED>" in cleaned


def test_sse_compares_ordered_event_shape():
    expected = [{"response": {"headers": {"Content-Type": "text/event-stream"}, "body": "event: token\ndata: {\"x\":1}\n\nevent: done\ndata: ok\n\n"}}]
    actual = [{"response": {"headers": {"Date": "later", "Content-Type": "text/event-stream"}, "body": "event: done\ndata: ok\n\nevent: token\ndata: {\"x\":1}\n\n"}}]
    assert not compare(expected, actual)[0]
    assert parse_sse(expected[0]["response"]["body"])[0] == {"event": "token", "data": '{"x":1}'}


def test_health_runtime_fields_are_volatile_but_shape_is_compared():
    expected = [{"response": {"status": 200, "body": {"status": "ok", "sessions": 1, "server_started_at": 1, "uptime_seconds": 2, "accept_loop": {"requests_total": 3, "last_request_at": 4}}}}]
    actual = [{"response": {"status": 200, "body": {"status": "ok", "sessions": 1, "server_started_at": 9, "uptime_seconds": 99, "accept_loop": {"requests_total": 8, "last_request_at": 10}}}}]
    assert compare(expected, actual)[0]
    actual[0]["response"]["body"].pop("accept_loop")
    assert not compare(expected, actual)[0]


def test_replay_remaps_derived_session_reference(tmp_path):
    import threading
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    from phase0_harness import replay

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers()
            self.wfile.write(b'{"session_id":"abcdef123456","ok":true}')
        def do_GET(self):
            if self.path != "/api/session?session_id=abcdef123456": self.send_error(404); return
            self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers(); self.wfile.write(b'{"session_id":"abcdef123456"}')
        def log_message(self, *_args): pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler); threading.Thread(target=server.serve_forever, daemon=True).start()
    fixture, output = tmp_path / "fixture.jsonl", tmp_path / "output.jsonl"
    fixture.write_text("\n".join([
        json.dumps({"replay_request": {"method": "POST", "path": "/api/session/new"}}),
        json.dumps({"replay_request": {"method": "GET", "path": "/api/session?session_id=<ID>"}}),
    ]) + "\n")
    try:
        replay(f"http://127.0.0.1:{server.server_port}", fixture, output)
        rows = [json.loads(x) for x in output.read_text().splitlines()]
        assert rows[1]["response"]["status"] == 200
    finally:
        server.shutdown(); server.server_close()


def test_relevant_response_headers_are_preserved():
    expected = [{"response": {"headers": {"Date": "one", "Content-Type": "application/json", "X-Trace": "keep"}}}]
    actual = [{"response": {"headers": {"Date": "two", "Content-Type": "application/json", "X-Trace": "keep"}}}]
    assert compare(expected, actual)[0]


def test_replay_uses_redacted_but_unormalized_session_request(tmp_path):
    import threading
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    from phase0_harness import record, replay

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
            if self.path != "/session" or json.loads(body) != {"session_id": "session-real", "prompt": "hello"}:
                self.send_error(404, "session mismatch")
                return
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"session_id":"session-real","ok":true}')

        def log_message(self, *_args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        base = f"http://127.0.0.1:{server.server_port}"
        fixture, replayed = tmp_path / "recorded.jsonl", tmp_path / "replayed.jsonl"
        journey = [{"method": "POST", "path": "/session", "body": {"session_id": "session-real", "prompt": "hello"}}]
        record(base, journey, fixture)
        row = json.loads(fixture.read_text())
        assert row["request"]["body"]["session_id"] == "<VOLATILE_1>"
        assert row["replay_request"]["body"]["session_id"] == "session-real"
        replay(base, fixture, replayed)
        replayed_row = json.loads(replayed.read_text())
        assert replayed_row.get("response", {}).get("status") == 200, replayed_row
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


def test_record_captures_named_response_references_for_later_path_and_body(tmp_path):
    import threading
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    from phase0_harness import record

    seen = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
            seen.append((self.path, body))
            payload = {"session": {"session_id": "fresh-session"}} if self.path == "/api/session/new" else {"ok": True}
            self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers()
            self.wfile.write(json.dumps(payload).encode())

        def log_message(self, *_args): pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
    try:
        journey = [
            {"method": "POST", "path": "/api/session/new", "body": {}, "capture": {"session_id": "session.session_id"}},
            {"method": "POST", "path": "/api/session/<session_id>", "body": {"session_id": "<session_id>"}},
        ]
        output = tmp_path / "recorded.jsonl"
        record(f"http://127.0.0.1:{server.server_port}", journey, output)
        assert seen[1] == ("/api/session/fresh-session", {"session_id": "fresh-session"})
        rows = [json.loads(line) for line in output.read_text().splitlines()]
        assert rows[0]["replay_request"]["capture"] == {"session_id": "session.session_id"}
        assert rows[1]["replay_request"]["path"] == "/api/session/<session_id>"
        assert rows[1]["replay_request"]["body"]["session_id"] == "<session_id>"
    finally:
        server.shutdown(); server.server_close(); thread.join()
