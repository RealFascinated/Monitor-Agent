//go:build linux

package gpu

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadAmdgpuIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "amdgpu.ids")
	content := "" +
		"# comment\n" +
		"744C, C8, AMD Radeon RX 7900 XTX\n" +
		"744C, CC, AMD Radeon RX 7900 XT\n" +
		"15BF, 01, AMD Radeon 760M Graphics\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	names := loadAmdgpuIDs(path)
	if got := names["744c:c8"]; got != "AMD Radeon RX 7900 XTX" {
		t.Fatalf("exact match: %q", got)
	}
	if got := names["744c"]; got != "AMD Radeon RX 7900 XTX" {
		t.Fatalf("device fallback: %q", got)
	}
	if got := names["15bf:01"]; got != "AMD Radeon 760M" {
		t.Fatalf("graphics suffix: %q", got)
	}
}

func TestAmdgpuDeviceNameFromSysfs(t *testing.T) {
	dir := t.TempDir()
	idsPath := filepath.Join(dir, "amdgpu.ids")
	if err := os.WriteFile(idsPath, []byte("744C, C8, AMD Radeon RX 7900 XTX\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deviceDir := filepath.Join(dir, "device")
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"device":   "0x744c",
		"revision": "0xc8",
	} {
		if err := os.WriteFile(filepath.Join(deviceDir, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	amdgpuIDsOnce = sync.Once{}
	amdgpuIDs = loadAmdgpuIDs(idsPath)
	got := amdgpuDeviceName(deviceDir)
	if got != "AMD Radeon RX 7900 XTX" {
		t.Fatalf("got %q", got)
	}
}
