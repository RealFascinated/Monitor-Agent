package fd

import (
	"math"
	"testing"

	"fascinated.cc/monitor/agent/internal/ingest"
)

func TestApplyToUnlimitedFileMax(t *testing.T) {
	t.Parallel()

	var metrics ingest.ServerMetrics
	ApplyTo(&metrics, Snapshot{Open: 13706, Max: math.MaxInt64})
	if metrics.FdOpen != 0 || metrics.FdMax != 0 || metrics.FdUsagePercent != 0 {
		t.Fatalf("expected no fd metrics for unlimited max, got %+v", metrics)
	}
}

func TestApplyToLimitedFileMax(t *testing.T) {
	t.Parallel()

	var metrics ingest.ServerMetrics
	ApplyTo(&metrics, Snapshot{Open: 1000, Max: 2000})
	if metrics.FdMax != 2000 {
		t.Fatalf("FdMax = %d, want 2000", metrics.FdMax)
	}
	if metrics.FdUsagePercent != 50 {
		t.Fatalf("FdUsagePercent = %v, want 50", metrics.FdUsagePercent)
	}
}
