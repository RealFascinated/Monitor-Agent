#!/bin/bash
set -euo pipefail

REPO_RAW_URL="https://raw.githubusercontent.com/RealFascinated/Monitor-Agent/master/agent.sh"
INSTALL_PATH="/usr/local/sbin/monitor-agent"
RUN_PATH="/usr/local/sbin/monitor-agent-run"
AUTO_UPDATE_FILE="/etc/default/monitor-agent"
AUTO_UPDATE_STATE_DIR="/var/lib/monitor-agent"
AUTO_UPDATE_INTERVAL_SECONDS=3600
RUN_INTERVAL_SECONDS=15
CRON_FILE="/etc/cron.d/monitor-agent"
UNRAID_CRON_FILE="/boot/config/plugins/dynamix/monitor-agent.cron"
SYSTEMD_SERVICE="/etc/systemd/system/monitor-agent.service"
SYSTEMD_TIMER="/etc/systemd/system/monitor-agent.timer"
LOG_FILE="/var/log/monitor-agent.log"
DEPENDENCIES=(curl jq ip)
AUTO_UPDATE=true
AUTO_UPDATE_EXPLICIT=false
INGEST_TOKEN="${INGEST_TOKEN:-}"

usage() {
    cat <<EOF
Usage: $0 [COMMAND] [OPTIONS]

Install or remove the Monitor Agent from GitHub.

Commands:
  install         Install the agent (default)
  uninstall       Remove the agent, scheduler, and log file

Options:
  --token TOKEN       Ingest API token (or set INGEST_TOKEN env var)
  --no-auto-update    Disable automatic agent updates from GitHub (enabled by default)
  --auto-update       Enable automatic agent updates from GitHub (default)

Auto-update checks GitHub at most once per hour by default.
The agent runs every 15 seconds (systemd timer when available, otherwise cron).

Examples:
  sudo $0 --token "your-token-here"
  sudo INGEST_TOKEN="your-token-here" $0
  sudo $0 --no-auto-update --token "your-token-here"
  sudo $0 uninstall
EOF
}

log() {
    echo "[monitor-agent] $*"
}

die() {
    echo "[monitor-agent] ERROR: $*" >&2
    exit 1
}

require_root() {
    if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
        die "This script must be run as root (use sudo)."
    fi
}

is_unraid() {
    [[ -f /etc/unraid-version || -f /boot/config/go ]]
}

use_systemd_timer() {
    is_unraid && return 1
    command -v systemctl >/dev/null 2>&1 || return 1
    [[ "$(ps -p 1 -o comm= 2>/dev/null | tr -d '[:space:]')" == "systemd" ]]
}

reload_cron() {
    if is_unraid && command -v update_cron >/dev/null 2>&1; then
        update_cron
        log "Ran update_cron"
        return
    fi

    if command -v systemctl >/dev/null 2>&1; then
        systemctl reload crond 2>/dev/null || systemctl reload cron 2>/dev/null || true
    elif command -v service >/dev/null 2>&1; then
        service crond reload 2>/dev/null || service cron reload 2>/dev/null || true
    fi
}

parse_args() {
    COMMAND="install"

    while [[ $# -gt 0 ]]; do
        case "$1" in
            install)
                COMMAND="install"
                shift
                ;;
            uninstall)
                COMMAND="uninstall"
                shift
                ;;
            --token)
                [[ $# -ge 2 ]] || die "--token requires a value"
                INGEST_TOKEN="$2"
                shift 2
                ;;
            --no-auto-update)
                AUTO_UPDATE=false
                AUTO_UPDATE_EXPLICIT=true
                shift
                ;;
            --auto-update)
                AUTO_UPDATE=true
                AUTO_UPDATE_EXPLICIT=true
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                die "Unknown option: $1"
                ;;
        esac
    done
}

prompt_token() {
    if [[ -z "${INGEST_TOKEN:-}" && -r "$INSTALL_PATH" ]]; then
        INGEST_TOKEN=$(sed -n 's/^export INGEST_TOKEN="\(.*\)"$/\1/p' "$INSTALL_PATH" | head -n1)
        if [[ -n "${INGEST_TOKEN:-}" ]]; then
            log "Using existing token from ${INSTALL_PATH}"
        fi
    fi

    if [[ -n "${INGEST_TOKEN:-}" ]]; then
        return
    fi

    if [[ -t 0 ]]; then
        if ! read -r -s -p "Enter INGEST_TOKEN: " INGEST_TOKEN; then
            die "Could not read INGEST_TOKEN from terminal."
        fi
        echo
        [[ -n "${INGEST_TOKEN:-}" ]] || die "INGEST_TOKEN is required."
    else
        die "INGEST_TOKEN is required. Pass --token or set the INGEST_TOKEN env var."
    fi
}

detect_pkg_manager() {
    if command -v apt-get >/dev/null 2>&1; then
        echo "apt"
    elif command -v dnf >/dev/null 2>&1; then
        echo "dnf"
    elif command -v yum >/dev/null 2>&1; then
        echo "yum"
    elif command -v apk >/dev/null 2>&1; then
        echo "apk"
    elif command -v pacman >/dev/null 2>&1; then
        echo "pacman"
    elif command -v zypper >/dev/null 2>&1; then
        echo "zypper"
    else
        echo "unknown"
    fi
}

pkg_for_dep() {
    local dep="$1"
    local pm="$2"

    case "$pm:$dep" in
        apt:ip) echo "iproute2" ;;
        dnf:ip|yum:ip) echo "iproute" ;;
        apk:ip) echo "iproute2" ;;
        pacman:ip) echo "iproute2" ;;
        zypper:ip) echo "iproute2" ;;
        *) echo "$dep" ;;
    esac
}

install_dependencies() {
    local pm missing dep pkg packages=()

    pm=$(detect_pkg_manager)
    missing=()

    for dep in "${DEPENDENCIES[@]}"; do
        if ! command -v "$dep" >/dev/null 2>&1; then
            missing+=("$dep")
        fi
    done

    if [[ ${#missing[@]} -eq 0 ]]; then
        log "All dependencies already installed."
        return
    fi

    for dep in "${missing[@]}"; do
        pkg=$(pkg_for_dep "$dep" "$pm")
        packages+=("$pkg")
    done

    log "Installing dependencies: ${packages[*]}"

    case "$pm" in
        apt)
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -qq
            apt-get install -y -qq "${packages[@]}"
            ;;
        dnf)
            dnf install -y "${packages[@]}"
            ;;
        yum)
            yum install -y "${packages[@]}"
            ;;
        apk)
            apk add --no-cache "${packages[@]}"
            ;;
        pacman)
            pacman -Sy --noconfirm "${packages[@]}"
            ;;
        zypper)
            zypper --non-interactive install "${packages[@]}"
            ;;
        unknown)
            log "WARNING: Unknown package manager (${pm}). Install manually if needed: ${packages[*]}"
            for dep in "${missing[@]}"; do
                command -v "$dep" >/dev/null 2>&1 || die "Missing dependency: $dep"
            done
            return
            ;;
    esac

    for dep in "${DEPENDENCIES[@]}"; do
        command -v "$dep" >/dev/null 2>&1 || die "Failed to install dependency: $dep"
    done

    log "Dependencies installed."
}

download_agent() {
    local dest="$1"

    log "Downloading agent from ${REPO_RAW_URL}"
    curl -fsSL \
        -H "Cache-Control: no-cache" \
        "${REPO_RAW_URL}?t=$(date +%s)" \
        -o "$dest"

    [[ -s "$dest" ]] || die "Downloaded agent script is empty."
    head -n1 "$dest" | grep -q '^#!/' || die "Downloaded file does not look like a shell script."
}

configure_token() {
    local src="$1"
    local dst="$2"
    local tmp escaped_token

    escaped_token=$(printf '%s' "$INGEST_TOKEN" | sed 's/[\\&|]/\\&/g')
    tmp=$(mktemp)
    sed "s|^export INGEST_TOKEN=\".*\"|export INGEST_TOKEN=\"${escaped_token}\"|" "$src" > "$tmp"
    chmod 755 "$tmp"
    mv -f "$tmp" "$dst"
}

install_agent() {
    local src="$1"
    local had_existing=false

    if [[ -f "$INSTALL_PATH" ]]; then
        had_existing=true
    fi

    log "Installing agent to ${INSTALL_PATH}"
    configure_token "$src" "$INSTALL_PATH"

    if [[ "$had_existing" == true ]]; then
        log "Agent updated."
    fi
}

write_auto_update_config() {
    log "Auto-update: $([[ "$AUTO_UPDATE" == true ]] && echo enabled || echo disabled)"

    cat > "$AUTO_UPDATE_FILE" <<EOF
# Monitor Agent settings (managed by install.sh)
AUTO_UPDATE=$([[ "$AUTO_UPDATE" == true ]] && echo 1 || echo 0)
AUTO_UPDATE_INTERVAL=${AUTO_UPDATE_INTERVAL_SECONDS}
EOF

    mkdir -p "$AUTO_UPDATE_STATE_DIR"
    chmod 755 "$AUTO_UPDATE_STATE_DIR"
    chmod 644 "$AUTO_UPDATE_FILE"
}

install_run_script() {
    log "Installing scheduler runner to ${RUN_PATH}"

    cat > "$RUN_PATH" <<'EOF'
#!/bin/bash
set -euo pipefail

INSTALL_PATH="/usr/local/sbin/monitor-agent"
AUTO_UPDATE_FILE="/etc/default/monitor-agent"
AUTO_UPDATE_STATE_DIR="/var/lib/monitor-agent"
LAST_UPDATE_FILE="/var/lib/monitor-agent/last-update"
REPO_RAW_URL="https://raw.githubusercontent.com/RealFascinated/Monitor-Agent/master/agent.sh"
DEFAULT_AUTO_UPDATE_INTERVAL=3600

read_config_value() {
    local key="$1"
    local default="$2"
    local value

    [[ -r "$AUTO_UPDATE_FILE" ]] || {
        echo "$default"
        return
    }

    value=$(sed -n "s/^${key}=\\(.*\\)\$/\\1/p" "$AUTO_UPDATE_FILE" | tail -n1)
    if [[ -n "$value" ]]; then
        echo "$value"
    else
        echo "$default"
    fi
}

auto_update_enabled() {
    local value

    value=$(read_config_value "AUTO_UPDATE" "1")
    [[ "$value" == "1" ]]
}

should_check_for_update() {
    local interval last now

    interval=$(read_config_value "AUTO_UPDATE_INTERVAL" "$DEFAULT_AUTO_UPDATE_INTERVAL")
    [[ "$interval" =~ ^[0-9]+$ ]] || interval=$DEFAULT_AUTO_UPDATE_INTERVAL
    (( interval > 0 )) || return 1

    [[ -r "$LAST_UPDATE_FILE" ]] || return 0

    last=$(cat "$LAST_UPDATE_FILE" 2>/dev/null || echo 0)
    [[ "$last" =~ ^[0-9]+$ ]] || return 0

    now=$(date +%s)
    (( now - last >= interval ))
}

read_installed_token() {
    sed -n 's/^export INGEST_TOKEN="\(.*\)"$/\1/p' "$INSTALL_PATH" | head -n1
}

update_agent() {
    local token dest tmp escaped_token

    should_check_for_update || return 0

    [[ -r "$INSTALL_PATH" ]] || return 0
    token=$(read_installed_token)
    [[ -n "$token" ]] || return 0

    mkdir -p "$AUTO_UPDATE_STATE_DIR"

    dest=$(mktemp)
    if ! curl -fsSL \
        -H "Cache-Control: no-cache" \
        "${REPO_RAW_URL}?t=$(date +%s)" \
        -o "$dest"; then
        rm -f "$dest"
        return 0
    fi

    if [[ ! -s "$dest" ]] || ! head -n1 "$dest" | grep -q '^#!/'; then
        rm -f "$dest"
        return 0
    fi

    escaped_token=$(printf '%s' "$token" | sed 's/[\\&|]/\\&/g')
    tmp=$(mktemp)
    sed "s|^export INGEST_TOKEN=\".*\"|export INGEST_TOKEN=\"${escaped_token}\"|" "$dest" > "$tmp"
    chmod 755 "$tmp"
    mv -f "$tmp" "$INSTALL_PATH"
    rm -f "$dest"
    date +%s > "$LAST_UPDATE_FILE"
}

if auto_update_enabled; then
    update_agent
fi

exec "$INSTALL_PATH"
EOF

    chmod 755 "$RUN_PATH"
}

scheduler_runner() {
    if [[ -x "$RUN_PATH" ]]; then
        echo "$RUN_PATH"
    else
        echo "$INSTALL_PATH"
    fi
}

remove_cron_jobs() {
    remove_file "$CRON_FILE" "Cron job"
    remove_file "$UNRAID_CRON_FILE" "Unraid cron job"
    reload_cron
}

remove_systemd_timer() {
    if command -v systemctl >/dev/null 2>&1; then
        systemctl stop monitor-agent.timer 2>/dev/null || true
        systemctl disable monitor-agent.timer 2>/dev/null || true
    fi

    remove_file "$SYSTEMD_TIMER" "Systemd timer"
    remove_file "$SYSTEMD_SERVICE" "Systemd service"

    if command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload 2>/dev/null || true
    fi
}

install_systemd_timer() {
    local runner

    runner=$(scheduler_runner)
    log "Installing systemd timer (every ${RUN_INTERVAL_SECONDS}s)"

    cat > "$SYSTEMD_SERVICE" <<EOF
[Unit]
Description=Monitor Agent run

[Service]
Type=oneshot
ExecStart=${runner}
StandardOutput=append:${LOG_FILE}
StandardError=append:${LOG_FILE}
EOF

    cat > "$SYSTEMD_TIMER" <<EOF
[Unit]
Description=Run Monitor Agent every ${RUN_INTERVAL_SECONDS} seconds

[Timer]
OnBootSec=${RUN_INTERVAL_SECONDS}
OnUnitActiveSec=${RUN_INTERVAL_SECONDS}s
AccuracySec=1s
Unit=monitor-agent.service

[Install]
WantedBy=timers.target
EOF

    chmod 644 "$SYSTEMD_SERVICE" "$SYSTEMD_TIMER"
    systemctl daemon-reload
    systemctl enable --now monitor-agent.timer
}

install_cron() {
    local runner offset line

    (( 60 % RUN_INTERVAL_SECONDS == 0 )) || \
        die "RUN_INTERVAL_SECONDS (${RUN_INTERVAL_SECONDS}) must evenly divide 60 for cron scheduling."

    runner=$(scheduler_runner)
    log "Installing cron job (every ${RUN_INTERVAL_SECONDS}s)"

    if is_unraid; then
        : > "$UNRAID_CRON_FILE"
        for (( offset = 0; offset < 60; offset += RUN_INTERVAL_SECONDS )); do
            if (( offset == 0 )); then
                line="* * * * * ${runner} >> ${LOG_FILE} 2>&1"
            else
                line="* * * * * sleep ${offset}; ${runner} >> ${LOG_FILE} 2>&1"
            fi
            printf '%s\n' "$line" >> "$UNRAID_CRON_FILE"
        done
        chmod 644 "$UNRAID_CRON_FILE"
        log "Installed persistent Unraid cron: ${UNRAID_CRON_FILE}"
        reload_cron
        return
    fi

    {
        echo "SHELL=/bin/bash"
        echo "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
        echo
        for (( offset = 0; offset < 60; offset += RUN_INTERVAL_SECONDS )); do
            if (( offset == 0 )); then
                echo "* * * * * root ${runner} >> ${LOG_FILE} 2>&1"
            else
                echo "* * * * * root sleep ${offset}; ${runner} >> ${LOG_FILE} 2>&1"
            fi
        done
        echo
    } > "$CRON_FILE"

    chmod 644 "$CRON_FILE"
    reload_cron
}

install_scheduler() {
    if use_systemd_timer; then
        remove_cron_jobs
        install_systemd_timer
    else
        remove_systemd_timer
        install_cron
    fi
}

remove_file() {
    local path="$1"
    local label="$2"

    if [[ -e "$path" ]]; then
        log "Removing ${label}: ${path}"
        rm -f "$path"
    else
        log "${label} not found: ${path}"
    fi
}

uninstall() {
    remove_file "$INSTALL_PATH" "Agent"
    remove_file "$RUN_PATH" "Scheduler runner"
    remove_cron_jobs
    remove_systemd_timer
    remove_file "$AUTO_UPDATE_FILE" "Auto-update config"
    remove_file "${AUTO_UPDATE_STATE_DIR}/last-update" "Auto-update timestamp"
    remove_file "$LOG_FILE" "Log file"

    if [[ -d "$AUTO_UPDATE_STATE_DIR" ]]; then
        rmdir "$AUTO_UPDATE_STATE_DIR" 2>/dev/null || true
    fi

    log "Uninstall complete."
}

load_auto_update_preference() {
    local value

    if [[ "$AUTO_UPDATE_EXPLICIT" == true ]]; then
        return 0
    fi

    if [[ ! -r "$AUTO_UPDATE_FILE" ]]; then
        return 0
    fi

    value=$(sed -n 's/^AUTO_UPDATE=\(.*\)$/\1/p' "$AUTO_UPDATE_FILE" | tail -n1)
    case "$value" in
        0) AUTO_UPDATE=false ;;
        1) AUTO_UPDATE=true ;;
    esac
}

main() {
    local tmp

    load_auto_update_preference
    prompt_token
    install_dependencies

    tmp=$(mktemp)
    download_agent "$tmp"
    install_agent "$tmp"
    rm -f "$tmp"
    write_auto_update_config
    install_run_script
    install_scheduler

    log "Installation complete."
    log "Agent: ${INSTALL_PATH}"
    log "Runner: ${RUN_PATH}"
    log "Interval: every ${RUN_INTERVAL_SECONDS}s"
    log "Auto-update: $([[ "$AUTO_UPDATE" == true ]] && echo enabled || echo disabled) (${AUTO_UPDATE_FILE})"
    if use_systemd_timer; then
        log "Scheduler: systemd timer (${SYSTEMD_TIMER})"
    elif is_unraid; then
        log "Scheduler: cron (${UNRAID_CRON_FILE})"
    else
        log "Scheduler: cron (${CRON_FILE})"
    fi
    log "Logs:  ${LOG_FILE}"
}

parse_args "$@"
require_root

log "Monitor Agent installer starting (command: ${COMMAND})"

case "$COMMAND" in
    install)
        main
        ;;
    uninstall)
        uninstall
        ;;
esac
