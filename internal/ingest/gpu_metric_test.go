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
	if !strings.Contains(string(data), `"encoderUsagePercent":0`) {
		t.Fatalf("expected zero encoder usage in JSON, got %s", data)
	}
	if !strings.Contains(string(data), `"decoderUsagePercent":0`) {
		t.Fatalf("expected zero decoder usage in JSON, got %s", data)
	}
	for _, field := range []string{
		`"memoryUsedBytes":0`,
		`"memoryTotalBytes":0`,
		`"temperatureCelsius":0`,
		`"powerWatts":0`,
	} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("expected %s in JSON, got %s", field, data)
		}
	}
}
