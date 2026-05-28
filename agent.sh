#!/bin/bash

export INGEST_TOKEN=""
export API_URL="https://monitor.fascinated.cc/api/v1/servers/ingest"

get_ip() {
    local ip
    ip=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i <= NF; i++) if ($i == "src") { print $(i + 1); exit }}')
    if [[ -z "$ip" ]]; then
        ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    fi
    echo "${ip:-127.0.0.1}"
}

is_container() {
    if [[ -f /proc/1/environ ]] && tr '\0' ' ' < /proc/1/environ | grep -q 'container='; then
        return 0
    fi

    if command -v systemd-detect-virt >/dev/null 2>&1; then
        [[ "$(systemd-detect-virt -c 2>/dev/null)" != "none" ]]
        return
    fi

    return 1
}

get_cgroup_dir() {
    local dir="/sys/fs/cgroup"

    if [[ -r "$dir/cpu.stat" || -r "$dir/memory.current" ]]; then
        echo "$dir"
    fi
}

get_online_cpu_count() {
    if command -v nproc >/dev/null 2>&1; then
        nproc
        return
    fi

    getconf _NPROCESSORS_ONLN
}

get_core_count() {
    if is_container; then
        local cores
        cores=$(awk '/^core id/ { ids[$4] = 1 } END { print length(ids) }' /proc/cpuinfo)
        if [[ "$cores" =~ ^[0-9]+$ && "$cores" -gt 0 ]]; then
            echo "$cores"
            return
        fi
        get_online_cpu_count
        return
    fi

    if command -v lscpu >/dev/null 2>&1; then
        local cores_per_socket sockets
        cores_per_socket=$(lscpu 2>/dev/null | awk '/^Core\(s\) per socket:/ {print $4; exit}')
        sockets=$(lscpu 2>/dev/null | awk '/^Socket\(s\):/ {print $2; exit}')
        if [[ -n "$cores_per_socket" && -n "$sockets" ]]; then
            echo $((cores_per_socket * sockets))
            return
        fi
    fi

    get_online_cpu_count
}

get_thread_count() {
    get_online_cpu_count
}

get_os_name() {
    if [[ -r /etc/os-release ]]; then
        # shellcheck disable=SC1091
        source /etc/os-release
        echo "${NAME:-$(uname -s)}"
        return
    fi

    uname -s
}

get_os_version() {
    if [[ -r /etc/os-release ]]; then
        # shellcheck disable=SC1091
        source /etc/os-release
        echo "${VERSION:-${VERSION_ID:-unknown}}"
        return
    fi

    uname -r
}

get_uptime_seconds() {
    awk '{print int($1)}' /proc/uptime
}

read_cpu_stat() {
    awk '/^cpu / {
        idle = $5 + $6
        total = $2 + $3 + $4 + idle + $7 + $8 + $9 + ($10 + $11)
        print idle, total
        exit
    }' /proc/stat
}

read_cgroup_cpu_usage_usec() {
    awk '/^usage_usec / { print $2; exit }' "$1/cpu.stat"
}

get_cpu_capacity_usec_per_second() {
    local cgroup_dir="$1" online_cpus quota period quota_capacity

    online_cpus=$(get_online_cpu_count)
    read -r quota period < "$cgroup_dir/cpu.max"

    if [[ "$quota" != "max" && "$period" =~ ^[0-9]+$ && "$period" -gt 0 ]]; then
        quota_capacity=$((quota * 1000000 / period))
        if (( quota_capacity > 0 && quota_capacity < online_cpus * 1000000 )); then
            echo "$quota_capacity"
            return
        fi
    fi

    echo $((online_cpus * 1000000))
}

get_cpu_usage_from_proc_stat() {
    local idle1 total1 idle2 total2

    read -r idle1 total1 < <(read_cpu_stat)
    sleep 1
    read -r idle2 total2 < <(read_cpu_stat)
    compute_cpu_usage_from_proc_stat_samples "$idle1" "$total1" "$idle2" "$total2"
}

compute_cpu_usage_from_proc_stat_samples() {
    local idle1="$1" total1="$2" idle2="$3" total2="$4" dt di

    dt=$((total2 - total1))
    di=$((idle2 - idle1))

    if (( dt <= 0 )); then
        echo "0"
        return
    fi

    awk -v dt="$dt" -v di="$di" 'BEGIN { printf "%.2f", (dt - di) / dt * 100 }'
}

get_cpu_usage() {
    local cgroup_dir usage1 usage2

    cgroup_dir=$(get_cgroup_dir)
    if [[ -n "$cgroup_dir" && -r "$cgroup_dir/cpu.stat" ]]; then
        usage1=$(read_cgroup_cpu_usage_usec "$cgroup_dir")
        sleep 1
        usage2=$(read_cgroup_cpu_usage_usec "$cgroup_dir")
        compute_cpu_usage_from_cgroup_samples "$usage1" "$usage2" && return
    fi

    get_cpu_usage_from_proc_stat
}

read_network_stats() {
    awk 'NR > 2 {
        iface = $1
        sub(/:$/, "", iface)
        if (iface == "" || iface == "lo") next
        if (iface !~ /^(eth|ens|enp|eno|enx|em|wlan|wlp|bond)[0-9]+$/) next
        print iface, $2, $3, $4, $10, $11, $12
    }' /proc/net/dev
}

compute_cpu_usage_from_cgroup_samples() {
    local usage1="$1" usage2="$2" cgroup_dir delta capacity_usec

    cgroup_dir=$(get_cgroup_dir)
    [[ -z "$cgroup_dir" ]] && return 1

    delta=$((usage2 - usage1))
    capacity_usec=$(get_cpu_capacity_usec_per_second "$cgroup_dir")

    if (( capacity_usec <= 0 )); then
        echo "0"
        return 0
    fi

    awk -v delta="$delta" -v capacity="$capacity_usec" \
        'BEGIN { printf "%.2f", delta / capacity * 100 }'
}

read_disk_space_stats() {
    df -B1 -P / | awk '
        NR > 1 && $6 == "/" {
            total = $3 + $4
            if (total <= 0) next
            if ($1 ~ /^[0-9]+(\.[0-9]+){3}:/ ) next
            if ($1 ~ /^\/dev\/zd/) next
            printf "%s\t%s\t%d\t%d\n", $1, $6, $3, total
        }
    '
}

read_diskstats() {
    awk '$3 !~ /^(loop|ram|fd|zram)/ {
        print $3, $4, $6, $7, $8, $10, $11, $13
    }' /proc/diskstats
}

read_cgroup_io_stats() {
    local cgroup_dir
    cgroup_dir=$(get_cgroup_dir)

    [[ -n "$cgroup_dir" && -r "$cgroup_dir/io.stat" ]] || return 0

    awk '{
        majmin = $1
        rbytes = 0
        wbytes = 0
        for (i = 2; i <= NF; i++) {
            if ($i ~ /^rbytes=/) {
                split($i, a, "=")
                rbytes = a[2]
            }
            if ($i ~ /^wbytes=/) {
                split($i, a, "=")
                wbytes = a[2]
            }
        }
        print majmin, rbytes, wbytes
    }' "$cgroup_dir/io.stat"
}

resolve_block_device_name() {
    local majmin="$1" name

    [[ -r "/sys/dev/block/$majmin" ]] || return 1
    name=$(basename "$(readlink -f "/sys/dev/block/$majmin")" 2>/dev/null) || return 1
    [[ -n "$name" ]] && echo "$name"
}

lookup_diskstats_delta() {
    local device="$1" before="$2" after="$3"
    awk -v device="$device" -v before="$before" -v after="$after" '
        function find_stats(data, name,    lines, i, n, parts) {
            n = split(data, lines, "\n")
            for (i = 1; i <= n; i++) {
                if (lines[i] == "") continue
                split(lines[i], parts, " ")
                if (parts[1] == name) {
                    reads = parts[2]
                    sectors_read = parts[3]
                    read_ms = parts[4]
                    writes = parts[5]
                    write_ms = parts[7]
                    io_ms = parts[8]
                    return 1
                }
            }
            return 0
        }
        BEGIN {
            sector_bytes = 512
            if (!find_stats(before, device)) exit 1
            prev_reads = reads
            prev_sectors_read = sectors_read
            prev_read_ms = read_ms
            prev_writes = writes
            prev_write_ms = write_ms
            prev_io_ms = io_ms

            if (!find_stats(after, device)) exit 1

            read_bps = (sectors_read - prev_sectors_read) * sector_bytes
            io_ms_delta = io_ms - prev_io_ms
            read_ms_delta = read_ms - prev_read_ms
            write_ms_delta = write_ms - prev_write_ms
            ios = (reads - prev_reads) + (writes - prev_writes)

            if (read_bps < 0) read_bps = 0
            if (io_ms_delta < 0) io_ms_delta = 0
            if (read_ms_delta < 0) read_ms_delta = 0
            if (write_ms_delta < 0) write_ms_delta = 0
            if (ios < 0) ios = 0

            io_usage = io_ms_delta / 10
            io_wait = (ios > 0) ? (read_ms_delta + write_ms_delta) / ios : 0

            printf "%d %.2f %.2f", read_bps, io_usage, io_wait
        }
    '
}

lookup_cgroup_read_bps() {
    local before="$1" after="$2"
    awk -v before="$before" -v after="$after" '
        BEGIN {
            total = 0
            n = split(after, after_lines, "\n")
            for (i = 1; i <= n; i++) {
                if (after_lines[i] == "") continue
                split(after_lines[i], curr, " ")
                if (curr[1] == "") continue
                key = curr[1]
                m = split(before, before_lines, "\n")
                for (j = 1; j <= m; j++) {
                    if (before_lines[j] == "") continue
                    split(before_lines[j], prev, " ")
                    if (prev[1] == key) {
                        delta = curr[2] - prev[2]
                        if (delta > 0) total += delta
                        break
                    }
                }
            }
            print total + 0
        }
    '
}

compute_disk_metrics_json() {
    local df_stats="$1" diskstats1="$2" diskstats2="$3" cgroup_io1="$4" cgroup_io2="$5"
    local fs mount used total device io_line read_bps io_usage io_wait majmin cgroup_device

    if [[ -n "$cgroup_io1" ]]; then
        majmin=$(awk 'NR == 1 { print $1; exit }' <<<"$cgroup_io1")
        cgroup_device=$(resolve_block_device_name "$majmin" 2>/dev/null || true)
    fi

    while IFS=$'\t' read -r fs mount used total; do
        [[ -z "$fs" ]] && continue

        device=""
        read_bps=0
        io_usage="0.00"
        io_wait="0.00"

        if [[ "$fs" == /dev/* ]]; then
            device=$(basename "$fs")
            if io_line=$(lookup_diskstats_delta "$device" "$diskstats1" "$diskstats2"); then
                read -r read_bps io_usage io_wait <<<"$io_line"
            fi
        elif [[ -n "$cgroup_io1" && -n "$cgroup_io2" ]]; then
            read_bps=$(lookup_cgroup_read_bps "$cgroup_io1" "$cgroup_io2")
        elif [[ -n "$cgroup_device" ]] && io_line=$(lookup_diskstats_delta "$cgroup_device" "$diskstats1" "$diskstats2"); then
            read -r read_bps io_usage io_wait <<<"$io_line"
        fi

        printf '%s\t%d\t%d\t%d\t%s\t%s\n' \
            "$fs" "$used" "$total" "$read_bps" "$io_usage" "$io_wait"
    done <<<"$df_stats" | jq -R -s '
        split("\n")
        | map(select(length > 0))
        | map(split("\t"))
        | map({
            diskName: .[0],
            usedBytes: (.[1] | tonumber),
            totalBytes: (.[2] | tonumber),
            ioReadBytesPerSecond: (.[3] | tonumber),
            ioUsagePercent: (.[4] | tonumber),
            ioWaitMilliseconds: (.[5] | tonumber)
        })
    '
}

compute_interface_metrics_json() {
    local snap1="$1" snap2="$2"

    awk '
        FNR == NR {
            prev_rx_bytes[$1] = $2
            prev_rx_packets[$1] = $3
            prev_rx_errors[$1] = $4
            prev_tx_bytes[$1] = $5
            prev_tx_packets[$1] = $6
            prev_tx_errors[$1] = $7
            next
        }
        $1 in prev_rx_bytes {
            iface = $1
            rx_bytes = $2 - prev_rx_bytes[iface]
            rx_packets = $3 - prev_rx_packets[iface]
            rx_errors = $4 - prev_rx_errors[iface]
            tx_bytes = $5 - prev_tx_bytes[iface]
            tx_packets = $6 - prev_tx_packets[iface]
            tx_errors = $7 - prev_tx_errors[iface]

            if (rx_bytes < 0) rx_bytes = 0
            if (rx_packets < 0) rx_packets = 0
            if (rx_errors < 0) rx_errors = 0
            if (tx_bytes < 0) tx_bytes = 0
            if (tx_packets < 0) tx_packets = 0
            if (tx_errors < 0) tx_errors = 0

            printf("%s\t%d\t%d\t%d\t%d\t%d\t%d\n",
                iface, rx_bytes, tx_bytes, rx_packets, tx_packets, rx_errors, tx_errors)
        }
    ' <(echo "$snap1") <(echo "$snap2") | jq -R -s '
        split("\n")
        | map(select(length > 0))
        | map(split("\t"))
        | map({
            interfaceName: .[0],
            rxBytesPerSecond: (.[1] | tonumber),
            txBytesPerSecond: (.[2] | tonumber),
            rxPacketsPerSecond: (.[3] | tonumber),
            txPacketsPerSecond: (.[4] | tonumber),
            rxErrorsPerSecond: (.[5] | tonumber),
            txErrorsPerSecond: (.[6] | tonumber)
        })
    '
}

sample_rate_metrics() {
    local cgroup_dir cpu_usage1 cpu_usage2 net_snap1 net_snap2
    local diskstats1 diskstats2 cgroup_io1 cgroup_io2 df_stats
    local idle1 total1 idle2 total2 cpu_usage interface_metrics disk_metrics

    cgroup_dir=$(get_cgroup_dir)
    if [[ -n "$cgroup_dir" && -r "$cgroup_dir/cpu.stat" ]]; then
        cpu_usage1=$(read_cgroup_cpu_usage_usec "$cgroup_dir")
    fi

    df_stats=$(read_disk_space_stats)
    diskstats1=$(read_diskstats)
    cgroup_io1=$(read_cgroup_io_stats)
    read -r idle1 total1 < <(read_cpu_stat)
    net_snap1=$(read_network_stats)
    sleep 1

    if [[ -n "$cpu_usage1" ]]; then
        cpu_usage2=$(read_cgroup_cpu_usage_usec "$cgroup_dir")
        cpu_usage=$(compute_cpu_usage_from_cgroup_samples "$cpu_usage1" "$cpu_usage2") || cpu_usage=""
    fi

    diskstats2=$(read_diskstats)
    cgroup_io2=$(read_cgroup_io_stats)
    read -r idle2 total2 < <(read_cpu_stat)
    net_snap2=$(read_network_stats)

    if [[ -z "$cpu_usage" ]]; then
        cpu_usage=$(compute_cpu_usage_from_proc_stat_samples "$idle1" "$total1" "$idle2" "$total2")
    fi

    interface_metrics=$(compute_interface_metrics_json "$net_snap1" "$net_snap2")
    disk_metrics=$(compute_disk_metrics_json "$df_stats" "$diskstats1" "$diskstats2" "$cgroup_io1" "$cgroup_io2")

    RATE_CPU_USAGE="$cpu_usage"
    RATE_INTERFACE_METRICS="$interface_metrics"
    RATE_DISK_METRICS="$disk_metrics"
}

get_memory_total() {
    local cgroup_dir memory_max

    cgroup_dir=$(get_cgroup_dir)
    if [[ -n "$cgroup_dir" && -r "$cgroup_dir/memory.max" ]]; then
        read -r memory_max < "$cgroup_dir/memory.max"
        if [[ "$memory_max" != "max" && "$memory_max" =~ ^[0-9]+$ ]]; then
            printf "%.2f" "$memory_max"
            return
        fi
    fi

    awk '/^MemTotal:/ { printf "%.2f", $2 * 1024; exit }' /proc/meminfo
}

get_memory_usage() {
    local cgroup_dir memory_max memory_current

    cgroup_dir=$(get_cgroup_dir)
    if [[ -n "$cgroup_dir" && -r "$cgroup_dir/memory.max" && -r "$cgroup_dir/memory.current" ]]; then
        read -r memory_max < "$cgroup_dir/memory.max"
        read -r memory_current < "$cgroup_dir/memory.current"
        if [[ "$memory_max" != "max" && "$memory_max" =~ ^[0-9]+$ && "$memory_current" =~ ^[0-9]+$ ]]; then
            printf "%.2f" "$memory_current"
            return
        fi
    fi

    awk '
        /^MemTotal:/ { total = $2 }
        /^MemAvailable:/ { available = $2 }
        END { printf "%.2f", (total - available) * 1024 }
    ' /proc/meminfo
}

build_payload() {
    sample_rate_metrics

    jq -n \
        --arg ip "$(get_ip)" \
        --argjson coreCount "$(get_core_count)" \
        --argjson threadCount "$(get_thread_count)" \
        --arg osName "$(get_os_name)" \
        --arg osVersion "$(get_os_version)" \
        --argjson uptimeSeconds "$(get_uptime_seconds)" \
        --argjson cpuUsage "$RATE_CPU_USAGE" \
        --argjson memoryUsage "$(get_memory_usage)" \
        --argjson memoryTotal "$(get_memory_total)" \
        --argjson interfaceMetrics "$RATE_INTERFACE_METRICS" \
        --argjson diskMetrics "$RATE_DISK_METRICS" \
        '{
            serverDetails: {
                ip: $ip,
                coreCount: $coreCount,
                threadCount: $threadCount,
                osName: $osName,
                osVersion: $osVersion,
                uptimeSeconds: $uptimeSeconds
            },
            serverMetrics: {
                cpuUsage: $cpuUsage,
                memoryUsage: $memoryUsage,
                memoryTotal: $memoryTotal
            },
            interfaceMetrics: $interfaceMetrics,
            diskMetrics: $diskMetrics
        }'
}

push_metrics() {
    local payload http_code body

    payload=$(build_payload)

    body=$(curl -sS -w "\n%{http_code}" \
        -X POST "$API_URL" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${INGEST_TOKEN}" \
        -d "$payload")

    http_code=$(tail -n1 <<<"$body")
    body=$(sed '$d' <<<"$body")

    if [[ "$http_code" -ge 200 && "$http_code" -lt 300 ]]; then
        echo "Metrics ingested successfully (HTTP ${http_code})"
        [[ -n "$body" ]] && echo "$body"
        return 0
    fi

    echo "Failed to ingest metrics (HTTP ${http_code})" >&2
    [[ -n "$body" ]] && echo "$body" >&2
    return 1
}

push_metrics

