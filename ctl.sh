#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HERMES_HOME="${HERMES_HOME:-${HOME}/.hermes}"
PID_FILE="${HERMES_WEBUI_PID_FILE:-${HERMES_HOME}/web-go.pid}"
LOG_FILE="${HERMES_WEBUI_LOG_FILE:-${HERMES_HOME}/web-go.log}"
BIN="${HERMES_WEBUI_BIN:-${ROOT}/hermes-web-go}"
mkdir -p "$(dirname "${PID_FILE}")" "$(dirname "${LOG_FILE}")"

pid() { [[ -f "$PID_FILE" ]] && read -r p < "$PID_FILE" && [[ "$p" =~ ^[0-9]+$ ]] && kill -0 "$p" 2>/dev/null && printf '%s\n' "$p"; }
usage() { printf 'Usage: ./ctl.sh {start|stop|restart|status|logs}\n'; }
case "${1:-}" in
  start)
    if p="$(pid)"; then printf 'Already running (PID %s)\n' "$p"; exit 0; fi
    [[ -x "$BIN" ]] || { printf 'Binary missing/not executable: %s\n' "$BIN" >&2; exit 1; }
    nohup "$BIN" >>"$LOG_FILE" 2>&1 & printf '%s\n' "$!" > "$PID_FILE"
    printf 'Started PID %s\n' "$!" ;;
  stop)
    if p="$(pid)"; then kill -TERM "$p"; for _ in {1..50}; do kill -0 "$p" 2>/dev/null || break; sleep 0.1; done; rm -f "$PID_FILE"; printf 'Stopped PID %s\n' "$p"; else rm -f "$PID_FILE"; printf 'Not running\n'; fi ;;
  restart) "$0" stop; "$0" start ;;
  status)
    if p="$(pid)"; then printf 'running PID=%s log=%s\n' "$p" "$LOG_FILE"; else printf 'stopped\n'; fi ;;
  logs)
    [[ -f "$LOG_FILE" ]] && tail -n "${HERMES_WEBUI_LOG_LINES:-100}" "$LOG_FILE" || printf 'No log: %s\n' "$LOG_FILE" ;;
  *) usage; exit 2 ;;
esac
