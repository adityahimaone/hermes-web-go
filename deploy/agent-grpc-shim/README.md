# Agent Grpc Shim — deployable backup

Investigate "thinking/working text missing" in WebUI led to finding that
`~/.hermes/hermes-agent/gateway/platforms/agent_grpc.py` had been lost from
disk (LaunchAgent still runs but module was gone) — the shim silently fell
back to HTTP, and the HTTP `/v1/runs` events filter only forwards
token/done/tool (no reasoning/tool_complete), so the browser showed a
"sudden reply" with no thinking card.

These are the exact files living at runtime:

- `~/.hermes/hermes-agent/gateway/platforms/agent_grpc.py`
- `~/.hermes/hermes-agent/proto/agent.proto`
- `~/.hermes/hermes-agent/proto/agent_pb2.py` (generated, protobuf-patched)
- `~/.hermes/hermes-agent/proto/agent_pb2_grpc.py` (generated, patched import)
- `~/.hermes/hermes-agent/proto/__init__.py` (empty marker)

Install to the runtime location (hermes-agent git pull may wipe them):

```bash
cp -r deploy/agent-grpc-shim/agent_grpc.py ~/.hermes/hermes-agent/gateway/platforms/
mkdir -p ~/.hermes/hermes-agent/proto
cp -r deploy/agent-grpc-shim/proto/* ~/.hermes/hermes-agent/proto/
launchctl kickstart -k gui/$(id -u)/ai.hermes.agent-grpc-shim
```

Restart is required after install (KeepAlive=true in the plist will relaunch
it, but kickstart forces a fresh process + freshly reads the module).

Tasks:
- [x] Shim rewrite forwarding ALL event types (token, reasoning,
      tool, tool_complete, metering, context_status, interim_assistant,
      done) — parity with api/streaming.py SSE surface
- [x] Go relay passthrough in internal/agentclient/httpclient.go +
      internal/stream/writer.go so these events reach the browser
- [x] Verify shim ping: hermes-agent-grpc-shim/0.2 (TestGrpcShimPing PASS)
- [ ] Restart server 8787 with new binary and verify live SSE event wealth
- [ ] Verify thinking card + metering appear in browser