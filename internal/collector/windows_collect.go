//go:build windows

package collector

import (
	"context"
	"log/slog"
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
	"fascinated.cc/monitor/agent/internal/sample"
	"fascinated.cc/monitor/agent/internal/thermal"
	"fascinated.cc/monitor/agent/internal/zfs"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

func collect(opts Options) (Result, error) {
	result := Result{
		InterfaceMetrics: []ingest.InterfaceMetrics{},
		DiskMetrics:      []ingest.DiskMetric{},
		ZfsPoolMetrics:   []ingest.ZfsPoolMetric{},
	}

	var dockerCh chan []ingest.DockerContainerMetric
	if opts.EnableDocker {
		dockerCh = make(chan []ingest.DockerContainerMetric, 1)
		go func() {
			dockerCh <- docker.CollectContainerMetrics()
		}()
	}

	cpuBefore, err := cpu.Times(false)
	if err != nil || len(cpuBefore) == 0 {
		return result, err
	}
	perCPUBefore, _ := cpu.Times(true)

	netBefore, err := network.ReadCounters()
	if err != nil {
		return result, err
	}

	mounts, err := disk.ListMounts()
	if err != nil {
		return result, err
	}

	beforeIO, err := disk.ReadIOCounters()
	if err != nil {
		beforeIO = map[string]disk.IOCounters{}
	}

	countersBefore, countersPerSecond, _ := counters.ReadSystemCounters()

	var (
		beforeZFS    map[string]zfs.PoolIO
		zfsFuture    func() map[string]zfs.PoolIORates
		poolStatus   zfs.PoolStatusSnapshot
		arcBefore    zfs.ArcSnapshot
		hasArcBefore bool
	)

	if opts.HasZFS {
		poolStatus = zfs.ReadPoolStatus()
		arcBefore, hasArcBefore = zfs.ReadArcSnapshot()
	}
	if opts.HasZFS {
		beforeZFS = zfs.ReadPoolIOSnapshots()
		zfsFuture = zfs.StartPoolIostatSample()
	}

	cpupkg.BeginIowaitSample()
	cpupkg.BeginCPUPowerSample()

	sampleStart := time.Now()
	time.Sleep(sample.Interval)
	elapsed := time.Since(sampleStart)

	cpuAfter, err := cpu.Times(false)
	if err != nil || len(cpuAfter) == 0 {
		return result, err
	}
	perCPUAfter, _ := cpu.Times(true)
	iowait := cpupkg.EndIowaitSample()

	netAfter, err := network.ReadCounters()
	if err != nil {
		return result, err
	}

	afterIO, err := disk.ReadIOCounters()
	if err != nil {
		afterIO = map[string]disk.IOCounters{}
	}

	var afterZFS map[string]zfs.PoolIO
	var zfsRates map[string]zfs.PoolIORates
	if opts.HasZFS {
		afterZFS = zfs.ReadPoolIOSnapshots()
		if zfsFuture != nil {
			zfsRates = zfsFuture()
		}
	}

	metrics, gpuMetrics := buildWindowsServerMetrics(
		cpuBefore[0], cpuAfter[0],
		perCPUBefore, perCPUAfter,
		iowait,
	)
	if opts.EnableGPU {
		result.GPUMetrics = gpuMetrics
	}

	metrics.ProcessCount, metrics.RunningProcesses = counters.ProcessStats()

	if countersPerSecond {
		metrics.ContextSwitchesPerSecond = int64(countersBefore.ContextSwitches)
		metrics.InterruptsPerSecond = int64(countersBefore.Interrupts)
	} else {
		countersAfter, _, _ := counters.ReadSystemCounters()
		metrics.ContextSwitchesPerSecond = iostats.PerSecond(delta.Uint64(countersAfter.ContextSwitches, countersBefore.ContextSwitches), elapsed)
		metrics.InterruptsPerSecond = iostats.PerSecond(delta.Uint64(countersAfter.Interrupts, countersBefore.Interrupts), elapsed)
	}

	if opts.HasZFS {
		if arcAfter, ok := zfs.ReadArcSnapshot(); ok && hasArcBefore {
			result.ZfsArcMetrics = zfs.ComputeArcMetrics(arcBefore, arcAfter, elapsed)
		}
		if zfsRates != nil {
			result.ZfsPoolMetrics = zfs.CollectPoolMetrics(zfsRates, poolStatus)
		}
	}

	if dockerCh != nil {
		result.DockerContainers = <-dockerCh
	}

	result.ServerMetrics = metrics
	result.InterfaceMetrics = network.ComputeMetrics(netBefore, netAfter, elapsed)
	result.DiskMetrics = disk.BuildFromSamples(opts.HasZFS, mounts, beforeIO, afterIO, beforeZFS, afterZFS, zfsRates, elapsed)
	return result, nil
}

func buildWindowsServerMetrics(
	cpuBefore, cpuAfter cpu.TimesStat,
	perCPUBefore, perCPUAfter []cpu.TimesStat,
	iowait float64,
) (ingest.ServerMetrics, []ingest.GPUMetric) {
	breakdown := cpupkg.ComputeCPUMetrics(cpuBefore, cpuAfter)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	snap, err := lhm.GetServerMetrics(ctx)
	if err != nil {
		warnLHMFallback(err)
		return serverMetricsFromGopsutil(cpuBefore, cpuAfter, perCPUBefore, perCPUAfter, iowait), nil
	}

	metrics := serverMetricsFromLHM(snap)
	gpuMetrics := snap.GPUs
	metrics.CPUUserPercent = breakdown.User
	metrics.CPUSystemPercent = breakdown.System
	metrics.CPUStealPercent = breakdown.Steal
	metrics.CPUIowaitPercent = iowait

	applyGopsutilMemoryExtras(&metrics, snap.Memory.Complete())
	if snap.CPUTotalPercent == nil && breakdown.Total > 0 {
		metrics.CPUUsage = breakdown.Total
	}
	if len(snap.Cores) == 0 {
		metrics.CPUCoreMetrics = coreMetricsFromGopsutil(cpupkg.ComputePerCoreCPUMetrics(perCPUBefore, perCPUAfter))
	}
	if len(snap.Temperatures) == 0 {
		metrics.TemperatureMetrics = temperatureMetricsFromGopsutil(thermal.ReadTemperatures())
	}
	if metrics.CPUPowerWatts <= 0 {
		if watts, ok := cpupkg.EndCPUPowerSample(); ok {
			metrics.CPUPowerWatts = watts
		}
	}

	return metrics, gpuMetrics
}

func applyGopsutilMemoryExtras(metrics *ingest.ServerMetrics, memoryComplete bool) {
	vm, err := mem.VirtualMemory()
	if err != nil {
		return
	}
	if !memoryComplete {
		metrics.MemoryUsage = float64(vm.Used)
		metrics.MemoryAvailable = float64(vm.Available)
		metrics.MemoryTotal = float64(vm.Total)
	}
	metrics.MemoryBuffers = int64(vm.Buffers)
	metrics.MemoryCached = int64(vm.Cached)
	metrics.SwapTotal = int64(vm.SwapTotal)
	metrics.SwapUsed = int64(vm.SwapTotal - vm.SwapFree)
}

var (
	lastLHMWarn   time.Time
	lhmWarnWindow = time.Minute
)

func warnLHMFallback(err error) {
	now := time.Now()
	if now.Sub(lastLHMWarn) < lhmWarnWindow {
		return
	}
	lastLHMWarn = now
	slog.Warn("lhm helper unavailable, using gopsutil fallback", "err", err)
}
