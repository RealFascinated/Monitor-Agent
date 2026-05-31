//go:build linux

package linux

import (
	"path/filepath"
	"strings"
)

// hwmonDeviceID returns a stable per-device id (e.g. nvme0, sda) for generic hwmon chip names.
func hwmonDeviceID(hwmonDir string) string {
	root := hwmonRootDir(hwmonDir)
	if id := deviceIDFromPath(root); isDeviceScopedID(id, root) {
		return id
	}
	link := filepath.Join(root, "device")
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return filepath.Base(root)
	}
	if id := deviceIDFromPath(target); id != "" {
		return id
	}
	return filepath.Base(root)
}

func isDeviceScopedID(id, hwmonRoot string) bool {
	if id == "" || id == filepath.Base(hwmonRoot) {
		return false
	}
	return !strings.HasPrefix(id, "hwmon")
}

func hwmonRootDir(dir string) string {
	if filepath.Base(dir) == "device" {
		return filepath.Dir(dir)
	}
	return dir
}

func deviceIDFromPath(devicePath string) string {
	parts := strings.Split(strings.Trim(filepath.ToSlash(devicePath), "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if strings.HasPrefix(part, "nvme") && part != "nvme" {
			return part
		}
	}
	for i := len(parts) - 1; i >= 0; i-- {
		if isBlockDeviceName(parts[i]) {
			return parts[i]
		}
	}
	if pci := pciAddressFromPath(parts); pci != "" {
		return "pci-" + pci
	}
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func isBlockDeviceName(name string) bool {
	if strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "hd") {
		return len(name) >= 3
	}
	// nvme0n1 namespace block device
	return strings.HasPrefix(name, "nvme") && strings.Contains(name, "n")
}

func pciAddressFromPath(parts []string) string {
	for _, part := range parts {
		if strings.Count(part, ":") < 2 {
			continue
		}
		return strings.TrimPrefix(part, "0000:")
	}
	return ""
}

func usesDeviceScopedHwmonName(hwName string) bool {
	switch hwName {
	case "nvme", "drivetemp":
		return true
	default:
		return false
	}
}
