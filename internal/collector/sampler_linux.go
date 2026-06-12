//go:build linux

package collector

import (
	"sync"
	"time"

	"fascinated.cc/monitor/agent/internal/connections"
	"fascinated.cc/monitor/agent/internal/cpu"
	"fascinated.cc/monitor/agent/internal/delta"
	"fascinated.cc/monitor/agent/internal/disk"
	"fascinated.cc/monitor/agent/internal/docker"
	gpupkg "fascinated.cc/monitor/agent/internal/gpu"
	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/iostats"
	"fascinated.cc/monitor/agent/internal/linux"
	"fascinated.cc/monitor/agent/internal/loadavg"
	"fascinated.cc/monitor/agent/internal/memory"
	"fascinated.cc/monitor/agent/internal/network"
	"fascinated.cc/monitor/agent/internal/thermal"
	"fascinated.cc/monitor/agent/internal/zfs"
)

type linuxTickState struct {
	lastAt time.Time

	cgroupCPUBefore uint64
	hasCgroupCPU    bool

	diskstatsBefore map[string]linux.DiskstatsEntry
	cgroupIOBefore  map[string]linux.CgroupIOEntry
	procBefore      linux.ProcStatSnapshot
	powerBefore     uint64
	powerMaxBefore  uint64
	hasPowerBefore  bool
	netBefore       []network.Counter
	zfsIOBefore     map[string]zfs.PoolIO
	arcBefore       zfs.ArcSnapshot
	hasArcBefore    bool
}

type linuxSamplerExtra struct {
	state       linuxTickState
	initialized bool

	cgroup       string
	mounts       []disk.Mount
	poolStatus   zfs.PoolStatusSnapshot
	poolIORates  map[string]zfs.PoolIORates
	cgroupDevice string
}

func (s *Sampler) linuxExtra() *linuxSamplerExtra {
	if s.platform == nil {
		s.platform = &linuxSamplerExtra{
			poolIORates: map[string]zfs.PoolIORates{},
			cgroup:      linux.Dir(),
		}
	}
	return s.platform.(*linuxSamplerExtra)
}

func (s *Sampler) tick() error {
	start := time.Now()
	extra := s.linuxExtra()

	var (
		procAfter      linux.ProcStatSnapshot
		netAfter       []network.Counter
		diskstatsAfter map[string]linux.DiskstatsEntry
		cgroupIOAfter  map[string]linux.CgroupIOEntry
		zfsIOAfter     map[string]zfs.PoolIO
		netErr         error
	)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		procAfter = linux.ReadProcStat()
	}()
	go func() {
		defer wg.Done()
		netAfter, netErr = network.ReadCounters()
	}()
	go func() {
		defer wg.Done()
		diskstatsAfter = linux.ReadDiskstats()
	}()
	wg.Wait()
	profilePhase("parallel_reads", start)

	if netErr != nil {
		return netErr
	}

	cgroupIOAfter = linux.ReadIOStats()
	powerAfter, powerMaxAfter, hasPowerAfter := cpu.ReadPackageEnergyMicrojoules()
	if s.opts.HasZFS {
		zfsIOAfter = zfs.ReadPoolIOSnapshots()
	}

	now := time.Now()
	if !extra.initialized {
		extra.state = linuxTickState{
			lastAt:          now,
			cgroupCPUBefore: readCgroupCPU(extra.cgroup),
			hasCgroupCPU:    hasCgroupCPU(extra.cgroup),
			diskstatsBefore: diskstatsAfter,
			cgroupIOBefore:  cgroupIOAfter,
			procBefore:      procAfter,
			powerBefore:     powerAfter,
			powerMaxBefore:  powerMaxAfter,
			hasPowerBefore:  hasPowerAfter,
			netBefore:       netAfter,
			zfsIOBefore:     zfsIOAfter,
		}
		if s.opts.HasZFS {
			extra.state.arcBefore, extra.state.hasArcBefore = zfs.ReadArcSnapshot()
		}
		extra.initialized = true
		return nil
	}

	elapsed := now.Sub(extra.state.lastAt)
	prev := extra.state

	result := s.Snapshot()
	if !s.Ready() {
		result = Result{
			InterfaceMetrics: []ingest.InterfaceMetrics{},
			DiskMetrics:      []ingest.DiskMetric{},
		}
	}

	var metrics ingest.ServerMetrics
	if prev.procBefore.HasCPU && procAfter.HasCPU {
		usage, user, system, iowait, steal := linux.ComputeCPUFromProcStat(prev.procBefore.CPU, procAfter.CPU)
		metrics.CPUUsage = usage
		metrics.CPUUserPercent = user
		metrics.CPUSystemPercent = system
		metrics.CPUIowaitPercent = iowait
		metrics.CPUStealPercent = steal
	}
	if len(prev.procBefore.PerCPU) > 0 && len(procAfter.PerCPU) > 0 {
		metrics.CPUCoreMetrics = coreMetricsFromLinux(cpu.ComputePerCoreCPU(prev.procBefore.PerCPU, procAfter.PerCPU))
	}
	metrics.TemperatureMetrics = temperatureMetricsFromLinux(thermal.ReadTemperatures())
	if prev.hasPowerBefore && hasPowerAfter {
		maxEnergy := powerMaxAfter
		if maxEnergy == 0 {
			maxEnergy = prev.powerMaxBefore
		}
		if watts, ok := cpu.ComputePowerWatts(prev.powerBefore, powerAfter, maxEnergy, elapsed); ok {
			metrics.CPUPowerWatts = watts
		}
	}
	if prev.hasCgroupCPU {
		if afterUsage, ok := cpu.ReadCgroupUsageUsec(extra.cgroup); ok {
			if usage, ok := cpu.CgroupUsagePercent(prev.cgroupCPUBefore, afterUsage, extra.cgroup, elapsed); ok {
				metrics.CPUUsage = usage
			}
		}
	}

	metrics.ContextSwitchesPerSecond = iostats.PerSecond(delta.Uint64(procAfter.ContextSwitches, prev.procBefore.ContextSwitches), elapsed)
	metrics.InterruptsPerSecond = iostats.PerSecond(delta.Uint64(procAfter.Interrupts, prev.procBefore.Interrupts), elapsed)

	avg := loadavg.Read()
	metrics.Load1 = avg.Load1
	metrics.Load5 = avg.Load5
	metrics.Load15 = avg.Load15
	metrics.ProcessCount = avg.ProcessCount
	metrics.RunningProcesses = avg.RunningProcesses

	memSnap := memory.Read()
	metrics.MemoryUsage = memSnap.Usage
	metrics.MemoryTotal = memSnap.Total
	metrics.MemoryAvailable = memSnap.Available
	metrics.MemoryBuffers = memSnap.Extras.Buffers
	metrics.MemoryCached = memSnap.Extras.Cached
	metrics.SwapUsed = memSnap.Extras.SwapUsed
	metrics.SwapTotal = memSnap.Extras.SwapTotal

	result.InterfaceMetrics = network.ComputeMetrics(prev.netBefore, netAfter, elapsed)

	if len(extra.mounts) == 0 {
		mounts, err := disk.ListMounts()
		if err != nil {
			return err
		}
		extra.mounts = mounts
	}

	vdevMap := extra.poolStatus.VdevMap
	if vdevMap == nil {
		vdevMap = map[string][]string{}
	}

	extra.poolIORates = computePoolIORates(prev.zfsIOBefore, zfsIOAfter, elapsed)
	result.DiskMetrics = disk.BuildLinuxMetrics(
		extra.mounts,
		prev.diskstatsBefore,
		diskstatsAfter,
		prev.cgroupIOBefore,
		cgroupIOAfter,
		prev.zfsIOBefore,
		zfsIOAfter,
		vdevMap,
		extra.cgroupDevice,
		s.opts.HasZFS,
		elapsed,
	)

	if s.opts.HasZFS {
		if arcAfter, ok := zfs.ReadArcSnapshot(); ok && prev.hasArcBefore {
			result.ZfsArcMetrics = zfs.ComputeArcMetrics(prev.arcBefore, arcAfter, elapsed)
		}
	}

	result.TCPConnectionMetrics = connections.Read().ToIngest()
	result.ServerMetrics = metrics

	s.setResult(result, true)

	extra.state = linuxTickState{
		lastAt:          now,
		cgroupCPUBefore: readCgroupCPU(extra.cgroup),
		hasCgroupCPU:    hasCgroupCPU(extra.cgroup),
		diskstatsBefore: diskstatsAfter,
		cgroupIOBefore:  cgroupIOAfter,
		procBefore:      procAfter,
		powerBefore:     powerAfter,
		powerMaxBefore:  powerMaxAfter,
		hasPowerBefore:  hasPowerAfter,
		netBefore:       netAfter,
		zfsIOBefore:     zfsIOAfter,
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

	if s.opts.HasZFS {
		extra := s.linuxExtra()
		extra.poolStatus = zfs.ReadPoolStatus()
		extra.cgroupDevice = resolveCgroupDevice()
		result.ZfsPoolMetrics = zfs.CollectPoolMetrics(extra.poolIORates, extra.poolStatus)
	}

	if s.opts.EnableDocker {
		result.DockerContainers = docker.CollectContainerMetrics()
	}
	if s.opts.EnableGPU {
		result.GPUMetrics = gpupkg.Collect()
	}

	mounts, err := disk.ListMounts()
	if err == nil {
		s.linuxExtra().mounts = mounts
		updateDiskUsage(&result, mounts)
	}

	s.setResult(result, s.Ready())
	profilePhase("refresh_slow", start)
	return nil
}

func readCgroupCPU(cgroup string) uint64 {
	usage, _ := cpu.ReadCgroupUsageUsec(cgroup)
	return usage
}

func hasCgroupCPU(cgroup string) bool {
	_, ok := cpu.ReadCgroupUsageUsec(cgroup)
	return ok
}

func resolveCgroupDevice() string {
	cgroupIO := linux.ReadIOStats()
	for majmin := range cgroupIO {
		return linux.ResolveBlockDeviceName(majmin)
	}
	return ""
}

func updateDiskUsage(result *Result, mounts []disk.Mount) {
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
