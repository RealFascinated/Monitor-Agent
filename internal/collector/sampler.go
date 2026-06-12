package collector

import (
	"sync"
	"time"

	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/platform"
)

// Sampler maintains stateful metric snapshots for background sampling and push.
type Sampler struct {
	opts Options

	mu     sync.RWMutex
	tickMu sync.Mutex

	result   Result
	ready    bool
	clockMHz float64
	backend  platform.Backend
}

func (s *Sampler) ClockMHz() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clockMHz
}

func NewSampler(opts Options) *Sampler {
	return &Sampler{
		opts: opts,
		result: Result{
			InterfaceMetrics: []ingest.InterfaceMetrics{},
			DiskMetrics:      []ingest.DiskMetric{},
		},
		backend: platform.New(platform.Options{
			HasZFS:       opts.HasZFS,
			EnableDocker: opts.EnableDocker,
			EnableGPU:    opts.EnableGPU,
		}),
	}
}

func (s *Sampler) Tick() error {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	return s.tick()
}

func (s *Sampler) RefreshSlow() error {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	return s.refreshSlow()
}

// RunOnce performs two ticks and one slow refresh for one-shot collection (print mode).
func (s *Sampler) RunOnce(interval time.Duration) error {
	if err := s.Tick(); err != nil {
		return err
	}
	time.Sleep(interval)
	if err := s.Tick(); err != nil {
		return err
	}
	return s.RefreshSlow()
}

func (s *Sampler) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

func (s *Sampler) Snapshot() Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneResult(s.result)
}

func (s *Sampler) setResult(result Result, ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = result
	s.ready = ready
}

func cloneResult(r Result) Result {
	out := r
	if r.InterfaceMetrics != nil {
		out.InterfaceMetrics = append([]ingest.InterfaceMetrics(nil), r.InterfaceMetrics...)
	}
	if r.DiskMetrics != nil {
		out.DiskMetrics = append([]ingest.DiskMetric(nil), r.DiskMetrics...)
	}
	if r.ZfsPoolMetrics != nil {
		out.ZfsPoolMetrics = append([]ingest.ZfsPoolMetric(nil), r.ZfsPoolMetrics...)
	}
	if r.DockerContainers != nil {
		out.DockerContainers = append([]ingest.DockerContainerMetric(nil), r.DockerContainers...)
	}
	if r.GPUMetrics != nil {
		out.GPUMetrics = append([]ingest.GPUMetric(nil), r.GPUMetrics...)
	}
	if r.TCPConnectionMetrics != nil {
		out.TCPConnectionMetrics = append([]ingest.TCPConnectionMetric(nil), r.TCPConnectionMetrics...)
	}
	if r.ServerMetrics.CPUCoreMetrics != nil {
		out.ServerMetrics.CPUCoreMetrics = append([]ingest.CPUCoreMetric(nil), r.ServerMetrics.CPUCoreMetrics...)
	}
	if r.ServerMetrics.TemperatureMetrics != nil {
		out.ServerMetrics.TemperatureMetrics = append([]ingest.TemperatureMetric(nil), r.ServerMetrics.TemperatureMetrics...)
	}
	if r.ZfsArcMetrics != nil {
		arc := *r.ZfsArcMetrics
		out.ZfsArcMetrics = &arc
	}
	return out
}

// WaitReady blocks until at least one rate sample is available or the timeout elapses.
func (s *Sampler) WaitReady(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
