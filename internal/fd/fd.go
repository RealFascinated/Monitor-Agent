package fd

import "fascinated.cc/monitor/agent/internal/ingest"

type Snapshot struct {
	Open, Max int64
}

func Read() Snapshot {
	return read()
}

func ApplyTo(metrics *ingest.ServerMetrics, s Snapshot) {
	metrics.FdOpen = s.Open
	metrics.FdMax = s.Max
	if s.Max > 0 {
		metrics.FdUsagePercent = float64(s.Open) / float64(s.Max) * 100
	}
}
