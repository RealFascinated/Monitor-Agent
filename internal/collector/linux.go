//go:build linux

package collector

import (
	"time"

	"fascinated.cc/monitor/agent/internal/cpu"
	"fascinated.cc/monitor/agent/internal/delta"
	"fascinated.cc/monitor/agent/internal/disk"
	"fascinated.cc/monitor/agent/internal/docker"
	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/iostats"
	"fascinated.cc/monitor/agent/internal/linux"
	"fascinated.cc/monitor/agent/internal/loadavg"
	"fascinated.cc/monitor/agent/internal/memory"
	"fascinated.cc/monitor/agent/internal/network"
	"fascinated.cc/monitor/agent/internal/sample"
	"fascinated.cc/monitor/agent/internal/thermal"
	"fascinated.cc/monitor/agent/internal/zfs"
)

func collect(opts Options) (Result, error) {
	result := Result{
		InterfaceMetrics: []ingest.InterfaceMetrics{},
		DiskMetrics:      []ingest.DiskMetric{},
	}

	var dockerCh chan []ingest.DockerContainerMetric
	if opts.EnableDocker {
		dockerCh = make(chan []ingest.DockerContainerMetric, 1)
		go func() {
			dockerCh <- docker.CollectContainerMetrics()
		}()
	}

	cgroup := linux.Dir()
	cgroupCPUBefore, hasCgroupCPU := cpu.ReadCgroupUsageUsec(cgroup)

	mounts, err := disk.ListMounts()
	if err != nil {
		return result, err
	}

	var (
		poolStatus      zfs.PoolStatusSnapshot
		zfsIOBefore     map[string]zfs.PoolIO
		zfsIostatFuture func() map[string]zfs.PoolIORates
		arcBefore       zfs.ArcSnapshot
		hasArcBefore    bool
	)

	if opts.HasZFS {
		poolStatus = zfs.ReadPoolStatus()
		zfsIOBefore = zfs.ReadPoolIOSnapshots()
		zfsIostatFuture = zfs.StartPoolIostatSample()
		arcBefore, hasArcBefore = zfs.ReadArcSnapshot()
	}

	diskstatsBefore := linux.ReadDiskstats()
	cgroupIOBefore := linux.ReadIOStats()
	procBefore := linux.ReadProcStat()
	powerBefore, powerMaxBefore, hasPowerBefore := cpu.ReadPackageEnergyMicrojoules()

	netBefore, err := network.ReadCounters()
	if err != nil {
		return result, err
	}

	sampleStart := time.Now()
	time.Sleep(sample.Interval)
	elapsed := time.Since(sampleStart)

	var metrics ingest.ServerMetrics
	procAfter := linux.ReadProcStat()
	avg := loadavg.Read()
	memSnap := memory.Read()
	if procBefore.HasCPU && procAfter.HasCPU {
		usage, user, system, iowait, steal := linux.ComputeCPUFromProcStat(procBefore.CPU, procAfter.CPU)
		metrics.CPUUsage = usage
		metrics.CPUUserPercent = user
		metrics.CPUSystemPercent = system
		metrics.CPUIowaitPercent = iowait
		metrics.CPUStealPercent = steal
	}
	if len(procBefore.PerCPU) > 0 && len(procAfter.PerCPU) > 0 {
		metrics.CPUCoreMetrics = coreMetricsFromLinux(cpu.ComputePerCoreCPU(procBefore.PerCPU, procAfter.PerCPU))
	}
	metrics.TemperatureMetrics = temperatureMetricsFromLinux(thermal.ReadTemperatures())
	if hasPowerBefore {
		if powerAfter, powerMaxAfter, ok := cpu.ReadPackageEnergyMicrojoules(); ok {
			maxEnergy := powerMaxAfter
			if maxEnergy == 0 {
				maxEnergy = powerMaxBefore
			}
			if watts, ok := cpu.ComputePowerWatts(powerBefore, powerAfter, maxEnergy, elapsed); ok {
				metrics.CPUPowerWatts = watts
			}
		}
	}
	if hasCgroupCPU {
		if afterUsage, ok := cpu.ReadCgroupUsageUsec(cgroup); ok {
			if usage, ok := cpu.CgroupUsagePercent(cgroupCPUBefore, afterUsage, cgroup, elapsed); ok {
				metrics.CPUUsage = usage
			}
		}
	}

	metrics.ContextSwitchesPerSecond = iostats.PerSecond(delta.Uint64(procAfter.ContextSwitches, procBefore.ContextSwitches), elapsed)
	metrics.InterruptsPerSecond = iostats.PerSecond(delta.Uint64(procAfter.Interrupts, procBefore.Interrupts), elapsed)

	metrics.Load1 = avg.Load1
	metrics.Load5 = avg.Load5
	metrics.Load15 = avg.Load15
	metrics.ProcessCount = avg.ProcessCount
	metrics.RunningProcesses = avg.RunningProcesses

	metrics.MemoryUsage = memSnap.Usage
	metrics.MemoryTotal = memSnap.Total
	metrics.MemoryAvailable = memSnap.Available
	metrics.MemoryBuffers = memSnap.Extras.Buffers
	metrics.MemoryCached = memSnap.Extras.Cached
	metrics.SwapUsed = memSnap.Extras.SwapUsed
	metrics.SwapTotal = memSnap.Extras.SwapTotal

	netAfter, err := network.ReadCounters()
	if err != nil {
		return result, err
	}
	result.InterfaceMetrics = network.ComputeMetrics(netBefore, netAfter, elapsed)

	diskstatsAfter := linux.ReadDiskstats()
	cgroupIOAfter := linux.ReadIOStats()

	var zfsIOAfter map[string]zfs.PoolIO
	var zfsRates map[string]zfs.PoolIORates
	if opts.HasZFS {
		zfsIOAfter = zfs.ReadPoolIOSnapshots()
		if zfsIostatFuture != nil {
			zfsRates = zfsIostatFuture()
		}
	}

	var cgroupDevice string
	if len(cgroupIOBefore) > 0 {
		for majmin := range cgroupIOBefore {
			cgroupDevice = linux.ResolveBlockDeviceName(majmin)
			break
		}
	}

	vdevMap := poolStatus.VdevMap
	if vdevMap == nil {
		vdevMap = map[string][]string{}
	}

	result.DiskMetrics = disk.BuildLinuxMetrics(
		mounts,
		diskstatsBefore,
		diskstatsAfter,
		cgroupIOBefore,
		cgroupIOAfter,
		zfsIOBefore,
		zfsIOAfter,
		zfsRates,
		vdevMap,
		cgroupDevice,
		opts.HasZFS,
		elapsed,
	)

	if opts.HasZFS {
		if arcAfter, ok := zfs.ReadArcSnapshot(); ok && hasArcBefore {
			result.ZfsArcMetrics = zfs.ComputeArcMetrics(arcBefore, arcAfter, elapsed)
		}
		result.ZfsPoolMetrics = zfs.CollectPoolMetrics(zfsRates, poolStatus)
	}

	if dockerCh != nil {
		result.DockerContainers = <-dockerCh
	}

	result.ServerMetrics = metrics
	return result, nil
}
