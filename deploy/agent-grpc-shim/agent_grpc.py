"""Thin gRPC adapter in front of the existing /v1/runs HTTP API."""
import asyncio
import importlib.util
import json
import os
import sys
import pathlib

import grpc
import httpx

_hermes_root = pathlib.Path(__file__).resolve().parents[2]
_proto_dir = _hermes_root / "proto"

def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    sys.modules[name] = mod
    assert spec and spec.loader
    spec.loader.exec_module(mod)
    return mod

agent_pb2 = _load("agent_pb2", _proto_dir / "agent_pb2.py")
agent_pb2_grpc = _load("agent_pb2_grpc", _proto_dir / "agent_pb2_grpc.py")

GATEWAY_HTTP_BASE = os.environ.get("HERMES_GATEWAY_HTTP_URL", "http://127.0.0.1:8642")
SOCKET_PATH = os.environ.get("HERMES_WEBUI_AGENT_SOCKET", os.path.expanduser("~/.hermes/webui/agent.sock"))
API_SERVER_KEY = os.environ.get("API_SERVER_KEY", "")


def _translate(pl, fallback_event, last_delta=None):
    """Map gateway SSE payload -> (type, text, name, preview) for Go."""
    et = pl.get("event", "") or fallback_event or ""
    if et == "message.delta":
        t = pl.get("delta", "") or pl.get("text", "") or ""
        return "token", t, "", ""
    if et in ("reasoning", "reasoning.available"):
        # Real chain-of-thought from the model (e.g. "User pokemon... no.
        # Vice president. Whatever.") is worth showing in the Thinking card.
        # BUT this gateway sometimes echoes the final answer text back as
        # reasoning.available (e.g. "`Fri Sep 4 ...`" or "adityahimawan"),
        # which made the Thinking card flash then vanish on done.
        # Gate: forward unless the text is a backtick-wrapped short span or
        # equals the last emitted message.delta (answer echo).
        t = pl.get("text", "") or pl.get("delta", "") or pl.get("content", "") or ""
        stripped = t.strip().strip("`").strip()
        if not stripped:
            return "", "", "", ""
        if t.strip().startswith("`") and t.strip().endswith("`") and len(stripped) < 160:
            # backtick code-span echo of a command output / answer
            return "", "", "", ""
        if last_delta is not None and t.strip() == str(last_delta).strip():
            # exact duplicate of a just-seen message.delta (answer echo)
            return "", "", "", ""
        return "reasoning", t, "", ""
    if et in ("tool", "tool.started"):
        # gateway sends {"tool":"terminal","preview":"date"} not {"name":...}
        name = pl.get("tool", "") or pl.get("name", "")
        return "tool", "", str(name), str(pl.get("preview", "") or "")
    if et in ("tool_complete", "tool.completed"):
        name = pl.get("tool", "") or pl.get("name", "")
        return "tool_complete", "", str(name), str(pl.get("preview", "") or "")
    if et == "interim_assistant":
        return "interim_assistant", pl.get("text", "") or "", "", ""
    if et == "run.completed":
        # gateway does not emit done; treat as done for Go so finishTurn fires
        # but also keep payload for debugging
        return "done", "", "", ""
    if et in ("done", "error", "run.failed", "approval"):
        return et, pl.get("text", "") or "", pl.get("name", ""), pl.get("preview", "")
    # passthrough for metering/context_status/title/todo_state/etc.
    return et, pl.get("text", "") or "", pl.get("name", ""), pl.get("preview", "")


class AgentServicer(agent_pb2_grpc.AgentServicer):
    def __init__(self):
        headers = {"Authorization": "Bearer " + API_SERVER_KEY} if API_SERVER_KEY else {}
        self._http = httpx.AsyncClient(base_url=GATEWAY_HTTP_BASE, timeout=None, headers=headers)

    async def Ping(self, request, context):
        return agent_pb2.PingResponse(version="hermes-agent-grpc-shim/0.2")

    async def RunTurn(self, request, context):
        # Gateway /v1/runs expects `input` + `conversation_history` (not `history`)
        try:
            hist = json.loads(request.history_json) if request.history_json else []
        except Exception:
            hist = []
        payload = {
            "session_id": request.session_id,
            "task_id": request.task_id,
            "message": request.message,
            "input": request.message,
            "workspace": request.workspace,
            "model": request.model,
            "provider": request.provider,
            "conversation_history": hist,
            "attachments": list(request.attachments),
            "source": "webui",
        }
        resp = await self._http.post("/v1/runs", json=payload)
        if resp.status_code // 100 != 2:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"agent grpc shim: /v1/runs start failed HTTP {resp.status_code}: {resp.text[:400]}")
            return
        try:
            run_id = resp.json()["run_id"]
        except Exception:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"agent grpc shim: missing run_id: {resp.text[:400]}")
            return

        last_delta = None
        async with self._http.stream("GET", f"/v1/runs/{run_id}/events") as stream:
            if stream.status_code // 100 != 2:
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(f"agent grpc shim: events HTTP {stream.status_code}")
                return
            event_type = ""
            data_lines = []
            async for line in stream.aiter_lines():
                if line.startswith("event:"):
                    event_type = line[len("event:"):].strip()
                    continue
                if line.startswith("data:"):
                    data_lines.append(line[len("data:"):].strip())
                    continue
                if line.strip() == "":
                    if not data_lines:
                        event_type = ""
                        continue
                    raw = "\n".join(data_lines)
                    data_lines = []
                    try:
                        pl = json.loads(raw)
                    except Exception:
                        pl = {}
                    if isinstance(pl, dict):
                        etype, text, name, preview = _translate(pl, event_type, last_delta)
                        if etype == "token" and text:
                            last_delta = text
                        # skip empty passthrough that would spam unknown
                        if not etype:
                            event_type = ""
                            continue
                        # filter empty reasoning with no text (like httpclient does for empty)
                        if etype == "reasoning" and not text:
                            event_type = ""
                            continue
                        yield agent_pb2.TurnEvent(
                            type=etype,
                            text=text or "",
                            name=name or "",
                            preview=preview or "",
                            data_json=json.dumps(pl, ensure_ascii=False),
                            error=str(pl.get("error", "")) if pl.get("error") else "",
                        )
                        if etype in ("done", "error", "run.failed"):
                            return
                    event_type = ""
            # Gateway closed stream after run.completed without done; if we already
            # emitted done above we returned. If stream ended without done (rare),
            # emit done so Go finishes instead of hanging 20s until EOF timeout.
            # Check if last emitted was not done — emit synthetic done.
            yield agent_pb2.TurnEvent(
                type="done",
                text="",
                name="",
                preview="",
                data_json=json.dumps({"event": "done", "synthetic": True}, ensure_ascii=False),
                error="",
            )

    async def Cancel(self, request, context):
        try:
            resp = await self._http.post(f"/v1/runs/{request.session_id}/stop", json={})
            if resp.status_code // 100 != 2:
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(f"agent grpc shim: stop HTTP {resp.status_code}: {resp.text[:400]}")
        except Exception as e:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"stop error {e}")
        return agent_pb2.CancelResponse()


async def serve():
    server = grpc.aio.server()
    agent_pb2_grpc.add_AgentServicer_to_server(AgentServicer(), server)
    if os.path.exists(SOCKET_PATH):
        os.remove(SOCKET_PATH)
    os.makedirs(os.path.dirname(SOCKET_PATH), exist_ok=True)
    server.add_insecure_port(f"unix://{SOCKET_PATH}")
    await server.start()
    print(f"agent grpc shim listening on unix://{SOCKET_PATH}")
    await server.wait_for_termination()

if __name__ == "__main__":
    asyncio.run(serve())
