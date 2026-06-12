package collector

import (
	"time"

	"fascinated.cc/monitor/agent/internal/ingest"
)

type Result struct {
	ServerMetrics        ingest.ServerMetrics
	InterfaceMetrics     []ingest.InterfaceMetrics
	DiskMetrics          []ingest.DiskMetric
	ZfsArcMetrics        *ingest.ZFSArcMetrics
	ZfsPoolMetrics       []ingest.ZfsPoolMetric
	DockerContainers     []ingest.DockerContainerMetric
	GPUMetrics           []ingest.GPUMetric
	TCPConnectionMetrics []ingest.TCPConnectionMetric
}

// Collect runs a one-shot sample (used by agent print). Waits for two ticks to produce rates.
func Collect(opts Options) (Result, error) {
	s := NewSampler(opts)
	if err := s.Tick(); err != nil {
		return Result{}, err
	}
	time.Sleep(time.Second)
	if err := s.Tick(); err != nil {
		return Result{}, err
	}
	_ = s.RefreshSlow()
	return s.Snapshot(), nil
}

func collect(opts Options) (Result, error) {
	return Collect(opts)
}
