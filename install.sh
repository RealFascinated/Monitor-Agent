#!/bin/bash
set -euo pipefail

REPO_RAW_URL="https://raw.githubusercontent.com/RealFascinated/Monitor-Agent/master/agent.sh"
INSTALL_PATH="/usr/local/sbin/monitor-agent"
CRON_FILE="/etc/cron.d/monitor-agent"
LOG_FILE="/var/log/monitor-agent.log"
DEPENDENCIES=(curl jq ip)

usage() {
    cat <<EOF
Usage: $0 [COMMAND] [OPTIONS]

Install or remove the Monitor Agent from GitHub.

Commands:
  install         Install the agent (default)
  uninstall       Remove the agent, cron job, and log file

Options:
  --token TOKEN   Ingest API token (or set INGEST_TOKEN env var)

Examples:
  sudo $0 --token "your-token-here"
  sudo INGEST_TOKEN="your-token-here" $0
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
        read -r -s -p "Enter INGEST_TOKEN: " INGEST_TOKEN
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
            die "Could not detect a supported package manager. Install manually: ${packages[*]}"
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

    [[ -f "$INSTALL_PATH" ]] && had_existing=true

    log "Installing agent to ${INSTALL_PATH}"
    configure_token "$src" "$INSTALL_PATH"

    if [[ "$had_existing" == true ]]; then
        log "Agent updated."
    fi
}

install_cron() {
    log "Installing cron job (every minute)"

    cat > "$CRON_FILE" <<EOF
SHELL=/bin/bash
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

* * * * * root ${INSTALL_PATH} >> ${LOG_FILE} 2>&1
EOF

    chmod 644 "$CRON_FILE"
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
    remove_file "$CRON_FILE" "Cron job"
    remove_file "$LOG_FILE" "Log file"

    log "Uninstall complete."
}

main() {
    local tmp

    prompt_token
    install_dependencies

    tmp=$(mktemp)
    download_agent "$tmp"
    install_agent "$tmp"
    rm -f "$tmp"
    install_cron

    log "Installation complete."
    log "Agent: ${INSTALL_PATH}"
    log "Cron:  ${CRON_FILE}"
    log "Logs:  ${LOG_FILE}"
}

parse_args "$@"
require_root

case "$COMMAND" in
    install)
        main
        ;;
    uninstall)
        uninstall
        ;;
esac
