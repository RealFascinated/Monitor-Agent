//go:build windows

package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"fascinated.cc/monitor/agent/internal/counters"
	cpupkg "fascinated.cc/monitor/agent/internal/cpu"
	"fascinated.cc/monitor/agent/internal/delta"
	"fascinated.cc/monitor/agent/internal/disk"
	"fascinated.cc/monitor/agent/internal/docker"
	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/iostats"
	"fascinated.cc/monitor/agent/internal/lhm"
	"fascinated.cc/monitor/agent/internal/network"
	"fascinated.cc/monitor/agent/internal/zfs"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

type windowsTickState struct {
	lastAt time.Time

	cpuBefore      cpu.TimesStat
	perCPUBefore   []cpu.TimesStat
	netBefore      []network.Counter
	beforeIO       map[string]disk.IOCounters
	countersBefore counters.SystemCounters
	countersPerSec bool
	beforeZFS      map[string]zfs.PoolIO
	arcBefore      zfs.ArcSnapshot
	hasArcBefore   bool
}

type windowsSamplerExtra struct {
	state       windowsTickState
	initialized bool

	mounts      []disk.Mount
	poolStatus  zfs.PoolStatusSnapshot
	poolIORates map[string]zfs.PoolIORates
}

func (s *Sampler) windowsExtra() *windowsSamplerExtra {
	if s.platform == nil {
		s.platform = &windowsSamplerExtra{
			poolIORates: map[string]zfs.PoolIORates{},
		}
	}
	return s.platform.(*windowsSamplerExtra)
}

func (s *Sampler) tick() error {
	start := time.Now()
	extra := s.windowsExtra()

	cpuAgg, err := cpu.Times(false)
	if err != nil || len(cpuAgg) == 0 {
		return err
	}
	perCPU, _ := cpu.Times(true)

	var (
		netBefore, netAfter []network.Counter
		afterIO             map[string]disk.IOCounters
		afterZFS            map[string]zfs.PoolIO
		netErr              error
		ioErr               error
	)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		netAfter, netErr = network.ReadCounters()
	}()
	go func() {
		defer wg.Done()
		afterIO, ioErr = disk.ReadIOCounters()
	}()
	wg.Wait()
	if netErr != nil {
		return netErr
	}
	if ioErr != nil {
		afterIO = map[string]disk.IOCounters{}
	}

	countersAfter, countersPerSecond, _ := counters.ReadSystemCounters()
	if s.opts.HasZFS {
		afterZFS = zfs.ReadPoolIOSnapshots()
	}

	now := time.Now()
	if !extra.initialized {
		cpupkg.BeginIowaitSample()
		cpupkg.BeginCPUPowerSample()
		netBefore = netAfter
		extra.state = windowsTickState{
			lastAt:         now,
			cpuBefore:      cpuAgg[0],
			perCPUBefore:   perCPU,
			netBefore:      netBefore,
			beforeIO:       afterIO,
			countersBefore: countersAfter,
			countersPerSec: countersPerSecond,
			beforeZFS:      afterZFS,
		}
		if s.opts.HasZFS {
			extra.state.arcBefore, extra.state.hasArcBefore = zfs.ReadArcSnapshot()
		}
		extra.initialized = true
		return nil
	}

	elapsed := now.Sub(extra.state.lastAt)
	prev := extra.state
	iowait := cpupkg.EndIowaitSample()
	cpupkg.BeginIowaitSample()

	result := s.Snapshot()
	if !s.Ready() {
		result = Result{
			InterfaceMetrics: []ingest.InterfaceMetrics{},
			DiskMetrics:      []ingest.DiskMetric{},
		}
	}

	breakdown := cpupkg.ComputeCPUMetrics(prev.cpuBefore, cpuAgg[0])
	metrics := ingest.ServerMetrics{
		CPUUsage:         breakdown.Total,
		CPUUserPercent:   breakdown.User,
		CPUSystemPercent: breakdown.System,
		CPUIowaitPercent: iowait,
		CPUStealPercent:  breakdown.Steal,
	}
	metrics.CPUCoreMetrics = coreMetricsFromGopsutil(cpupkg.ComputePerCoreCPUMetrics(prev.perCPUBefore, perCPU))
	if watts, ok := cpupkg.EndCPUPowerSample(); ok {
		metrics.CPUPowerWatts = watts
	}
	cpupkg.BeginCPUPowerSample()

	applyGopsutilMemory(&metrics)
	metrics.ProcessCount, metrics.RunningProcesses = counters.ProcessStats()

	if prev.countersPerSec {
		metrics.ContextSwitchesPerSecond = int64(prev.countersBefore.ContextSwitches)
		metrics.InterruptsPerSecond = int64(prev.countersBefore.Interrupts)
	} else {
		metrics.ContextSwitchesPerSecond = iostats.PerSecond(delta.Uint64(countersAfter.ContextSwitches, prev.countersBefore.ContextSwitches), elapsed)
		metrics.InterruptsPerSecond = iostats.PerSecond(delta.Uint64(countersAfter.Interrupts, prev.countersBefore.Interrupts), elapsed)
	}

	if len(extra.mounts) == 0 {
		mounts, err := disk.ListMounts()
		if err != nil {
			return err
		}
		extra.mounts = mounts
	}

	extra.poolIORates = computePoolIORates(prev.beforeZFS, afterZFS, elapsed)
	result.InterfaceMetrics = network.ComputeMetrics(prev.netBefore, netAfter, elapsed)
	result.DiskMetrics = disk.BuildFromSamples(s.opts.HasZFS, extra.mounts, prev.beforeIO, afterIO, prev.beforeZFS, afterZFS, elapsed)

	if s.opts.HasZFS {
		if arcAfter, ok := zfs.ReadArcSnapshot(); ok && prev.hasArcBefore {
			result.ZfsArcMetrics = zfs.ComputeArcMetrics(prev.arcBefore, arcAfter, elapsed)
		}
	}

	result.ServerMetrics = metrics
	s.setResult(result, true)

	extra.state = windowsTickState{
		lastAt:         now,
		cpuBefore:      cpuAgg[0],
		perCPUBefore:   perCPU,
		netBefore:      netAfter,
		beforeIO:       afterIO,
		countersBefore: countersAfter,
		countersPerSec: countersPerSecond,
		beforeZFS:      afterZFS,
	}
	if s.opts.HasZFS {
		extra.state.arcBefore, extra.state.hasArcBefore = zfs.ReadArcSnapshot()
	}

	profilePhase("tick_total", start)
	return nil
}

func (s *Sampler) refreshSlow() error {
	start := time.Now()
	result := s.Snapshot()
	extra := s.windowsExtra()

	if s.opts.HasZFS {
		extra.poolStatus = zfs.ReadPoolStatus()
		result.ZfsPoolMetrics = zfs.CollectPoolMetrics(extra.poolIORates, extra.poolStatus)
	}

	if s.opts.EnableDocker {
		result.DockerContainers = docker.CollectContainerMetrics()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	snap, err := lhm.GetServerMetrics(ctx)
	cancel()
	if err != nil {
		warnLHMSlow(err)
	} else {
		applyLHMSlow(&result, snap, s.opts.EnableGPU)
	}

	mounts, err := disk.ListMounts()
	if err == nil {
		extra.mounts = mounts
		updateDiskUsageWindows(&result, mounts)
	}

	s.setResult(result, s.Ready())
	profilePhase("refresh_slow", start)
	return nil
}

func applyGopsutilMemory(metrics *ingest.ServerMetrics) {
	vm, err := mem.VirtualMemory()
	if err != nil {
		return
	}
	metrics.MemoryUsage = float64(vm.Used)
	metrics.MemoryAvailable = float64(vm.Available)
	metrics.MemoryTotal = float64(vm.Total)
	metrics.MemoryBuffers = int64(vm.Buffers)
	metrics.MemoryCached = int64(vm.Cached)
	metrics.SwapTotal = int64(vm.SwapTotal)
	metrics.SwapUsed = int64(vm.SwapTotal - vm.SwapFree)
}

func applyLHMSlow(result *Result, snap lhm.ServerSnapshot, enableGPU bool) {
	if len(snap.Temperatures) > 0 {
		result.ServerMetrics.TemperatureMetrics = snap.Temperatures
	}
	if enableGPU && len(snap.GPUs) > 0 {
		result.GPUMetrics = snap.GPUs
	}
	if snap.Memory.Complete() {
		result.ServerMetrics.MemoryUsage = float64(*snap.Memory.Used)
		result.ServerMetrics.MemoryAvailable = float64(*snap.Memory.Available)
		result.ServerMetrics.MemoryTotal = float64(*snap.Memory.Total)
	}
	if snap.CPUPowerWatts != nil && *snap.CPUPowerWatts > 0 {
		result.ServerMetrics.CPUPowerWatts = *snap.CPUPowerWatts
	}
	if len(snap.Cores) > 0 {
		result.ServerMetrics.CPUCoreMetrics = snap.Cores
	}
}

func updateDiskUsageWindows(result *Result, mounts []disk.Mount) {
	usageByName := make(map[string]disk.Mount, len(mounts))
	for _, m := range mounts {
		usageByName[m.Name] = m
	}
	for i := range result.DiskMetrics {
		if m, ok := usageByName[result.DiskMetrics[i].DiskName]; ok {
			result.DiskMetrics[i].UsedBytes = int64(m.UsedBytes)
			result.DiskMetrics[i].TotalBytes = int64(m.TotalBytes)
			result.DiskMetrics[i].InodeUsed = int64(m.InodeUsed)
			result.DiskMetrics[i].InodeTotal = int64(m.InodeTotal)
		}
	}
}

var (
	lastLHMWarn   time.Time
	lhmWarnWindow = time.Minute
)

func warnLHMSlow(err error) {
	now := time.Now()
	if now.Sub(lastLHMWarn) < lhmWarnWindow {
		return
	}
	lastLHMWarn = now
	slog.Warn("lhm helper unavailable, keeping last slow metrics", "err", err)
}
