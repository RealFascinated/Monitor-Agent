//go:build linux

package linux

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func Dir() string {
	if !IsContainer() {
		return ""
	}

	dir := "/sys/fs/cgroup"
	if cgroupDirUsable(dir) {
		return dir
	}
	if dir := cgroup1Dir(); dir != "" && cgroupDirUsable(dir) {
		return dir
	}
	return ""
}

func cgroupDirUsable(dir string) bool {
	for _, name := range []string{
		"cpu.stat",
		"memory.current",
		"cpuacct.usage",
		"cpuset.cpus.effective",
		"cpuset.cpus",
	} {
		if _, err := os.Stat(dir + "/" + name); err == nil {
			return true
		}
	}
	return false
}

func cgroup1Dir() string {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}

	var controller, rel string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 || fields[0] == "0" {
			continue
		}
		if strings.Contains(fields[1], "cpu") {
			controller = fields[1]
			rel = fields[2]
			break
		}
	}
	if rel == "" {
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			fields := strings.SplitN(line, ":", 3)
			if len(fields) != 3 || fields[0] == "0" {
				continue
			}
			if strings.Contains(fields[1], "memory") {
				controller = fields[1]
				rel = fields[2]
				break
			}
		}
	}
	if rel == "" {
		return ""
	}

	mountRoot := cgroup1MountRoot(controller)
	if mountRoot == "" {
		return ""
	}
	return filepath.Join(mountRoot, strings.TrimPrefix(rel, "/"))
}

func cgroup1MountRoot(controller string) string {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}

	want := strings.Split(controller, ",")
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		sep := -1
		for i, field := range fields {
			if field == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+1 >= len(fields) || fields[sep+1] != "cgroup" {
			continue
		}
		mountPoint := fields[4]
		opts := ""
		if sep+2 < len(fields) {
			opts = fields[sep+2]
		}
		for _, ctrl := range want {
			if strings.Contains(mountPoint, ctrl) || strings.Contains(opts, ctrl) {
				return mountPoint
			}
		}
	}
	return ""
}

// ParseCPUSet parses a cpuset.cpus list such as "0-3,8,10-11".
func ParseCPUSet(value string) map[string]struct{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	out := make(map[string]struct{})
	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if dash := strings.Index(part, "-"); dash >= 0 {
			start, err1 := strconv.Atoi(strings.TrimSpace(part[:dash]))
			end, err2 := strconv.Atoi(strings.TrimSpace(part[dash+1:]))
			if err1 != nil || err2 != nil {
				continue
			}
			if end < start {
				start, end = end, start
			}
			for n := start; n <= end; n++ {
				out[strconv.Itoa(n)] = struct{}{}
			}
			continue
		}
		out[part] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EffectiveCPUs returns the CPUs assigned to the cgroup, if known.
func EffectiveCPUs(dir string) (map[string]struct{}, bool) {
	if dir == "" {
		return nil, false
	}
	for _, name := range []string{"cpuset.cpus.effective", "cpuset.cpus"} {
		data, err := os.ReadFile(dir + "/" + name)
		if err != nil {
			continue
		}
		cpus := ParseCPUSet(string(data))
		if len(cpus) > 0 {
			return cpus, true
		}
	}
	return nil, false
}

func CgroupMemoryBytes() (max, current uint64, ok bool) {
	dir := Dir()
	if dir == "" {
		return 0, 0, false
	}

	maxData, err := os.ReadFile(dir + "/memory.max")
	if err != nil {
		return 0, 0, false
	}
	currentData, err := os.ReadFile(dir + "/memory.current")
	if err != nil {
		return 0, 0, false
	}

	maxStr := strings.TrimSpace(string(maxData))
	if maxStr == "max" {
		return 0, 0, false
	}

	maxVal, err := strconv.ParseUint(maxStr, 10, 64)
	if err != nil || maxVal == 0 {
		return 0, 0, false
	}
	currentVal, err := strconv.ParseUint(strings.TrimSpace(string(currentData)), 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return maxVal, currentVal, true
}

func ReadIOStats() map[string]CgroupIOEntry {
	dir := Dir()
	if dir == "" {
		return map[string]CgroupIOEntry{}
	}

	file, err := os.Open(dir + "/io.stat")
	if err != nil {
		return map[string]CgroupIOEntry{}
	}
	defer file.Close()

	stats := make(map[string]CgroupIOEntry)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		entry := CgroupIOEntry{}
		for _, field := range fields[1:] {
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "rbytes":
				entry.Rbytes = ParseUint64(parts[1])
			case "wbytes":
				entry.Wbytes = ParseUint64(parts[1])
			}
		}
		stats[fields[0]] = entry
	}
	return stats
}

func ResolveBlockDeviceName(majmin string) string {
	path := "/sys/dev/block/" + majmin
	link, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	if strings.Contains(link, "/") {
		parts := strings.Split(link, "/")
		return parts[len(parts)-1]
	}
	return link
}
