"""Thin gRPC adapter in front of the existing /v1/runs HTTP API."""
import asyncio
import importlib.util
import json
import os
import sys
import pathlib

import grpc
import httpx

# Load proto stubs directly by file path to avoid shadowing the pip `proto`
# package (site-packages/proto). The stubs live at ~/.hermes/hermes-agent/proto/.
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


class AgentServicer(agent_pb2_grpc.AgentServicer):
    def __init__(self):
        headers = {"Authorization": "Bearer " + API_SERVER_KEY} if API_SERVER_KEY else {}
        self._http = httpx.AsyncClient(base_url=GATEWAY_HTTP_BASE, timeout=None, headers=headers)

    async def Ping(self, request, context):
        return agent_pb2.PingResponse(version="hermes-agent-grpc-shim/0.2")

    async def RunTurn(self, request, context):
        payload = {
            "session_id": request.session_id,
            "task_id": request.task_id,
            "message": request.message,
            "workspace": request.workspace,
            "model": request.model,
            "provider": request.provider,
            "history": json.loads(request.history_json) if request.history_json else [],
            "attachments": list(request.attachments),
            "source": "webui",
            "input": request.message,
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
                        ename = pl.get("event", "") or event_type
                        text = pl.get("text", "")
                        name = pl.get("name", "")
                        preview = pl.get("preview", "")
                        error = pl.get("error", "")
                        yield agent_pb2.TurnEvent(
                            type=ename,
                            text=str(text) if text is not None else "",
                            name=str(name) if name is not None else "",
                            preview=str(preview) if preview is not None else "",
                            data_json=json.dumps(pl, ensure_ascii=False),
                            error=str(error) if error is not None else "",
                        )
                        if ename in ("done", "error", "run.failed"):
                            return
                    event_type = ""

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
