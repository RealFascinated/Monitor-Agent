#!/usr/bin/env bash
set -euo pipefail

GITHUB_REPO="RealFascinated/Monitor-Agent"
DEFAULT_VERSION="2.0.1"
DEFAULT_API_ENDPOINT="https://monitor.fascinated.cc/api/v1/servers/ingest"
INSTALL_BIN="/usr/local/bin/monitor-agent"
CONFIG_DIR="/etc/monitor-agent"
CONFIG_FILE="${CONFIG_DIR}/config.yml"
SERVICE_NAME="monitor-agent"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
UNRAID_PLUGIN_DIR="/boot/config/plugins/monitor-agent"
UNRAID_STORED_BIN="${UNRAID_PLUGIN_DIR}/monitor-agent"
UNRAID_START_SCRIPT="/usr/local/bin/monitor-agent-start.sh"
UNRAID_CRON_FILE="/boot/config/plugins/dynamix/monitor-agent.cron"
UNRAID_UPDATE_CRON_FILE="/boot/config/plugins/dynamix/monitor-agent-update.cron"
UNRAID_GO_MARKER="# monitor-agent (installed by Monitor Agent)"

usage() {
  cat <<EOF
Install the Monitor agent on Linux (including Unraid).

Usage:
  sudo $0 <ingest_token> [options]

Options:
  --version VERSION       Agent release version (default: latest GitHub release)
  --api-endpoint URL      Ingest API endpoint (default: ${DEFAULT_API_ENDPOINT})
  --auto-update VALUE     Daily self-updates (default: true)
  -h, --help              Show this help message

Example:
  curl -fsSL https://github.com/${GITHUB_REPO}/releases/download/agent/v${DEFAULT_VERSION}/install.sh | sudo bash -s -- YOUR_INGEST_TOKEN
EOF
}

log() {
  printf '==> %s\n' "$*" >&2
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

require_root() {
  if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    die "run as root (e.g. sudo $0 <ingest_token>)"
  fi
}

require_linux() {
  case "$(uname -s)" in
    Linux) ;;
    *)
      die "this installer only supports Linux"
      ;;
  esac
}

is_unraid() {
  [[ -f /etc/unraid-version ]]
}

require_systemd() {
  is_unraid && die "systemd install path called on Unraid"
  command -v systemctl >/dev/null 2>&1 || die "systemctl not found"
  [[ -d /etc/systemd/system ]] || die "/etc/systemd/system not found"
  [[ "$(ps -p 1 -o comm= 2>/dev/null | tr -d '[:space:]')" == "systemd" ]] || die "init is not systemd"
}

configure_install_paths() {
  if ! is_unraid; then
    return
  fi
  CONFIG_DIR="${UNRAID_PLUGIN_DIR}"
  CONFIG_FILE="${CONFIG_DIR}/config.yml"
}

reload_unraid_cron() {
  command -v update_cron >/dev/null 2>&1 || die "update_cron not found (Unraid dynamix cron plugin required)"
  update_cron
  log "Reloaded Unraid cron (update_cron)"
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)
      die "unsupported architecture: $(uname -m)"
      ;;
  esac
}

escape_yaml_string() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

fetch_latest_version() {
  local releases tag
  releases="$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases?per_page=100")"
  tag="$(printf '%s\n' "$releases" | grep -oE '"tag_name"[[:space:]]*:[[:space:]]*"agent/v[^"]+"' | head -1 | sed -E 's/.*"agent\/v([^"]+)".*/\1/')"
  [[ -n "$tag" ]] || die "could not determine latest agent release version"
  echo "$tag"
}

download_release() {
  local version="$1"
  local arch="$2"
  local tmpdir="$3"
  local tag="agent/v${version}"
  local asset="monitor-agent-linux-${arch}"
  local base_url="https://github.com/${GITHUB_REPO}/releases/download/${tag}"
  local checksums_url="${base_url}/checksums.txt"
  local asset_url="${base_url}/${asset}"

  log "Downloading ${asset} (${tag})"
  curl -fsSL "$asset_url" -o "${tmpdir}/${asset}"

  log "Verifying checksum"
  curl -fsSL "$checksums_url" -o "${tmpdir}/checksums.txt"
  (
    cd "$tmpdir"
    grep " ${asset}\$" checksums.txt | sha256sum -c - >&2
  )

  DOWNLOADED="${tmpdir}/${asset}"
}

write_config() {
  local ingest_token="$1"
  local api_endpoint="$2"
  local token_escaped endpoint_escaped

  token_escaped="$(escape_yaml_string "$ingest_token")"
  endpoint_escaped="$(escape_yaml_string "$api_endpoint")"

  install -d -m 0755 "$CONFIG_DIR"
  cat >"$CONFIG_FILE" <<EOF
ingest_token: "${token_escaped}"
api_endpoint: "${endpoint_escaped}"
push_schedule: "*/15 * * * * *"
enable_docker: true
EOF
  chmod 0600 "$CONFIG_FILE"
}

write_service() {
  cat >"$SERVICE_FILE" <<EOF
[Unit]
Description=Monitor Agent
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_BIN}
Environment=MONITOR_CONFIG_FILE=${CONFIG_FILE}
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
}

write_update_timer() {
  cat >"/etc/systemd/system/${SERVICE_NAME}-update.service" <<EOF
[Unit]
Description=Monitor Agent Self Update
After=network-online.target

[Service]
Type=oneshot
ExecStart=${INSTALL_BIN} update

[Install]
WantedBy=multi-user.target
EOF

  cat >"/etc/systemd/system/${SERVICE_NAME}-update.timer" <<EOF
[Unit]
Description=Daily Monitor Agent update check

[Timer]
OnCalendar=daily
Persistent=true
RandomizedDelaySec=4h

[Install]
WantedBy=timers.target
EOF

  systemctl daemon-reload
  systemctl enable --now "${SERVICE_NAME}-update.timer"
}

install_unraid_start_script() {
  cat >"$UNRAID_START_SCRIPT" <<'EOF'
#!/bin/bash
set -euo pipefail

PLUGIN_DIR="/boot/config/plugins/monitor-agent"
STORED_BIN="${PLUGIN_DIR}/monitor-agent"
RUN_BIN="/usr/local/bin/monitor-agent"
CONFIG="${PLUGIN_DIR}/config.yml"
PIDFILE="/var/run/monitor-agent.pid"
LOG="/var/log/monitor-agent.log"

if [[ -f "$PIDFILE" ]]; then
  pid="$(cat "$PIDFILE" 2>/dev/null || true)"
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    exit 0
  fi
fi

[[ -f "$STORED_BIN" ]] || exit 1
install -m 0755 "$STORED_BIN" "$RUN_BIN"
nohup env MONITOR_CONFIG_FILE="$CONFIG" "$RUN_BIN" >>"$LOG" 2>&1 &
echo $! >"$PIDFILE"
EOF
  chmod 755 "$UNRAID_START_SCRIPT"
}

install_unraid_go_hook() {
  local go_file="/boot/config/go"

  [[ -f "$go_file" ]] || die "/boot/config/go not found; is the Unraid flash drive mounted?"

  if grep -qF "$UNRAID_GO_MARKER" "$go_file" 2>/dev/null; then
    return
  fi

  {
    printf '\n%s\n' "$UNRAID_GO_MARKER"
    printf '%s\n' "$UNRAID_START_SCRIPT"
  } >>"$go_file"
  log "Added array-start hook to ${go_file}"
}

install_unraid_watchdog_cron() {
  printf '%s\n' "*/5 * * * * ${UNRAID_START_SCRIPT} >>/var/log/monitor-agent.log 2>&1" >"$UNRAID_CRON_FILE"
  chmod 644 "$UNRAID_CRON_FILE"
  reload_unraid_cron
  log "Installed watchdog cron: ${UNRAID_CRON_FILE}"
}

install_unraid_update_cron() {
  printf '%s\n' "0 4 * * * ${INSTALL_BIN} update && cp -f ${INSTALL_BIN} ${UNRAID_STORED_BIN} && ${UNRAID_START_SCRIPT} >>/var/log/monitor-agent.log 2>&1" >"$UNRAID_UPDATE_CRON_FILE"
  chmod 644 "$UNRAID_UPDATE_CRON_FILE"
  reload_unraid_cron
  log "Installed daily update cron: ${UNRAID_UPDATE_CRON_FILE}"
}

remove_unraid_update_cron() {
  [[ -f "$UNRAID_UPDATE_CRON_FILE" ]] || return
  rm -f "$UNRAID_UPDATE_CRON_FILE"
  reload_unraid_cron
}

install_unraid_service() {
  [[ -d /boot/config ]] || die "/boot/config not found; is the Unraid flash drive mounted?"

  install_unraid_start_script
  install_unraid_go_hook
  install_unraid_watchdog_cron

  log "Starting monitor-agent"
  "$UNRAID_START_SCRIPT"
}

INGEST_TOKEN=""
VERSION=""
API_ENDPOINT="$DEFAULT_API_ENDPOINT"
AUTO_UPDATE="true"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      [[ $# -ge 2 ]] || die "--version requires a value"
      VERSION="$2"
      shift 2
      ;;
    --api-endpoint)
      [[ $# -ge 2 ]] || die "--api-endpoint requires a value"
      API_ENDPOINT="$2"
      shift 2
      ;;
    --auto-update)
      if [[ "$1" == *=* ]]; then
        AUTO_UPDATE="${1#*=}"
        shift
      elif [[ $# -ge 2 ]]; then
        AUTO_UPDATE="$2"
        shift 2
      else
        die "--auto-update requires true or false"
      fi
      if [[ "$AUTO_UPDATE" != "true" && "$AUTO_UPDATE" != "false" ]]; then
        die "--auto-update must be true or false"
      fi
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      die "unknown option: $1"
      ;;
    *)
      if [[ -n "$INGEST_TOKEN" ]]; then
        die "unexpected argument: $1"
      fi
      INGEST_TOKEN="$1"
      shift
      ;;
  esac
done

[[ -n "$INGEST_TOKEN" ]] || {
  usage
  die "ingest token is required"
}

require_root
require_linux
configure_install_paths

ARCH="$(detect_arch)"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

if [[ -z "$VERSION" ]]; then
  VERSION="$(fetch_latest_version)"
fi

DOWNLOADED=""
download_release "$VERSION" "$ARCH" "$TMPDIR"
[[ -f "$DOWNLOADED" ]] || die "failed to download monitor-agent binary"

if is_unraid; then
  log "Installing binary to ${UNRAID_STORED_BIN} (flash) and ${INSTALL_BIN} (runtime)"
  install -d -m 0755 "$UNRAID_PLUGIN_DIR" "$(dirname "$INSTALL_BIN")"
  install -m 0755 "$DOWNLOADED" "$UNRAID_STORED_BIN"
  install -m 0755 "$DOWNLOADED" "$INSTALL_BIN"
else
  log "Installing binary to ${INSTALL_BIN}"
  install -d -m 0755 "$(dirname "$INSTALL_BIN")"
  install -m 0755 "$DOWNLOADED" "$INSTALL_BIN"
fi

log "Writing config to ${CONFIG_FILE}"
write_config "$INGEST_TOKEN" "$API_ENDPOINT"

if is_unraid; then
  log "Installing Unraid service (persistent on flash, no systemd)"
  install_unraid_service

  if [[ "$AUTO_UPDATE" == "true" ]]; then
    log "Enabling daily self-updates"
    install_unraid_update_cron
  else
    log "Skipping daily self-updates"
    remove_unraid_update_cron
  fi

  log "Monitor agent installed and started"
  log "Binary (flash): ${UNRAID_STORED_BIN}"
  log "Binary (runtime): ${INSTALL_BIN}"
  log "Config: ${CONFIG_FILE}"
  log "Logs:   /var/log/monitor-agent.log"
  if [[ -f /var/run/monitor-agent.pid ]]; then
    log "PID:    $(cat /var/run/monitor-agent.pid)"
  fi
else
  require_systemd
  log "Installing systemd service"
  write_service
  systemctl daemon-reload
  systemctl enable --now "$SERVICE_NAME"

  if [[ "$AUTO_UPDATE" == "true" ]]; then
    log "Enabling daily self-updates"
    write_update_timer
  else
    log "Skipping daily self-updates"
  fi

  log "Monitor agent installed and started"
  systemctl --no-pager status "$SERVICE_NAME"
fi
