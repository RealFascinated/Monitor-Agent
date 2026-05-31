//go:build !linux

package collector

import (
	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/metric"
)

func coreMetricsFromGopsutil(cores []metric.CoreUsage) []ingest.CPUCoreMetric {
	if len(cores) == 0 {
		return nil
	}
	out := make([]ingest.CPUCoreMetric, len(cores))
	for i, core := range cores {
		out[i] = ingest.CPUCoreMetric{
			CPU:          core.CPU,
			UsagePercent: core.UsagePercent,
		}
	}
	return out
}

func temperatureMetricsFromGopsutil(readings []metric.TemperatureReading) []ingest.TemperatureMetric {
	if len(readings) == 0 {
		return nil
	}
	out := make([]ingest.TemperatureMetric, len(readings))
	for i, reading := range readings {
		out[i] = ingest.TemperatureMetric{
			Sensor:  reading.Sensor,
			Celsius: reading.Celsius,
		}
	}
	return out
}
