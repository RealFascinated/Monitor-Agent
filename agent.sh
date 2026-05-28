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

    is_container || return 0

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

read_lscpu_value() {
    local label="$1"

    command -v lscpu >/dev/null 2>&1 || return 1

    lscpu 2>/dev/null | awk -v label="$label" -F: '
        function trimmed(value) {
            sub(/^[ \t]+/, "", value)
            sub(/[ \t]+$/, "", value)
            return value
        }
        trimmed($1) == label {
            print trimmed($2)
            exit
        }
    '
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

    local cores_per_socket sockets
    cores_per_socket=$(read_lscpu_value "Core(s) per socket")
    sockets=$(read_lscpu_value "Socket(s)")
    if [[ "$cores_per_socket" =~ ^[0-9]+$ && "$sockets" =~ ^[0-9]+$ ]]; then
        echo $((cores_per_socket * sockets))
        return
    fi

    get_online_cpu_count
}

get_thread_count() {
    get_online_cpu_count
}

get_cpu_model() {
    local model

    model=$(read_lscpu_value "Model name")
    if [[ -n "$model" ]]; then
        echo "$model"
        return
    fi

    model=$(awk -F: '/model name/ { sub(/^ /, "", $2); print $2; exit }' /proc/cpuinfo)
    if [[ -n "$model" ]]; then
        echo "$model"
        return
    fi

    model=$(awk -F: '/^Hardware/ { sub(/^ /, "", $2); print $2; exit }' /proc/cpuinfo)
    if [[ -n "$model" ]]; then
        echo "$model"
        return
    fi

    echo "unknown"
}

get_socket_count() {
    local sockets

    sockets=$(read_lscpu_value "Socket(s)")
    if [[ "$sockets" =~ ^[0-9]+$ ]]; then
        echo "$sockets"
        return
    fi

    sockets=$(awk '/^physical id/ { ids[$4] = 1 } END { print length(ids) }' /proc/cpuinfo)
    if [[ "$sockets" =~ ^[0-9]+$ && "$sockets" -gt 0 ]]; then
        echo "$sockets"
        return
    fi

    echo 1
}

get_cpu_clock_mhz() {
    local mhz="" freq_glob="/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_cur_freq"

    if compgen -G "$freq_glob" >/dev/null 2>&1; then
        mhz=$(awk '{ sum += $1; count++ } END { if (count > 0) printf "%.2f", sum / count / 1000; else print "0" }' $freq_glob 2>/dev/null)
    fi

    if [[ ! "$mhz" =~ ^[0-9.]+$ || "$mhz" == "0" || "$mhz" == "0.0" ]]; then
        mhz=$(awk '/^cpu MHz/ {
            sum += $4
            count++
        }
        END {
            if (count > 0) printf "%.2f", sum / count
            else print "0"
        }' /proc/cpuinfo 2>/dev/null)
    fi

    if [[ ! "$mhz" =~ ^[0-9.]+$ || "$mhz" == "0" || "$mhz" == "0.0" ]]; then
        mhz=$(read_lscpu_value "CPU MHz")
    fi

    if [[ "$mhz" =~ ^[0-9.]+$ ]]; then
        printf "%.2f\n" "$mhz"
    else
        echo "0"
    fi
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

read_cpu_stat_line() {
    awk '/^cpu / {
        print $2, $3, $4, $5, $6, $7, $8, $9
        exit
    }' /proc/stat
}

read_cpu_stat() {
    awk '/^cpu / {
        idle = $5 + $6
        total = $2 + $3 + $4 + idle + $7 + $8 + $9 + ($10 + $11)
        print idle, total
        exit
    }' /proc/stat
}

compute_cpu_metrics_from_proc_stat_samples() {
    local before="$1" after="$2"
    awk -v before="$before" -v after="$after" '
        function delta(curr, prev,    d) {
            d = curr - prev
            return (d > 0) ? d : 0
        }
        BEGIN {
            split(before, b, " ")
            split(after, a, " ")
            du_user = delta(a[1], b[1])
            du_nice = delta(a[2], b[2])
            du_sys = delta(a[3], b[3])
            du_idle = delta(a[4], b[4])
            du_iow = delta(a[5], b[5])
            du_irq = delta(a[6], b[6])
            du_soft = delta(a[7], b[7])
            du_steal = delta(a[8], b[8])
            total = du_user + du_nice + du_sys + du_idle + du_iow + du_irq + du_soft + du_steal
            if (total <= 0) {
                printf "0 0 0 0 0\n"
                exit
            }
            usage = (total - du_idle) / total * 100
            user_pct = (du_user + du_nice) / total * 100
            sys_pct = (du_sys + du_irq + du_soft) / total * 100
            iow_pct = du_iow / total * 100
            steal_pct = du_steal / total * 100
            printf "%.2f %.2f %.2f %.2f %.2f\n", usage, user_pct, sys_pct, iow_pct, steal_pct
        }
    '
}

read_cgroup_cpu_usage_usec() {
    awk '/^usage_usec / { print $2; exit }' "$1/cpu.stat"
}

get_cpu_capacity_usec_per_second() {
    local cgroup_dir="$1" online_cpus quota period quota_capacity

    online_cpus=$(get_online_cpu_count)

    if [[ ! -r "$cgroup_dir/cpu.max" ]]; then
        echo $((online_cpus * 1000000))
        return
    fi

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
    awk '
        function normalize_iface(name) {
            sub(/@.*$/, "", name)
            return name
        }
        function is_common_interface(name) {
            name = normalize_iface(name)
            if (name == "" || name == "lo") return 0
            if (name ~ /^veth/ || name ~ /^fwbr/ || name ~ /^docker/ || name ~ /^br-/ || name ~ /^virbr/) return 0
            if (name ~ /^(tap|tun|wg|dummy|nlmon|ifb|vnet|lxc|tailscale|pterodactyl)/) return 0
            return name ~ /^(eth[0-9]+|ens[0-9]+|enp[0-9]+s[0-9]+(d[0-9]+)?(f[0-9]+)?|eno[0-9]+|enx[0-9a-f]+|em[0-9]+|wlan[0-9]+|wlp[0-9]+s[0-9]+|bond[0-9]+|nic[0-9]+|vmbr[0-9]+)$/
        }
        NR > 2 {
            iface = $1
            sub(/:$/, "", iface)
            if (!is_common_interface(iface)) next
            iface = normalize_iface(iface)
            print iface, $2, $3, $4, $10, $11, $12
        }
    ' /proc/net/dev
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

zfs_mount_gets_pool_io() {
    local source="$1" mount="$2"
    local pool="${source%%/*}"

    [[ "$mount" == "/" ]] && return 0
    [[ "$source" == "$pool" ]] && return 0
    return 1
}

read_disk_space_stats() {
    local -a df_exclude=(
        tmpfs devtmpfs overlay squashfs efivarfs fuse fusectl shfs
        autofs nsfs binfmt_misc tracefs debugfs securityfs pstore
        hugetlbfs mqueue configfs rpc_pipefs
        nfs nfs4 cifs smb3 9p ceph cephfs glusterfs vmhgfs vboxsf
    )
    local df_args=(-B1 -PT)

    for fstype in "${df_exclude[@]}"; do
        df_args+=(-x "$fstype")
    done

    df "${df_args[@]}" 2>/dev/null | awk '
        NR > 1 {
            source = $1
            fstype = $2
            used = $4 + 0
            avail = $5 + 0
            mount = $7
            total = used + avail

            if (total <= 0) next
            if (source ~ /^[0-9]+(\.[0-9]+){3}:/ ) next
            if (source ~ /^[^/]+@/ ) next
            if (source ~ /^\/dev\/(zd|fuse|loop)/ ) next
            if (source ~ /^(shfs|mergerfs|portal|rclone|vmhgfs|vboxsf)/ ) next
            if (mount ~ /^\/(run|dev|sys|proc|snap)(\/|$)/ ) next
            if (mount ~ /^\/var\/lib\/(docker|containerd|lxc|libvirt)(\/|$)/ ) next

            if (fstype == "zfs") {
                disk_type = "zfs"
            } else if (source ~ /^\//) {
                disk_type = "block"
            } else {
                next
            }

            printf "%s\t%s\t%d\t%d\t%s\n", mount, source, used, total, disk_type
        }
    '
}

read_zfs_pool_io_snapshot() {
    local io pool

    [[ -d /proc/spl/kstat/zpool ]] || return 0

    for io in /proc/spl/kstat/zpool/*/io; do
        [[ -e "$io" ]] || continue
        [[ -r "$io" ]] || continue
        pool=$(basename "$(dirname "$io")")
        awk -v pool="$pool" '
            $1 == "nread" { nread = $NF }
            $1 == "nwritten" { nwritten = $NF }
            END {
                printf "%s\t%d\t%d\n", pool, nread + 0, nwritten + 0
            }
        ' "$io"
    done
}

start_zfs_pool_io_rates_sample() {
    local tmp

    command -v zpool >/dev/null 2>&1 || return 1
    tmp=$(mktemp)
    zpool iostat -yqHp 1 1 >"$tmp" 2>/dev/null &
    echo "$tmp $!"
}

read_zfs_pool_io_rates() {
    local path="$1"

    [[ -r "$path" ]] || return 0
    awk '
        /^[[:space:]]/ { next }
        $1 ~ /^\// { next }
        NF >= 7 {
            printf "%s\t%d\t%d\n", $1, $6 + 0, $7 + 0
        }
    ' "$path"
}

lookup_zfs_pool_io_rates() {
    local pool="$1" rates="$2"
    awk -v pool="$pool" -v rates="$rates" '
        function find_rates(data, name,    lines, i, n, parts) {
            n = split(data, lines, "\n")
            for (i = 1; i <= n; i++) {
                if (lines[i] == "") continue
                split(lines[i], parts, "\t")
                if (parts[1] == name) {
                    read_bps = parts[2]
                    write_bps = parts[3]
                    return 1
                }
            }
            return 0
        }
        BEGIN {
            if (!find_rates(rates, pool)) exit 1
            printf "%d %d\n", read_bps + 0, write_bps + 0
        }
    '
}

read_diskstats() {
    awk '$3 !~ /^(loop|ram|fd|zram)/ {
        print $3, $4, $6, $7, $8, $10, $11, $13, $14
    }' /proc/diskstats
}

build_zfs_pool_vdev_map() {
    local pool path name

    command -v zpool >/dev/null 2>&1 || return 0

    while IFS=$'\t' read -r pool path; do
        [[ -z "$pool" || -z "$path" ]] && continue
        name=$(basename "$(readlink -f "$path" 2>/dev/null)" 2>/dev/null) || continue
        [[ -n "$name" ]] && printf '%s\t%s\n' "$pool" "$name"
    done < <(zpool status -P 2>/dev/null | awk '
        /^  pool: / { pool = $2; next }
        /\/dev\// && pool != "" {
            if (match($0, /\/dev\/[^[:space:]]+/)) {
                print pool "\t" substr($0, RSTART, RLENGTH)
            }
        }
    ')
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

diskstats_has_device() {
    local device="$1"
    awk -v d="$device" '$3 == d { found = 1; exit } END { exit !found }' /proc/diskstats
}

resolve_diskstats_device_name() {
    local fs="$1"
    local path base candidate parent

    [[ "$fs" == /dev/* ]] || return 1
    path=$(readlink -f "$fs" 2>/dev/null) || path="$fs"
    base=$(basename "$path")

    # mdadm reports IO on the array device, not mdNpM partitions.
    if [[ "$base" =~ ^(md[0-9]+)p[0-9]+$ ]]; then
        parent="${BASH_REMATCH[1]}"
        if diskstats_has_device "$parent"; then
            echo "$parent"
            return 0
        fi
    fi

    for candidate in "$(basename "$fs")" "$base"; do
        [[ -z "$candidate" ]] && continue
        if diskstats_has_device "$candidate"; then
            echo "$candidate"
            return 0
        fi
    done

    if [[ "$base" =~ ^(nvme[0-9]+n[0-9]+)p[0-9]+$ ]]; then
        parent="${BASH_REMATCH[1]}"
    elif [[ "$base" =~ ^(sd[a-z]+|vd[a-z]+|hd[a-z]+)[0-9]+$ ]]; then
        parent="${BASH_REMATCH[1]}"
    else
        return 1
    fi

    if diskstats_has_device "$parent"; then
        echo "$parent"
        return 0
    fi

    return 1
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
                    sectors_written = parts[6]
                    write_ms = parts[7]
                    io_ms = parts[8]
                    weighted_io_ms = parts[9]
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
            prev_sectors_written = sectors_written
            prev_write_ms = write_ms
            prev_io_ms = io_ms
            prev_weighted_io_ms = weighted_io_ms

            if (!find_stats(after, device)) exit 1

            read_bps = (sectors_read - prev_sectors_read) * sector_bytes
            write_bps = (sectors_written - prev_sectors_written) * sector_bytes
            weighted_io_ms_delta = weighted_io_ms - prev_weighted_io_ms
            read_ms_delta = read_ms - prev_read_ms
            write_ms_delta = write_ms - prev_write_ms
            ios = (reads - prev_reads) + (writes - prev_writes)

            if (read_bps < 0) read_bps = 0
            if (write_bps < 0) write_bps = 0
            if (weighted_io_ms_delta < 0) weighted_io_ms_delta = 0
            if (read_ms_delta < 0) read_ms_delta = 0
            if (write_ms_delta < 0) write_ms_delta = 0
            if (ios < 0) ios = 0

            io_usage = weighted_io_ms_delta / 10
            io_wait = (ios > 0) ? (read_ms_delta + write_ms_delta) / ios : 0

            printf "%d %d %.2f %.2f", read_bps, write_bps, io_usage, io_wait
        }
    '
}

lookup_zfs_pool_vdev_io_util() {
    local pool="$1" before="$2" after="$3" vdev_map="$4"
    local vdev p line read_bps write_bps usage wait
    local max_usage=0 max_wait=0 found=0

    while IFS=$'\t' read -r p vdev; do
        [[ "$p" != "$pool" || -z "$vdev" ]] && continue
        line=$(lookup_diskstats_delta "$vdev" "$before" "$after") || continue
        read -r read_bps write_bps usage wait <<<"$line"
        found=1
        if awk -v u="$usage" -v m="$max_usage" 'BEGIN { exit !(u > m) }'; then
            max_usage=$usage
        fi
        if (( read_bps + write_bps > 0 )) && awk -v w="$wait" -v m="$max_wait" 'BEGIN { exit !(w > m) }'; then
            max_wait=$wait
        fi
    done <<<"$vdev_map"

    (( found )) || return 1
    printf '%.2f %.2f\n' "$max_usage" "$max_wait"
}

lookup_cgroup_io_bps() {
    local before="$1" after="$2"
    awk -v before="$before" -v after="$after" '
        BEGIN {
            read_total = 0
            write_total = 0
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
                        read_delta = curr[2] - prev[2]
                        write_delta = curr[3] - prev[3]
                        if (read_delta > 0) read_total += read_delta
                        if (write_delta > 0) write_total += write_delta
                        break
                    }
                }
            }
            printf "%d %d\n", read_total + 0, write_total + 0
        }
    '
}

lookup_zfs_pool_io_bps() {
    local pool="$1" before="$2" after="$3"
    awk -v pool="$pool" -v before="$before" -v after="$after" '
        function find_bytes(data, name,    lines, i, n, parts) {
            n = split(data, lines, "\n")
            for (i = 1; i <= n; i++) {
                if (lines[i] == "") continue
                split(lines[i], parts, "\t")
                if (parts[1] == name) {
                    nread = parts[2]
                    nwritten = parts[3]
                    return 1
                }
            }
            return 0
        }
        BEGIN {
            if (!find_bytes(before, pool)) exit 1
            prev_nread = nread
            prev_nwritten = nwritten
            if (!find_bytes(after, pool)) exit 1
            read_delta = nread - prev_nread
            write_delta = nwritten - prev_nwritten
            if (read_delta < 0) read_delta = 0
            if (write_delta < 0) write_delta = 0
            printf "%d %d\n", read_delta + 0, write_delta + 0
        }
    '
}

compute_disk_metrics_json() {
    local df_stats="$1" diskstats1="$2" diskstats2="$3" cgroup_io1="$4" cgroup_io2="$5" zfs_io1="$6" zfs_io2="$7" zfs_io_rates="$8" zfs_vdev_map="$9"
    local mount source used total disk_type device io_line read_bps write_bps io_usage io_wait majmin cgroup_device zfs_pool

    if [[ -n "$cgroup_io1" ]]; then
        majmin=$(awk 'NR == 1 { print $1; exit }' <<<"$cgroup_io1")
        cgroup_device=$(resolve_block_device_name "$majmin" 2>/dev/null || true)
    fi

    while IFS=$'\t' read -r mount source used total disk_type; do
        [[ -z "$mount" ]] && continue

        device=""
        read_bps=0
        write_bps=0
        io_usage="0.00"
        io_wait="0.00"
        zfs_pool="${source%%/*}"

        if [[ "$disk_type" == "zfs" ]] && zfs_mount_gets_pool_io "$source" "$mount"; then
            if [[ -n "$zfs_io_rates" ]] \
                && io_line=$(lookup_zfs_pool_io_rates "$zfs_pool" "$zfs_io_rates"); then
                read -r read_bps write_bps <<<"$io_line"
            elif [[ -n "$zfs_io1" && -n "$zfs_io2" ]] \
                && io_line=$(lookup_zfs_pool_io_bps "$zfs_pool" "$zfs_io1" "$zfs_io2"); then
                read -r read_bps write_bps <<<"$io_line"
            fi
            if [[ -n "$zfs_vdev_map" ]] \
                && io_line=$(lookup_zfs_pool_vdev_io_util "$zfs_pool" "$diskstats1" "$diskstats2" "$zfs_vdev_map"); then
                read -r io_usage io_wait <<<"$io_line"
            fi
        elif [[ "$disk_type" == "block" ]]; then
            device=$(resolve_diskstats_device_name "$source" || basename "$source")
            if io_line=$(lookup_diskstats_delta "$device" "$diskstats1" "$diskstats2"); then
                read -r read_bps write_bps io_usage io_wait <<<"$io_line"
            fi
        elif [[ -n "$cgroup_io1" && -n "$cgroup_io2" ]]; then
            if io_line=$(lookup_cgroup_io_bps "$cgroup_io1" "$cgroup_io2"); then
                read -r read_bps write_bps <<<"$io_line"
            fi
        elif [[ -n "$cgroup_device" ]] && io_line=$(lookup_diskstats_delta "$cgroup_device" "$diskstats1" "$diskstats2"); then
            read -r read_bps write_bps io_usage io_wait <<<"$io_line"
        fi

        printf '%s\t%d\t%d\t%d\t%d\t%s\t%s\n' \
            "$mount" "$used" "$total" "$read_bps" "$write_bps" "$io_usage" "$io_wait"
    done <<<"$df_stats" | jq -R -s '
        split("\n")
        | map(select(length > 0))
        | map(split("\t"))
        | map({
            diskName: .[0],
            usedBytes: (.[1] | tonumber),
            totalBytes: (.[2] | tonumber),
            ioReadBytesPerSecond: (.[3] | tonumber),
            ioWriteBytesPerSecond: (.[4] | tonumber),
            ioUsagePercent: (.[5] | tonumber),
            ioWaitMilliseconds: (.[6] | tonumber)
        })
    '
}

compute_interface_metrics_json() {
    local snap1="$1" snap2="$2"

    awk '
        function normalize_iface(name) {
            sub(/@.*$/, "", name)
            return name
        }
        function is_common_interface(name) {
            name = normalize_iface(name)
            if (name == "" || name == "lo") return 0
            if (name ~ /^veth/ || name ~ /^fwbr/ || name ~ /^docker/ || name ~ /^br-/ || name ~ /^virbr/) return 0
            if (name ~ /^(tap|tun|wg|dummy|nlmon|ifb|vnet|lxc|tailscale|pterodactyl)/) return 0
            return name ~ /^(eth[0-9]+|ens[0-9]+|enp[0-9]+s[0-9]+(d[0-9]+)?(f[0-9]+)?|eno[0-9]+|enx[0-9a-f]+|em[0-9]+|wlan[0-9]+|wlp[0-9]+s[0-9]+|bond[0-9]+|nic[0-9]+|vmbr[0-9]+)$/
        }
        FNR == NR {
            if ($1 == "" || !is_common_interface($1)) next
            iface = normalize_iface($1)
            prev_rx_bytes[iface] = $2
            prev_rx_packets[iface] = $3
            prev_rx_errors[iface] = $4
            prev_tx_bytes[iface] = $5
            prev_tx_packets[iface] = $6
            prev_tx_errors[iface] = $7
            next
        }
        $1 == "" || !is_common_interface($1) { next }
        {
            iface = normalize_iface($1)
            if (!(iface in prev_rx_bytes)) next

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
        | map(select(.[0] | length > 0))
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
    local diskstats1 diskstats2 cgroup_io1 cgroup_io2 zfs_io1 zfs_io2 zfs_io_rates zfs_vdev_map df_stats
    local zfs_iostat_tmp="" zfs_iostat_pid=""
    local cpu_line1 cpu_line2 cpu_metrics interface_metrics disk_metrics

    cgroup_dir=$(get_cgroup_dir)
    if [[ -n "$cgroup_dir" && -r "$cgroup_dir/cpu.stat" ]]; then
        cpu_usage1=$(read_cgroup_cpu_usage_usec "$cgroup_dir")
    fi

    df_stats=$(read_disk_space_stats)
    zfs_vdev_map=$(build_zfs_pool_vdev_map)
    diskstats1=$(read_diskstats)
    cgroup_io1=$(read_cgroup_io_stats)
    zfs_io1=$(read_zfs_pool_io_snapshot)
    if [[ -z "$zfs_io1" ]]; then
        read -r zfs_iostat_tmp zfs_iostat_pid < <(start_zfs_pool_io_rates_sample || true)
    fi
    cpu_line1=$(read_cpu_stat_line)
    net_snap1=$(read_network_stats)
    sleep 1

    if [[ -n "$cpu_usage1" ]]; then
        cpu_usage2=$(read_cgroup_cpu_usage_usec "$cgroup_dir")
        RATE_CPU_USAGE=$(compute_cpu_usage_from_cgroup_samples "$cpu_usage1" "$cpu_usage2") || RATE_CPU_USAGE=""
    fi

    diskstats2=$(read_diskstats)
    cgroup_io2=$(read_cgroup_io_stats)
    if [[ -n "$zfs_iostat_tmp" ]]; then
        wait "$zfs_iostat_pid" 2>/dev/null || true
        zfs_io_rates=$(read_zfs_pool_io_rates "$zfs_iostat_tmp")
        rm -f "$zfs_iostat_tmp"
    else
        zfs_io2=$(read_zfs_pool_io_snapshot)
    fi
    cpu_line2=$(read_cpu_stat_line)
    net_snap2=$(read_network_stats)

    read -r _cpu_usage RATE_CPU_USER RATE_CPU_SYSTEM RATE_CPU_IOWAIT RATE_CPU_STEAL \
        < <(compute_cpu_metrics_from_proc_stat_samples "$cpu_line1" "$cpu_line2")
    if [[ -z "${RATE_CPU_USAGE:-}" ]]; then
        RATE_CPU_USAGE="$_cpu_usage"
    fi

    read -r RATE_LOAD1 RATE_LOAD5 RATE_LOAD15 < <(get_load_averages)

    interface_metrics=$(compute_interface_metrics_json "$net_snap1" "$net_snap2")
    disk_metrics=$(compute_disk_metrics_json "$df_stats" "$diskstats1" "$diskstats2" "$cgroup_io1" "$cgroup_io2" "$zfs_io1" "$zfs_io2" "$zfs_io_rates" "$zfs_vdev_map")

    RATE_INTERFACE_METRICS="$interface_metrics"
    RATE_DISK_METRICS="$disk_metrics"
}

get_load_averages() {
    awk '{ printf "%.2f %.2f %.2f", $1, $2, $3 }' /proc/loadavg
}

read_memory_extras() {
    awk '
        /^Buffers:/ { buffers = $2 * 1024 }
        /^Cached:/ { cached = $2 * 1024 }
        /^SwapTotal:/ { swap_total = $2 * 1024 }
        /^SwapFree:/ { swap_free = $2 * 1024 }
        END {
            swap_used = swap_total - swap_free
            if (swap_used < 0) swap_used = 0
            printf "%d %d %d %d\n", buffers + 0, cached + 0, swap_used + 0, swap_total + 0
        }
    ' /proc/meminfo
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
    local mem_buffers mem_cached swap_used swap_total

    sample_rate_metrics
    read -r mem_buffers mem_cached swap_used swap_total < <(read_memory_extras)

    jq -n \
        --arg ip "$(get_ip)" \
        --argjson coreCount "$(get_core_count)" \
        --argjson threadCount "$(get_thread_count)" \
        --arg osName "$(get_os_name)" \
        --arg osVersion "$(get_os_version)" \
        --argjson uptimeSeconds "$(get_uptime_seconds)" \
        --arg cpuModel "$(get_cpu_model)" \
        --argjson socketCount "$(get_socket_count)" \
        --argjson cpuClockMhz "$(get_cpu_clock_mhz 2>/dev/null || echo 0)" \
        --argjson cpuUsage "${RATE_CPU_USAGE:-0}" \
        --argjson memoryUsage "$(get_memory_usage)" \
        --argjson memoryTotal "$(get_memory_total)" \
        --argjson load1 "${RATE_LOAD1:-0}" \
        --argjson load5 "${RATE_LOAD5:-0}" \
        --argjson load15 "${RATE_LOAD15:-0}" \
        --argjson cpuUserPercent "${RATE_CPU_USER:-0}" \
        --argjson cpuSystemPercent "${RATE_CPU_SYSTEM:-0}" \
        --argjson cpuIowaitPercent "${RATE_CPU_IOWAIT:-0}" \
        --argjson cpuStealPercent "${RATE_CPU_STEAL:-0}" \
        --argjson memoryBuffers "$mem_buffers" \
        --argjson memoryCached "$mem_cached" \
        --argjson swapUsed "$swap_used" \
        --argjson swapTotal "$swap_total" \
        --argjson interfaceMetrics "$RATE_INTERFACE_METRICS" \
        --argjson diskMetrics "$RATE_DISK_METRICS" \
        '{
            serverDetails: {
                ip: $ip,
                coreCount: $coreCount,
                threadCount: $threadCount,
                osName: $osName,
                osVersion: $osVersion,
                uptimeSeconds: $uptimeSeconds,
                cpuModel: $cpuModel,
                socketCount: $socketCount,
                cpuClockMhz: $cpuClockMhz
            },
            serverMetrics: {
                cpuUsage: $cpuUsage,
                memoryUsage: $memoryUsage,
                memoryTotal: $memoryTotal,
                load1: $load1,
                load5: $load5,
                load15: $load15,
                cpuUserPercent: $cpuUserPercent,
                cpuSystemPercent: $cpuSystemPercent,
                cpuIowaitPercent: $cpuIowaitPercent,
                cpuStealPercent: $cpuStealPercent,
                memoryBuffers: $memoryBuffers,
                memoryCached: $memoryCached,
                swapUsed: $swapUsed,
                swapTotal: $swapTotal
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