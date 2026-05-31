//go:build windows

package collector

import (
	"testing"

	"github.com/shirou/gopsutil/v4/cpu"
)

func TestBuildWindowsServerMetricsIowait(t *testing.T) {
	before := cpu.TimesStat{User: 100, System: 50, Idle: 850}
	after := cpu.TimesStat{User: 200, System: 100, Idle: 1600}

	metrics, _ := buildWindowsServerMetrics(before, after, nil, nil, 3.5)
	if metrics.CPUIowaitPercent != 3.5 {
		t.Fatalf("iowait: %v", metrics.CPUIowaitPercent)
	}
}
