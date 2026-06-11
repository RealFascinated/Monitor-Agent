//go:build linux

package gpu

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fascinated.cc/monitor/agent/internal/linux"
)

const amdgpuIDsPath = "/usr/share/libdrm/amdgpu.ids"

var (
	amdgpuIDsOnce sync.Once
	amdgpuIDs     map[string]string
)

func amdgpuDeviceName(deviceDir string) string {
	deviceID := pciIDHex(filepath.Join(deviceDir, "device"))
	if deviceID == "" {
		return ""
	}
	revisionID := pciIDHex(filepath.Join(deviceDir, "revision"))

	amdgpuIDsOnce.Do(func() {
		amdgpuIDs = loadAmdgpuIDs(linux.HostPath(amdgpuIDsPath))
	})

	if revisionID != "" {
		if name := amdgpuIDs[deviceID+":"+revisionID]; name != "" {
			return name
		}
	}
	return amdgpuIDs[deviceID]
}

func loadAmdgpuIDs(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}

	names := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 3)
		if len(parts) != 3 {
			continue
		}
		deviceID := pciIDHex(strings.TrimSpace(parts[0]))
		revisionID := pciIDHex(strings.TrimSpace(parts[1]))
		name := normalizeAmdgpuName(strings.TrimSpace(parts[2]))
		if deviceID == "" || name == "" {
			continue
		}
		if revisionID != "" {
			names[deviceID+":"+revisionID] = name
		}
		if _, ok := names[deviceID]; !ok {
			names[deviceID] = name
		}
	}
	return names
}

func normalizeAmdgpuName(name string) string {
	for _, suffix := range []string{" Graphics", " Series"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}

func pciIDHex(raw string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "0x")
}
