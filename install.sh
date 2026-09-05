#!/usr/bin/env bash
#
# One-command installer for hermes-web-go.
#
#   curl -fsSL https://raw.githubusercontent.com/adityahimaone/hermes-web-go/main/install.sh | bash
#
# or, from a clone:
#
#   ./install.sh
#
# Installs: prebuilt Go binary (no Go toolchain needed) + embedded Python
# runtime shim (no system Python packages needed). Targets macOS (arm64/x86_64)
# and Linux (x86_64/aarch64).
#
# Env overrides:
#   HERMES_WEBGO_INSTALL_DIR   default ~/.hermes/web-go
#   HERMES_WEBGO_VERSION       default latest (or e.g. v0.1.0)
#   HERMES_WEBGO_REPO          default adityahimaone/hermes-web-go
set -euo pipefail

REPO="${HERMES_WEBGO_REPO:-adityahimaone/hermes-web-go}"
VERSION="${HERMES_WEBGO_VERSION:-latest}"
INSTALL_DIR="${HERMES_WEBGO_INSTALL_DIR:-$HOME/.hermes/web-go}"
HERMES_HOME="${HERMES_HOME:-$HOME/.hermes}"

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# ── 1. Detect platform ────────────────────────────────────────────────
OS="$(uname -s)"; ARCH="$(uname -m)"
case "$OS" in
  Darwin) GOOS=darwin ;;
  Linux)  GOOS=linux ;;
  *) fail "unsupported OS: $OS (macOS/Linux only)" ;;
esac
case "$ARCH" in
  arm64|aarch64) GOARCH=arm64 ;;
  x86_64)        GOARCH=amd64 ;;
  *) fail "unsupported arch: $ARCH" ;;
esac
say "platform: ${GOOS}/${GOARCH}"

# ── 2. Resolve release tag ────────────────────────────────────────────
if command -v curl >/dev/null 2>&1; then fetch() { curl -fsSL "$1"; }; dl() { curl -fSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then fetch() { wget -qO- "$1"; }; dl() { wget -qO "$2" "$1"; }
else fail "need curl or wget"
fi

if [ "$VERSION" = "latest" ]; then
  TAG="$(fetch "https://api.github.com/repos/${REPO}/releases/latest" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$TAG" ] || fail "cannot resolve latest release (set HERMES_WEBGO_VERSION=vX.Y.Z)"
else
  TAG="$VERSION"
fi
say "version: ${TAG}"

# ── 3. Download prebuilt binary (no Go toolchain required) ───────────
ASSET="hermes-web-go_${TAG#v}_${GOOS}_${GOARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
say "downloading ${ASSET}"
dl "$URL" "$TMP/asset.tar.gz" || fail "download failed: $URL  (release asset missing? see 'Build from source' in README)"
mkdir -p "$INSTALL_DIR"
tar -xzf "$TMP/asset.tar.gz" -C "$INSTALL_DIR"
chmod +x "$INSTALL_DIR/hermes-web-go"
say "binary installed: ${INSTALL_DIR}/hermes-web-go"

# ── 4. Python runtime shim (no pip install; stdlib venv only) ────────
# The gRPC shim needs python3 + grpcio. Bootstrap a tiny venv from the
# system python3 (always present on macOS; on Linux usually present) and
# pip-install ONLY grpcio+httpx into it — no other system changes.
PYBIN="$(command -v python3 || true)"
[ -n "$PYBIN" ] || fail "python3 not found (needed for the agent gRPC shim)"
if ! "$PYBIN" -c "import grpc, httpx" >/dev/null 2>&1; then
  say "creating shim venv at ${INSTALL_DIR}/shim-venv"
  "$PYBIN" -m venv "$INSTALL_DIR/shim-venv"
  "$INSTALL_DIR/shim-venv/bin/pip" -q install --upgrade pip
  "$INSTALL_DIR/shim-venv/bin/pip" -q install "grpcio" "httpx"
else
  say "system python3 already has grpc+httpx (reusing)"
  INSTALL_DIR_SHIM=""   # will use system python3
fi
SHIM_PY="${INSTALL_DIR_SHIM:-$PYBIN}"
[ -d "$INSTALL_DIR/shim-venv" ] && SHIM_PY="$INSTALL_DIR/shim-venv/bin/python"

# Install shim files from the tarball (we ship them in the release asset).
if [ -f "$INSTALL_DIR/agent_grpc.py" ]; then
  say "installing agent gRPC shim"
  mkdir -p "$HERMES_HOME/hermes-agent/gateway/platforms" "$HERMES_HOME/hermes-agent/proto"
  cp "$INSTALL_DIR/agent_grpc.py" "$HERMES_HOME/hermes-agent/gateway/platforms/"
  cp "$INSTALL_DIR"/proto/* "$HERMES_HOME/hermes-agent/proto/" 2>/dev/null || true
fi

# ── 5. LaunchAgent (macOS) / systemd user unit (Linux) ───────────────
if [ "$GOOS" = "darwin" ]; then
  PLIST="$HOME/Library/LaunchAgents/ai.hermes.web-go.plist"
  say "writing LaunchAgent ${PLIST}"
  cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>ai.hermes.web-go</string>
  <key>ProgramArguments</key><array>
    <string>${INSTALL_DIR}/hermes-web-go</string>
  </array>
  <key>EnvironmentVariables</key><dict>
    <key>HERMES_WEBUI_HOST</key><string>127.0.0.1</string>
    <key>HERMES_WEBUI_PORT</key><string>8787</string>
    <key>HERMES_WEBUI_STATIC_DIR</key><string>${INSTALL_DIR}/static</string>
    <key>HERMES_WEBUI_DATA_ROOT</key><string>${HERMES_HOME}/webui</string>
    <key>HERMES_WEBUI_STATE_DB_PATH</key><string>${HERMES_HOME}/state.db</string>
    <key>HERMES_WEBUI_AGENT_SOCKET</key><string>${HERMES_HOME}/webui/agent.sock</string>
    <key>HERMES_WEBUI_AGENT_TRANSPORT</key><string>auto</string>
    <key>HERMES_WEBUI_LEGACY_PROXY_URL</key><string></string>
  </dict>
  <key>KeepAlive</key><true/>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>${INSTALL_DIR}/web-go.log</string>
  <key>StandardErrorPath</key><string>${INSTALL_DIR}/web-go.log</string>
</dict></plist>
EOF
  # Shim LaunchAgent (gRPC unix-socket adapter in front of /v1/runs)
  PLIST2="$HOME/Library/LaunchAgents/ai.hermes.agent-grpc-shim.plist"
  say "writing LaunchAgent ${PLIST2}"
  cat > "$PLIST2" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>ai.hermes.agent-grpc-shim</string>
  <key>ProgramArguments</key><array>
    <string>${SHIM_PY}</string>
    <string>-m</string>
    <string>gateway.platforms.agent_grpc</string>
  </array>
  <key>WorkingDirectory</key><string>${HERMES_HOME}/hermes-agent</string>
  <key>EnvironmentVariables</key><dict>
    <key>HERMES_WEBUI_AGENT_SOCKET</key><string>${HERMES_HOME}/webui/agent.sock</string>
    <key>PYTHONPATH</key><string>${HERMES_HOME}/hermes-agent</string>
  </dict>
  <key>KeepAlive</key><true/>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>${INSTALL_DIR}/agent-grpc-shim.log</string>
  <key>StandardErrorPath</key><string>${INSTALL_DIR}/agent-grpc-shim.log</string>
</dict></plist>
EOF
  launchctl bootout "gui/$(id -u)/ai.hermes.web-go" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$PLIST"
  launchctl bootout "gui/$(id -u)/ai.hermes.agent-grpc-shim" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$PLIST2"
  launchctl kickstart -k "gui/$(id -u)/ai.hermes.web-go"
  launchctl kickstart -k "gui/$(id -u)/ai.hermes.agent-grpc-shim"
else
  UNIT_DIR="$HOME/.config/systemd/user"
  mkdir -p "$UNIT_DIR"
  say "writing systemd user unit"
  cat > "$UNIT_DIR/hermes-web-go.service" <<EOF
[Unit]
Description=Hermes WebUI (Go)
After=network.target

[Service]
ExecStart=${INSTALL_DIR}/hermes-web-go
Environment=HERMES_WEBUI_HOST=127.0.0.1
Environment=HERMES_WEBUI_PORT=8787
Environment=HERMES_WEBUI_STATIC_DIR=${INSTALL_DIR}/static
Environment=HERMES_WEBUI_DATA_ROOT=${HERMES_HOME}/webui
Environment=HERMES_WEBUI_STATE_DB_PATH=${HERMES_HOME}/state.db
Environment=HERMES_WEBUI_AGENT_SOCKET=${HERMES_HOME}/webui/agent.sock
Environment=HERMES_WEBUI_AGENT_TRANSPORT=auto
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
EOF
  cat > "$UNIT_DIR/hermes-agent-grpc-shim.service" <<EOF
[Unit]
Description=Hermes agent gRPC shim
Before=hermes-web-go.service

[Service]
ExecStart=${SHIM_PY} -m gateway.platforms.agent_grpc
WorkingDirectory=${HERMES_HOME}/hermes-agent
Environment=HERMES_WEBUI_AGENT_SOCKET=${HERMES_HOME}/webui/agent.sock
Environment=PYTHONPATH=${HERMES_HOME}/hermes-agent
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable --now hermes-agent-grpc-shim.service hermes-web-go.service
fi

# ── 6. Verify ─────────────────────────────────────────────────────────
say "waiting for health check"
for _ in $(seq 1 20); do
  if curl -fsS http://127.0.0.1:8787/health >/dev/null 2>&1; then
    say "OK  http://127.0.0.1:8787/"
    say "logs: ${INSTALL_DIR}/web-go.log"
    exit 0
  fi
  sleep 1
done
fail "health check failed after 20s — see ${INSTALL_DIR}/web-go.log"
