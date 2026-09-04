#!/usr/bin/env bash
#
# Switch hermes-web-ui backend between native Go (hermes-web-go) and
# reference Python (hermes-webui-personal) on port 8787.
#
# Usage:
#   ./switch-backend.sh go          # build & launch Go server
#   ./switch-backend.sh python      # launch Python server (isolated state dir)
#   ./switch-backend.sh status      # what is listening on 8787
#
set -u

PORT="${PORT:-8787}"
GO_BIN="$(cd "$(dirname "$0")" && pwd)/hermes-web-go"
GO_LOG="/tmp/go-webui.log"
PY_DIR="/Users/adityahimawan/Development/hermes-webui-personal"
PY_STATE="${PY_STATE:-/tmp/pyrec-state}"
PY_LOG="/tmp/python-webui2.log"
HERMES_HOME="${HERMES_HOME:-$HOME/.hermes}"
PYTHON_BIN="$HERMES_HOME/hermes-agent/venv/bin/python"

stop_port() {
  local pids
  pids="$(lsof -tiTCP:"$PORT" -sTCP:LISTEN 2>/dev/null || true)"
  if [ -n "$pids" ]; then
    echo "[switch] stopping $(echo $pids | tr '\n' ' ' | sed 's/ $//') on :$PORT"
    kill $pids 2>/dev/null
    sleep 2
    pids="$(lsof -tiTCP:"$PORT" -sTCP:LISTEN 2>/dev/null || true)"
    [ -n "$pids" ] && kill -9 $pids 2>/dev/null
  else
    echo "[switch] nothing on :$PORT"
  fi
}

case "${1:-status}" in
  go)
    stop_port
    echo "[switch] building Go server..."
    ( cd "$(dirname "$0")" && go build -o hermes-web-go ./cmd/server ) || exit 1
    echo "[switch] launching Go server, log: $GO_LOG"
    ( cd "$(dirname "$0")" && exec ./hermes-web-go > "$GO_LOG" 2>&1 ) &
    ;;
  python)
    stop_port
    [ -d "$PY_STATE" ] || { echo "[switch] fresh state dir $PY_STATE"; mkdir -p "$PY_STATE"; }
    echo "[switch] launching Python server (isolated state), log: $PY_LOG"
    cat > /tmp/pyrec-run.sh <<EOF
export HERMES_HOME="$HERMES_HOME"
export HERMES_WEBUI_STATE_DIR="$PY_STATE"
export HERMES_AGENT_HTTP_URL="http://127.0.0.1:8642"
cd "$PY_DIR"
exec "$PYTHON_BIN" server.py > "$PY_LOG" 2>&1
EOF
    chmod +x /tmp/pyrec-run.sh
    /tmp/pyrec-run.sh &
    ;;
  status)
    echo "listeners on :$PORT:"
    lsof -iTCP:"$PORT" -sTCP:LISTEN -P -n 2>/dev/null || echo "  (none)"
    ;;
  *)
    echo "usage: $0 {go|python|status}" >&2
    exit 2
    ;;
esac

sleep 3
if curl -sf -m 3 "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
  echo "[switch] OK :$PORT healthy"
else
  echo "[switch] WARN :$PORT not responding yet; check log"
fi