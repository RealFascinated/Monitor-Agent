//go:build windows

package collector

import (
	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/lhm"
)

func serverMetricsFromLHM(snap lhm.ServerSnapshot) ingest.ServerMetrics {
	var metrics ingest.ServerMetrics
	lhm.ApplyServerSnapshot(&metrics, snap)
	return metrics
}
