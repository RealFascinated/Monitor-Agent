# Monitor Agent

Lightweight host metrics agent for [Monitor](https://monitor.fascinated.cc).

## Install (Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/RealFascinated/Monitor-Agent/main/install.sh | sudo bash -s -- install YOUR_INGEST_TOKEN
```

## Windows

Run this from an **Administrator PowerShell** to download and install the agent as a background service:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
Invoke-WebRequest -Uri https://raw.githubusercontent.com/RealFascinated/Monitor-Agent/main/install.ps1 -OutFile install.ps1
.\install.ps1 install YOUR_INGEST_TOKEN
```

This writes `config.yml` to `%ProgramData%\MonitorAgent`, registers a **monitor-agent** service via [NSSM](https://nssm.cc/), and logs to `agent.log` in the same folder.

> **Note:** The agent must run as Administrator so hardware sensors (including GPU power) can be read.

To uninstall: `.\install.ps1 uninstall`

**Building from source:**

```powershell
.\scripts\build-lhm.ps1
go build -tags lhmbundle -o monitor-agent.exe ./cmd/main.go
.\install.ps1 install YOUR_INGEST_TOKEN -BinaryPath .\monitor-agent.exe
```

The Windows build bundles [LibreHardwareMonitorLib](https://github.com/LibreHardwareMonitor/LibreHardwareMonitor) for CPU, memory, temperatures, and GPU sensors (see [licenses/LibreHardwareMonitor/NOTICE.md](licenses/LibreHardwareMonitor/NOTICE.md)).

## Configuration

A config file is optional. Provide settings in `config.yml` (see `config-example.yml`) or entirely via environment variables. Environment variables override values from the file.

Each YAML key maps to an environment variable: `MONITOR_` + the key in uppercase (e.g. `ingest_token` → `MONITOR_INGEST_TOKEN`). New fields added to `config-example.yml` follow the same rule automatically.

| Config key | Default | Description |
| --- | --- | --- |
| `config_file` (`MONITOR_CONFIG_FILE`) | `config.yml` if present; `-` skips the file | Config path |
| `ingest_token` | *(required)* | Monitor ingest token |
| `api_endpoint` | `https://monitor.fascinated.cc/api/v1/servers/ingest` | Ingest URL |
| `push_schedule` | `*/15 * * * * *` | Cron with seconds (6 fields) |
| `enable_docker` | `true` | Docker container stats (Linux) |
| `enable_gpu` | `true` | GPU metrics collection |
| `print_mode` | `false` | Print JSON to stdout instead of pushing |

Other runtime variables: `MONITOR_HOST_ROOT` (host root bind mount prefix for disk metrics in containers), `MONITOR_LOG_LEVEL` (`debug`, `info`, `warn`, `error`).

Boolean config env vars accept `true`/`false`, `1`/`0`, `yes`/`no`, or `on`/`off`.

If `config.yml` is missing and `MONITOR_CONFIG_FILE` is not set, the agent starts using environment variables only. If `MONITOR_CONFIG_FILE` points at a path, that file must exist.

### Print mode (debug)

Print one metrics payload as indented JSON to stdout (no ingest token required):

```bash
./monitor-agent print
```

Or set `print_mode: true` / `MONITOR_PRINT_MODE=true` in config. Logs still go to stderr.

## GPU metrics

Each GPU is reported in `gpuMetrics` with a stable `deviceId` (16-character hex hash of the platform identifier), plus `name`, `vendor`, and optional usage, VRAM, temperature, and power fields.

| Platform | Source |
| --- | --- |
| **Windows** | LibreHardwareMonitor (NVIDIA, AMD, Intel) |
| **Linux — NVIDIA** | `nvidia-smi` (included in the Docker image) |
| **Linux — AMD / Intel** | DRM sysfs (`amdgpu`, `i915`, `xe`) |

Set `enable_gpu: false` or `MONITOR_ENABLE_GPU=false` to disable.

## Unraid

Install the Docker template on Unraid:

```bash
wget -O /boot/config/plugins/dockerMan/templates-user/monitor-agent.xml \
  https://raw.githubusercontent.com/RealFascinated/Monitor-Agent/main/unraid/monitor-agent.xml
```

Then open **Docker → Add Container**, choose **monitor-agent**, enter your **Ingest Token**, and apply. Defaults mount the host `/proc`, `/sys`, `/dev`, array root at `/host`, and the Docker socket for full metrics on Unraid (including `/mnt/*` shares and ZFS).

For **NVIDIA** GPUs on Unraid, add GPU support in the container (e.g. set the container to use the NVIDIA driver / pass through GPUs) so `nvidia-smi` can reach the host driver. AMD and Intel GPUs use sysfs and do not need `nvidia-smi`.

## Docker

Images are published to GitHub Container Registry on each `agent/v*` release tag:

`ghcr.io/realfascinated/monitor-agent`

Pull a versioned tag (for example `2.0.1`), `latest` (releases), or `master` (every push to the `master` branch).

The image includes `nvidia-smi` and `gcompat` (glibc compatibility on Alpine). **NVIDIA** hosts should install the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html) and pass GPUs into the container (`gpus: all` below, or `docker run --gpus all`) so driver libraries match the host. **AMD and Intel** GPUs are collected via `/sys` mounts and do not require `gpus: all`.

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
      MONITOR_ENABLE_GPU: "true"
      MONITOR_HOST_ROOT: /host
    volumes:
      - /:/host:ro,rslave
      - /proc:/proc:ro
      - /sys:/sys:ro
      - /dev:/dev:ro
      - /etc/os-release:/etc/os-release:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
    gpus: all
```

Omit `gpus: all` if you have no NVIDIA GPU (or no Container Toolkit); AMD/Intel metrics still work with the sysfs mounts above.

Alternatively, mount a config file instead of using env vars:

```yaml
    environment:
      MONITOR_CONFIG_FILE: /etc/monitor-agent/config.yml
    volumes:
      - ./config.yml:/etc/monitor-agent/config.yml:ro
```

## Releases

Tagged releases use the prefix `agent/vMAJOR.MINOR.PATCH` (for example `agent/v2.0.1`). Pushing a tag builds GitHub release binaries and publishes the multi-arch Docker image to GHCR.
