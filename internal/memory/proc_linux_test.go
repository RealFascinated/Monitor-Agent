//go:build linux

package memory

import (
	"testing"

	"fascinated.cc/monitor/agent/internal/linux"
)

func TestOverlayLxcfsMemory(t *testing.T) {
	t.Parallel()

	got := overlayLxcfsMemory(Snapshot{})
	if got.Total != 0 || got.Usage != 0 {
		t.Fatalf("empty snapshot = %#v, want zero values", got)
	}
}

func TestOverlayLxcfsMemoryWithCurrent(t *testing.T) {
	t.Parallel()

	snap := Snapshot{Total: 8589934592}
	limit := uint64(snap.Total)
	current := uint64(628408320)

	snap.Usage = float64(current)
	if current >= limit {
		snap.Available = 0
	} else {
		snap.Available = float64(limit - current)
	}

	if snap.Usage != 628408320 {
		t.Fatalf("usage = %v, want 628408320", snap.Usage)
	}
	if snap.Available != float64(limit-current) {
		t.Fatalf("available = %v, want %v", snap.Available, limit-current)
	}
}

func TestOverlayCgroupMemory(t *testing.T) {
	t.Parallel()

	snap := Snapshot{Total: 8e9, Available: 490e6}
	cg := linux.CgroupMemory{
		Max:     8e9,
		Current: 628408320,
		OK:      true,
	}

	got := overlayCgroupMemory(snap, cg)
	if got.Total != 8e9 {
		t.Fatalf("total = %v, want 8e9", got.Total)
	}
	if got.Usage != 628408320 {
		t.Fatalf("usage = %v, want 628408320", got.Usage)
	}
	if got.Available != 8e9-628408320 {
		t.Fatalf("available = %v, want %v", got.Available, 8e9-628408320)
	}
}
