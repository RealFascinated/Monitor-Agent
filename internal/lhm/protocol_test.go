package lhm

import (
	"testing"

	"fascinated.cc/monitor/agent/internal/ingest"
)

func TestParseServerMetricsJSON(t *testing.T) {
	raw := `{
		"cpuTotalPercent": 11.5,
		"cores": [{"cpu":"0","usagePercent":40},{"cpu":"1","usagePercent":5}],
		"memory": {"used":100,"available":50,"total":150},
		"temperatures": [{"sensor":"cpu_die","celsius":46.9}]
	}`
	snap, err := ParseServerMetricsJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if snap.CPUTotalPercent == nil || *snap.CPUTotalPercent != 11.5 {
		t.Fatalf("cpu total: %+v", snap.CPUTotalPercent)
	}
	if len(snap.Cores) != 2 || snap.Cores[0].CPU != "0" {
		t.Fatalf("cores: %+v", snap.Cores)
	}
	if !snap.Memory.Complete() {
		t.Fatal("expected complete memory")
	}

	var metrics ingest.ServerMetrics
	ApplyServerSnapshot(&metrics, snap)
	if metrics.CPUUsage != 11.5 {
		t.Fatalf("cpu usage: %v", metrics.CPUUsage)
	}
	if len(metrics.CPUCoreMetrics) != 2 {
		t.Fatalf("core metrics: %+v", metrics.CPUCoreMetrics)
	}
	if metrics.MemoryTotal != 150 {
		t.Fatalf("memory total: %v", metrics.MemoryTotal)
	}
	if len(metrics.TemperatureMetrics) != 1 || metrics.TemperatureMetrics[0].Sensor != "cpu_die" {
		t.Fatalf("temps: %+v", metrics.TemperatureMetrics)
	}
}

func TestApplyServerSnapshotPartialMemory(t *testing.T) {
	used := int64(10)
	snap := ServerSnapshot{
		Memory: MemorySnapshot{Used: &used},
	}
	var metrics ingest.ServerMetrics
	metrics.MemoryTotal = 99
	ApplyServerSnapshot(&metrics, snap)
	if metrics.MemoryTotal != 99 {
		t.Fatalf("partial memory should not overwrite: %v", metrics.MemoryTotal)
	}
}
