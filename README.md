# Monitor Agent

Lightweight host metrics agent for [Monitor](https://monitor.fascinated.cc). Collects CPU, memory, load, network, disk space and I/O, optional ZFS pool metrics, and optional Docker container stats, then pushes them to your Monitor ingest endpoint on a cron schedule.

## Install (Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/RealFascinated/Monitor-Agent/main/install.sh \
  | sudo bash -s -- install YOUR_INGEST_TOKEN
```

## Windows

Download `monitor-agent-windows-amd64.exe` from the [latest agent release](https://github.com/RealFascinated/Monitor-Agent/releases). The Windows build embeds [LibreHardwareMonitorLib](https://github.com/LibreHardwareMonitor/LibreHardwareMonitor) for CPU, memory, per-core usage, and temperature sensors (see [licenses/LibreHardwareMonitor/NOTICE.md](licenses/LibreHardwareMonitor/NOTICE.md)).

Install as a background service (Administrator PowerShell):

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1 install YOUR_INGEST_TOKEN
```

The installer downloads the release binary (or use `-BinaryPath .\monitor-agent.exe`), writes `config.yml` under `%ProgramData%\MonitorAgent`, registers a **monitor-agent** service via [NSSM](https://nssm.cc/), and logs to `agent.log` in that folder. Uninstall: `.\install.ps1 uninstall`.

Run the agent **as Administrator** so hardware sensors can be read.

Build from source on Windows:

```powershell
.\scripts\build-lhm.ps1
go build -tags lhmbundle -o monitor-agent.exe ./cmd/main.go
.\install.ps1 install YOUR_INGEST_TOKEN -BinaryPath .\monitor-agent.exe
```

## Configuration

A config file is optional. Provide settings in `config.yml` (see `config-example.yml`) or entirely via environment variables. Environment variables override values from the file.

Each YAML key maps to an environment variable: `MONITOR_` + the key in uppercase (e.g. `ingest_token` → `MONITOR_INGEST_TOKEN`). New fields added to `config-example.yml` follow the same rule automatically.

| Config key | Default |
| --- | --- |
| `config_file` (`MONITOR_CONFIG_FILE`) | `config.yml` if present; set to `-` to skip the file |
| `ingest_token` | *(required)* |
| `api_endpoint` | `https://monitor.fascinated.cc/api/v1/servers/ingest` |
| `push_schedule` | `*/15 * * * * *` |
| `enable_docker` | `true` |

Other runtime variables: `MONITOR_HOST_ROOT` (Docker disk mounts), `MONITOR_LOG_LEVEL` (`info`).

Boolean config env vars accept `true`/`false`, `1`/`0`, `yes`/`no`, or `on`/`off`.

If `config.yml` is missing and `MONITOR_CONFIG_FILE` is not set, the agent starts using environment variables only. If `MONITOR_CONFIG_FILE` points at a path, that file must exist.

## Unraid

Install the Docker template on Unraid:

```bash
wget -O /boot/config/plugins/dockerMan/templates-user/monitor-agent.xml \
  https://raw.githubusercontent.com/RealFascinated/Monitor-Agent/main/unraid/monitor-agent.xml
```

Then open **Docker → Add Container**, choose **monitor-agent**, enter your **Ingest Token**, and apply. Defaults mount the host `/proc`, `/sys`, `/dev`, array root at `/host`, and the Docker socket for full metrics on Unraid (including `/mnt/*` shares and ZFS).

## Docker

Images are published to GitHub Container Registry on each `agent/v*` release tag:

`ghcr.io/realfascinated/monitor-agent`

Pull a versioned tag (for example `2.0.1`), `latest` (releases), or `master` (every push to the `master` branch).

### Example `docker-compose.yml`

```yaml
services:
  monitor-agent:
    image: ghcr.io/realfascinated/monitor-agent:latest
    container_name: monitor-agent
    restart: unless-stopped
    privileged: true
    pid: host
    network_mode: host
    environment:
      MONITOR_CONFIG_FILE: "-"
      MONITOR_INGEST_TOKEN: your-ingest-token
      MONITOR_API_ENDPOINT: https://monitor.fascinated.cc/api/v1/servers/ingest
      MONITOR_PUSH_SCHEDULE: "*/15 * * * * *"
      MONITOR_ENABLE_DOCKER: "true"
      MONITOR_HOST_ROOT: /host
    volumes:
      - /:/host:ro,rslave
      - /proc:/proc:ro
      - /sys:/sys:ro
      - /dev:/dev:ro
      - /etc/os-release:/etc/os-release:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
```

Alternatively, mount a config file instead of using env vars:

```yaml
    environment:
      MONITOR_CONFIG_FILE: /etc/monitor-agent/config.yml
    volumes:
      - ./config.yml:/etc/monitor-agent/config.yml:ro
```

## Releases

Tagged releases use the prefix `agent/vMAJOR.MINOR.PATCH` (for example `agent/v2.0.1`). Pushing a tag builds GitHub release binaries and publishes the multi-arch Docker image to GHCR.
