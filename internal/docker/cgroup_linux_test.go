//go:build linux

package docker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverDockerCgroupsUnified(t *testing.T) {
	root := t.TempDir()
	cgroupRoot := filepath.Join(root, "sys", "fs", "cgroup")
	systemSlice := filepath.Join(cgroupRoot, "system.slice", "docker-abc123def456.scope")
	if err := os.MkdirAll(systemSlice, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemSlice, "cpu.stat"), []byte("usage_usec 1000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemSlice, "memory.current"), []byte("1048576\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MONITOR_HOST_ROOT", root)

	containers := discoverDockerCgroups()
	if len(containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(containers))
	}
	if containers[0].id != "abc123def456" {
		t.Fatalf("id = %q", containers[0].id)
	}
}

func BenchmarkDockerCgroupDiscovery(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = discoverDockerCgroups()
	}
}
