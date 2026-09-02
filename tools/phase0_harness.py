#!/usr/bin/env python3
"""Bounded HTTP/SSE recorder, redactor, normalizer, and replay comparator."""
from __future__ import annotations
import argparse, base64, binascii, json, math, os, re, stat, sys, urllib.error, urllib.request
from pathlib import Path
from typing import Any

SECRET_KEY = re.compile(r"(?:authorization|cookie|set-cookie|api[_-]?key|password|token|secret|csrf|access[_-]?token)$", re.I)
VOLATILE_KEY = re.compile(r"(?:timestamp|created[_-]?at|updated[_-]?at|last[_-]?message[_-]?at|server[_-]?time|stream[_-]?id|run[_-]?id|task[_-]?id|request[_-]?id|session[_-]?id)$", re.I)
HEALTH_VOLATILE_KEYS = {"server_started_at", "uptime_seconds", "requests_total", "last_request_at"}
SECRET_PARAM = re.compile(r"(?i)([?&](?:access[_-]?token|api[_-]?key|authorization|password|token|secret|csrf|cookie)=)[^&#\s]+")
SECRET_HEADER = re.compile(r"(?i)(\bauthorization\s*[:=]\s*)(bearer\s+)([^,;\s]+)|(\b(?:cookie|set-cookie|x-api-key|api-key|password|token|secret|csrf)\s*[:=]\s*)([^,;\s]+)")

def _redact_header(match: re.Match[str]) -> str:
    if match.group(1): return match.group(1) + match.group(2) + "<REDACTED>"
    return match.group(4) + "<REDACTED>"
SECRET_JSON = re.compile(r'(?i)(["\\\'](?:authorization|cookie|set-cookie|x-api-key|api-key|password|token|secret|csrf)["\\\']\s*:\s*["\\\'])[^"\\\']*(["\\\'])')
HEX_ID = re.compile(r"(?<![A-Za-z0-9])[0-9a-f]{12,64}(?![A-Za-z0-9])", re.I)
ISO_TS = re.compile(r"\b20\d\d-\d\d-\d\d[T ][^\s\"']+\b")
VOLATILE_HEADERS = {"date", "server", "content-length", "x-request-id", "x-correlation-id", "x-trace-id", "set-cookie"}
MAX_BODY_BYTES = 10 * 1024 * 1024

def _read_bounded(response: Any, max_body_bytes: int) -> bytes:
    if max_body_bytes < 0: raise ValueError("max_body_bytes must be non-negative")
    content_length = response.headers.get("Content-Length")
    if content_length is not None:
        try:
            if int(content_length) > max_body_bytes: raise ValueError(f"response body exceeds {max_body_bytes} bytes")
        except ValueError as exc:
            if "exceeds" in str(exc): raise
    body = response.read(max_body_bytes + 1)
    if len(body) > max_body_bytes: raise ValueError(f"response body exceeds {max_body_bytes} bytes")
    return body

def _check_output_path(output: Path) -> None:
    if output.is_symlink(): raise ValueError("output path must not be a symlink")
    if output.exists() and not output.is_dir(): raise ValueError("output path must be a directory")

def _check_body_size(body: bytes, max_body_bytes: int, label: str) -> bytes:
    if len(body) > max_body_bytes: raise ValueError(f"{label} exceeds {max_body_bytes} bytes")
    return body

def redact(value: Any, key: str = "") -> Any:
    if SECRET_KEY.search(key): return "<REDACTED>"
    if isinstance(value, dict): return {k: redact(v, k) for k, v in value.items()}
    if isinstance(value, list): return [redact(v, key) for v in value]
    if isinstance(value, str):
        value = SECRET_PARAM.sub(r"\1<REDACTED>", value)
        value = SECRET_HEADER.sub(_redact_header, value)
        value = SECRET_JSON.sub(r"\1<REDACTED>\2", value)
        return re.sub(r"(?i)(bearer\s+)[^\s]+", r"\1<REDACTED>", value)
    return value

def normalize(value: Any, table: dict[str, str] | None = None, key: str = "") -> Any:
    table = table if table is not None else {}
    if VOLATILE_KEY.search(key) or key in HEALTH_VOLATILE_KEYS:
        raw = json.dumps(value, sort_keys=True)
        table.setdefault(raw, f"<VOLATILE_{len(table) + 1}>")
        return table[raw]
    if isinstance(value, dict): return {k: normalize(v, table, k) for k, v in value.items()}
    if isinstance(value, list): return [normalize(v, table, key) for v in value]
    if isinstance(value, str):
        value = ISO_TS.sub("<TS>", value)
        return HEX_ID.sub("<ID>", value) if re.search(r"(?:^|_)(?:id|ids)$", key, re.I) else value
    return value

def parse_sse(body: str) -> list[dict[str, str]]:
    events, fields = [], {}
    for line in body.replace("\r\n", "\n").split("\n"):
        if not line:
            if fields: events.append({"event": fields.get("event", "message"), "data": fields.get("data", "")}); fields = {}
        elif line.startswith("event:"): fields["event"] = line[6:].lstrip()
        elif line.startswith("data:"): fields["data"] = fields.get("data", "") + ("\n" if "data" in fields else "") + line[5:].lstrip()
    if fields: events.append({"event": fields.get("event", "message"), "data": fields.get("data", "")})
    return events

def _decode_json_body(value: Any) -> Any:
    if isinstance(value, str):
        try: return json.loads(value)
        except (TypeError, ValueError): return value
    return value


def _paired(e: Any, a: Any, mappings: dict[str, str], reverse: dict[str, str], key: str = "") -> bool:
    if key == "body":
        e, a = _decode_json_body(e), _decode_json_body(a)
    if key in HEALTH_VOLATILE_KEYS or re.search(r"(?:timestamp|created[_-]?at|updated[_-]?at|last[_-]?message[_-]?at|server[_-]?time)$", key, re.I):
        return True
    if VOLATILE_KEY.search(key):
        left, right = json.dumps(e, sort_keys=True), json.dumps(a, sort_keys=True)
        if left in mappings: return mappings[left] == right
        if right in reverse: return False
        mappings[left] = right; reverse[right] = left; return True
    if type(e) is not type(a): return False
    if isinstance(e, dict): return list(e) == list(a) and all(_paired(e[k], a[k], mappings, reverse, k) for k in e)
    if isinstance(e, list): return len(e) == len(a) and all(_paired(x, y, mappings, reverse, key) for x, y in zip(e, a))
    if isinstance(e, str):
        left = ISO_TS.sub("<TS>", e); right = ISO_TS.sub("<TS>", a)
        if re.search(r"(?:^|_)(?:id|ids)$", key, re.I):
            left, right = HEX_ID.sub("<ID>", left), HEX_ID.sub("<ID>", right)
        return left == right
    return e == a

def _clean_headers(headers: dict[str, Any]) -> dict[str, Any]:
    return {k: redact(v, k) for k, v in headers.items() if k.lower() not in VOLATILE_HEADERS}

def sanitize_exchange(exchange: dict[str, Any]) -> dict[str, Any]:
    exchange = dict(exchange)
    replay_request = exchange.pop("replay_request", None)
    clean = redact(exchange)
    replay_request = redact(replay_request) if replay_request is not None else None
    if isinstance(clean.get("request"), dict) and "headers" in clean["request"]:
        clean["request"]["headers"] = {
            k: v for k, v in _clean_headers(clean["request"]["headers"]).items()
            if k.lower() != "host"
        }
    if isinstance(clean.get("response"), dict) and "headers" in clean["response"]:
        clean["response"]["headers"] = _clean_headers(clean["response"]["headers"])
        ct = next((v for k, v in clean["response"]["headers"].items() if k.lower() == "content-type"), "")
        if "text/event-stream" in str(ct):
            clean["response"]["body"] = parse_sse(str(clean["response"].get("body", "")))
    clean = normalize(clean)
    if replay_request is not None:
        clean["replay_request"] = replay_request
    return clean

def load(path: Path) -> list[dict[str, Any]]: return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]

def _comparison_exchange(value: dict[str, Any]) -> dict[str, Any]:
    # replay_request is operational metadata, not parity evidence.
    return {k: v for k, v in value.items() if k != "replay_request"}

def compare(expected: list[dict[str, Any]], actual: list[dict[str, Any]]) -> tuple[bool, str]:
    if len(expected) != len(actual): return False, f"exchange count differs: expected {len(expected)}, got {len(actual)}"
    mappings, reverse = {}, {}
    for i, (left, right) in enumerate(zip(expected, actual)):
        left, right = redact(_comparison_exchange(left)), redact(_comparison_exchange(right))
        for item in (left, right):
            response = item.get("response", {}) if isinstance(item, dict) else {}
            if isinstance(response, dict) and "headers" in response:
                response["headers"] = _clean_headers(response["headers"])
                ct = next((v for k, v in response["headers"].items() if k.lower() == "content-type"), "")
                if "text/event-stream" in str(ct): response["body"] = parse_sse(str(response.get("body", "")))
                elif "json" in str(ct).lower(): response["body"] = _decode_json_body(response.get("body"))
        if not _paired(left, right, mappings, reverse): return False, f"exchange {i} differs"
    return True, f"{len(expected)} exchanges match after redaction/normalization"

def _validate_request(item: Any, where: str = "request") -> dict[str, Any]:
    if not isinstance(item, dict):
        raise ValueError(f"{where} must be an object")
    if not isinstance(item.get("method", "GET"), str) or not item.get("method", "GET"):
        raise ValueError(f"{where}.method must be a non-empty string")
    if not isinstance(item.get("path"), str) or not item["path"].startswith("/"):
        raise ValueError(f"{where}.path must be an absolute URL path")
    if "headers" in item:
        if not isinstance(item["headers"], dict):
            raise ValueError(f"{where}.headers must be an object")
        if any(not isinstance(k, str) or not k or not isinstance(v, str) for k, v in item["headers"].items()):
            raise ValueError(f"{where}.headers names and values must be strings")
    body_keys = {key for key in ("body", "body_base64", "body_fixture") if key in item}
    if len(body_keys) > 1:
        raise ValueError(f"{where} body forms are mutually exclusive")
    if "body_base64" in item and not isinstance(item["body_base64"], str):
        raise ValueError(f"{where}.body_base64 must be a string")
    if "body_fixture" in item and (not isinstance(item["body_fixture"], str) or not item["body_fixture"]):
        raise ValueError(f"{where}.body_fixture must be a non-empty string")
    if "capture" in item:
        if not isinstance(item["capture"], dict):
            raise ValueError(f"{where}.capture must be an object")
        if any(not isinstance(k, str) or not k or not isinstance(v, str) or not v for k, v in item["capture"].items()):
            raise ValueError(f"{where}.capture names and paths must be strings")
    if "timeout" in item:
        if isinstance(item["timeout"], bool) or not isinstance(item["timeout"], (int, float)) or not math.isfinite(item["timeout"]) or item["timeout"] <= 0:
            raise ValueError(f"{where}.timeout must be finite and positive")
    return item


def validate_journey(journey: Any) -> list[dict[str, Any]]:
    if not isinstance(journey, list) or not journey:
        raise ValueError("journey must be a non-empty JSON array")
    return [_validate_request(item, f"journey[{i}]") for i, item in enumerate(journey)]


def request_body(item: dict[str, Any], fixture_root: Path | None = None, max_body_bytes: int = MAX_BODY_BYTES) -> bytes | None:
    if not isinstance(item, dict):
        raise ValueError("request must be an object")
    if "body_base64" not in item and "body_fixture" not in item and "body" not in item:
        return None
    if "body_base64" in item:
        if not isinstance(item["body_base64"], str):
            raise ValueError("body_base64 must be a string")
        if item.get("body_base64_safe") is not True:
            raise ValueError("opaque body_base64 requires body_base64_safe=true")
        try: body = base64.b64decode(item["body_base64"], validate=True)
        except (ValueError, binascii.Error) as exc: raise ValueError("body_base64 is invalid") from exc
        return _check_body_size(body, max_body_bytes, "body_base64")
    if "body_fixture" in item:
        if fixture_root is None:
            raise ValueError("body_fixture requires explicit fixture_root")
        relative = Path(item["body_fixture"])
        if relative.is_absolute() or not relative.parts or ".." in relative.parts or any(part in ("", ".") for part in relative.parts):
            raise ValueError("body_fixture must be relative and stay within fixture_root")
        try:
            root_fd = os.open(fixture_root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        except OSError as exc:
            raise ValueError("fixture_root is not a trusted directory") from exc
        fd = root_fd
        try:
            for part in relative.parts[:-1]:
                next_fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
                if fd != root_fd: os.close(fd)
                fd = next_fd
            file_fd = os.open(relative.parts[-1], os.O_RDONLY | os.O_NOFOLLOW, dir_fd=fd)
            try:
                fstat = os.fstat(file_fd)
                if not stat.S_ISREG(fstat.st_mode): raise ValueError("body_fixture must be a regular file")
                if fstat.st_size > max_body_bytes: raise ValueError(f"body_fixture exceeds {max_body_bytes} bytes")
                with os.fdopen(file_fd, "rb") as handle:
                    return _check_body_size(handle.read(max_body_bytes + 1), max_body_bytes, "body_fixture")
            except OSError as exc:
                raise ValueError("body_fixture could not be read safely") from exc
        except OSError as exc:
            raise ValueError("body_fixture escapes fixture_root or is unavailable") from exc
        finally:
            if fd != root_fd: os.close(fd)
            os.close(root_fd)
    body = item.get("body")
    if body is None: return None
    if isinstance(body, bytes): return _check_body_size(body, max_body_bytes, "body")
    encoded = json.dumps(body, separators=(",", ":")).encode() if isinstance(body, (dict, list)) else str(body).encode()
    return _check_body_size(encoded, max_body_bytes, "body")

def _replace_references(value: Any, references: dict[str, str]) -> Any:
    if isinstance(value, dict): return {k: _replace_references(v, references) for k, v in value.items()}
    if isinstance(value, list): return [_replace_references(v, references) for v in value]
    if isinstance(value, str):
        for old, new in references.items(): value = value.replace(old, new)
    return value


def _extract(payload: Any, dotted_path: str) -> str:
    value = payload
    for part in dotted_path.split("."):
        if not isinstance(value, dict) or part not in value:
            raise ValueError(f"capture path not found: {dotted_path}")
        value = value[part]
    if not isinstance(value, (str, int, float)):
        raise ValueError(f"capture path is not scalar: {dotted_path}")
    return str(value)


def _template_request(item: dict[str, Any]) -> dict[str, Any]:
    return {k: item[k] for k in ("method", "path", "headers", "body", "body_base64", "body_base64_safe", "body_fixture", "capture", "timeout") if k in item}


def record(base: str, journey: list[dict[str, Any]], output: Path, references: dict[str, str] | None = None, fixture_root: Path | None = None, max_body_bytes: int = MAX_BODY_BYTES) -> None:
    _check_output_path(output)
    rows = []
    journey = validate_journey(journey)
    references = references if references is not None else {}
    for original in journey:
        item = _replace_references(original, references)
        method, path = item.get("method", "GET"), item["path"]
        req = urllib.request.Request(base.rstrip("/") + path, method=method, headers=item.get("headers", {}), data=request_body(item, fixture_root, max_body_bytes))
        replay_request = _template_request(original)
        try:
            with urllib.request.urlopen(req, timeout=float(item.get("timeout", 15))) as response:
                raw_body = _read_bounded(response, max_body_bytes).decode("utf-8", "replace")
                payload = None
                try:
                    payload = json.loads(raw_body)
                except (TypeError, ValueError): pass
                captures = original.get("capture", {})
                if not captures and isinstance(payload, dict):
                    session = payload.get("session", payload)
                    if isinstance(session, dict) and session.get("session_id"):
                        captures = {"ID": "session.session_id" if "session" in payload else "session_id"}
                for name, dotted_path in captures.items():
                    references[f"<{name}>"] = _extract(payload, dotted_path)
                rows.append({"request": {"method": method, "path": path, "headers": dict(req.header_items()), "body": item.get("body"), **({"body_base64": item["body_base64"]} if "body_base64" in item else {})}, "replay_request": replay_request, "response": {"status": response.status, "headers": dict(response.headers), "body": raw_body}})
        except Exception as exc:
            raise RuntimeError(f"record request failed: {method} {path}: {exc}") from exc
    inverse = {actual: placeholder for placeholder, actual in references.items()}
    prepared = []
    for row in rows:
        replay_request = row.get("replay_request")
        comparable = _replace_references({k: v for k, v in row.items() if k != "replay_request"}, inverse)
        if replay_request is not None:
            comparable["replay_request"] = replay_request
        prepared.append(sanitize_exchange(comparable))
    _check_output_path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    _check_output_path(output)
    output.write_text("\n".join(json.dumps(x, sort_keys=True) for x in prepared) + "\n")

def replay(base: str, fixture: Path, output: Path, references: dict[str, str] | None = None, fixture_root: Path | None = None) -> None:
    journey = []
    for row in load(fixture):
        if not isinstance(row, dict): raise ValueError("fixture row must be an object")
        request = row.get("replay_request") or row.get("request")
        _validate_request(request, "fixture.replay_request")
        journey.append({k: request[k] for k in ("method", "path", "headers", "body", "body_base64", "body_base64_safe", "body_fixture", "capture", "timeout") if k in request})
    record(base, journey, output, references, fixture_root=fixture_root or fixture.parent)

def main() -> int:
    parser = argparse.ArgumentParser(); sub = parser.add_subparsers(dest="command", required=True)
    for name in ("compare",):
        p = sub.add_parser(name); p.add_argument("expected", type=Path); p.add_argument("actual", type=Path)
    for name in ("record", "replay"):
        p = sub.add_parser(name); p.add_argument("base"); p.add_argument("fixture", type=Path); p.add_argument("output", type=Path); p.add_argument("--fixture-root", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "compare": ok, msg = compare(load(args.expected), load(args.actual)); print(msg); return 0 if ok else 1
    record(args.base, json.loads(args.fixture.read_text()), args.output, fixture_root=args.fixture_root) if args.command == "record" else replay(args.base, args.fixture, args.output, fixture_root=args.fixture_root)
    print(f"wrote {len(load(args.output))} exchanges"); return 0
if __name__ == "__main__": sys.exit(main())
