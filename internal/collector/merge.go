package collector

import (
	"fascinated.cc/monitor/agent/internal/disk"
	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/platform"
)

func (s *Sampler) mergeTick(update platform.TickUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.result
	r.ServerMetrics = update.ServerMetrics
	r.InterfaceMetrics = update.InterfaceMetrics
	r.DiskMetrics = update.DiskMetrics
	r.ZfsArcMetrics = update.ZfsArcMetrics
	r.TCPConnectionMetrics = update.TCPConnectionMetrics
	s.result = r
	s.ready = update.Ready
	s.clockMHz = update.ClockMHz
}

func (s *Sampler) mergeSlow(update platform.SlowUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.result
	if update.ZfsPoolMetrics != nil {
		r.ZfsPoolMetrics = update.ZfsPoolMetrics
	}
	if update.DockerContainers != nil {
		r.DockerContainers = update.DockerContainers
	}
	if update.GPUMetrics != nil {
		r.GPUMetrics = update.GPUMetrics
	}
	overlaySlowServerMetrics(&r.ServerMetrics, update.ServerMetrics)
	if update.HasMounts {
		disk.UpdateUsageFromMounts(r.DiskMetrics, update.Mounts)
	}
	s.result = r
}

func overlaySlowServerMetrics(dest *ingest.ServerMetrics, src ingest.ServerMetrics) {
	if len(src.TemperatureMetrics) > 0 {
		dest.TemperatureMetrics = src.TemperatureMetrics
	}
	if len(src.CPUCoreMetrics) > 0 {
		dest.CPUCoreMetrics = src.CPUCoreMetrics
	}
	if src.CPUPowerWatts > 0 {
		dest.CPUPowerWatts = src.CPUPowerWatts
	}
	if src.MemoryTotal > 0 {
		dest.MemoryUsage = src.MemoryUsage
		dest.MemoryAvailable = src.MemoryAvailable
		dest.MemoryTotal = src.MemoryTotal
	}
}
