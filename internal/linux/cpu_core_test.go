//go:build linux

package linux

import (
	"strings"
	"testing"
)

func TestComputePerCoreCPU(t *testing.T) {
	t.Parallel()

	before := map[string]CPUStat{
		"0": {User: 100, System: 50, Idle: 850},
		"1": {User: 200, System: 100, Idle: 700},
	}
	after := map[string]CPUStat{
		"0": {User: 200, System: 100, Idle: 1000},
		"1": {User: 300, System: 150, Idle: 850},
	}

	got := ComputePerCoreCPU(before, after)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].CPU != "0" || got[1].CPU != "1" {
		t.Fatalf("cores = %#v", got)
	}
	if got[0].UsagePercent != 50 || got[1].UsagePercent != 50 {
		t.Fatalf("usage = %#v", got)
	}
}

func TestParseProcStatPerCPU(t *testing.T) {
	t.Parallel()

	const sample = `cpu  10 0 20 70 0 0 0 0 0 0
cpu0 5 0 10 85 0 0 0 0 0 0
cpu1 5 0 10 85 0 0 0 0 0 0
ctxt 100
`
	snap := parseProcStat(strings.NewReader(sample))
	if !snap.HasCPU {
		t.Fatal("expected aggregate cpu")
	}
	if len(snap.PerCPU) != 2 {
		t.Fatalf("PerCPU len = %d, want 2", len(snap.PerCPU))
	}
	if snap.PerCPU["0"].Idle != 85 {
		t.Fatalf("cpu0 idle = %d", snap.PerCPU["0"].Idle)
	}
}
