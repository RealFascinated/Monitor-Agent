package ingest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGPUMetricJSONIncludesZeroUsage(t *testing.T) {
	data, err := json.Marshal(GPUMetric{
		DeviceID:     "abc",
		Name:         "GPU",
		Vendor:       "nvidia",
		UsagePercent: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"usagePercent":0`) {
		t.Fatalf("expected zero usage in JSON, got %s", data)
	}
}
