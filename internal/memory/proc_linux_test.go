//go:build linux

package memory

import (
	"testing"

	"fascinated.cc/monitor/agent/internal/linux"
)

func TestOverlayCgroupMemory(t *testing.T) {
	t.Parallel()

	snap := Snapshot{Total: 8e9, Available: 490e6}
	cg := linux.CgroupMemory{
		Max:     8e9,
		Current: 7898e6,
		File:    7600e6,
		OK:      true,
	}

	got := overlayCgroupMemory(snap, cg)
	if got.Total != 8e9 {
		t.Fatalf("total = %v, want 8e9", got.Total)
	}
	if got.Usage != 298e6 {
		t.Fatalf("usage = %v, want 298e6", got.Usage)
	}
	if got.Available != 8e9-298e6 {
		t.Fatalf("available = %v, want %v", got.Available, 8e9-298e6)
	}
}
