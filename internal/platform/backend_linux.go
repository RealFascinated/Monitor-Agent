//go:build linux

package platform

import (
	"sync"
	"time"

	"fascinated.cc/monitor/agent/internal/connections"
	"fascinated.cc/monitor/agent/internal/cpu"
	"fascinated.cc/monitor/agent/internal/disk"
	"fascinated.cc/monitor/agent/internal/docker"
	gpupkg "fascinated.cc/monitor/agent/internal/gpu"
	"fascinated.cc/monitor/agent/internal/ingest"
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

type linuxBackend struct {
	opts Options

	state       linuxTickState
	initialized bool

	cgroup       string
	mounts       []disk.Mount
	poolStatus   zfs.PoolStatusSnapshot
	poolIORates  map[string]zfs.PoolIORates
	cgroupDevice string
}

func newLinuxBackend(opts Options) *linuxBackend {
	return &linuxBackend{
		opts:        opts,
		poolIORates: map[string]zfs.PoolIORates{},
		cgroup:      linux.Dir(),
	}
}

func (b *linuxBackend) Tick(ready bool) (TickUpdate, error) {
	start := time.Now()

	var (
		procAfter      linux.ProcStatSnapshot
		netAfter       []network.Counter
		diskstatsAfter map[string]linux.DiskstatsEntry
		cgroupIOAfter  map[string]linux.CgroupIOEntry
		zfsIOAfter     map[string]zfs.PoolIO
		clockMHz       float64
		netErr         error
	)

	var wg sync.WaitGroup
	wg.Add(4)
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
	go func() {
		defer wg.Done()
		if mhz, err := cpu.GetClockSpeedMHz(); err == nil {
			clockMHz = mhz
		}
	}()
	wg.Wait()
	profilePhase("parallel_reads", start)

	if netErr != nil {
		return TickUpdate{}, netErr
	}

	cgroupIOAfter = linux.ReadIOStats()
	powerAfter, powerMaxAfter, hasPowerAfter := cpu.ReadPackageEnergyMicrojoules()
	if b.opts.HasZFS {
		zfsIOAfter = zfs.ReadPoolIOSnapshots()
	}

	now := time.Now()
	if !b.initialized {
		b.state = linuxTickState{
			lastAt:          now,
			cgroupCPUBefore: readCgroupCPU(b.cgroup),
			hasCgroupCPU:    hasCgroupCPU(b.cgroup),
			diskstatsBefore: diskstatsAfter,
			cgroupIOBefore:  cgroupIOAfter,
			procBefore:      procAfter,
			powerBefore:     powerAfter,
			powerMaxBefore:  powerMaxAfter,
			hasPowerBefore:  hasPowerAfter,
			netBefore:       netAfter,
			zfsIOBefore:     zfsIOAfter,
		}
		if b.opts.HasZFS {
			b.state.arcBefore, b.state.hasArcBefore = zfs.ReadArcSnapshot()
		}
		b.initialized = true
		return TickUpdate{Skip: true}, nil
	}

	elapsed := now.Sub(b.state.lastAt)
	prev := b.state

	update := TickUpdate{Ready: true}
	if !ready {
		update.InterfaceMetrics = []ingest.InterfaceMetrics{}
		update.DiskMetrics = []ingest.DiskMetric{}
	}

	metrics := cpu.ComputeLinuxTick(cpu.LinuxTickInput{
		PrevProc:       prev.procBefore,
		CurrProc:       procAfter,
		PrevPower:      prev.powerBefore,
		CurrPower:      powerAfter,
		PrevPowerMax:   prev.powerMaxBefore,
		CurrPowerMax:   powerMaxAfter,
		HasPowerBefore: prev.hasPowerBefore,
		HasPowerAfter:  hasPowerAfter,
		PrevCgroupCPU:  prev.cgroupCPUBefore,
		HasCgroupCPU:   prev.hasCgroupCPU,
		Cgroup:         b.cgroup,
		Elapsed:        elapsed,
	})
	metrics.TemperatureMetrics = thermal.ToIngest(thermal.ReadTemperatures())

	avg := loadavg.Read()
	metrics.Load1 = avg.Load1
	metrics.Load5 = avg.Load5
	metrics.Load15 = avg.Load15
	metrics.ProcessCount = avg.ProcessCount
	metrics.RunningProcesses = avg.RunningProcesses

	memory.ApplyTo(&metrics, memory.Read())

	update.InterfaceMetrics = network.ComputeMetrics(prev.netBefore, netAfter, elapsed)

	if len(b.mounts) == 0 {
		mounts, err := disk.ListMounts()
		if err != nil {
			return TickUpdate{}, err
		}
		b.mounts = mounts
	}

	vdevMap := b.poolStatus.VdevMap
	if vdevMap == nil {
		vdevMap = map[string][]string{}
	}

	b.poolIORates = zfs.ComputePoolIORates(prev.zfsIOBefore, zfsIOAfter, elapsed)
	update.DiskMetrics = disk.BuildLinuxMetrics(
		b.mounts,
		prev.diskstatsBefore,
		diskstatsAfter,
		prev.cgroupIOBefore,
		cgroupIOAfter,
		prev.zfsIOBefore,
		zfsIOAfter,
		vdevMap,
		b.cgroupDevice,
		b.opts.HasZFS,
		elapsed,
	)

	if b.opts.HasZFS {
		if arcAfter, ok := zfs.ReadArcSnapshot(); ok && prev.hasArcBefore {
			update.ZfsArcMetrics = zfs.ComputeArcMetrics(prev.arcBefore, arcAfter, elapsed)
		}
	}

	update.TCPConnectionMetrics = connections.Read().ToIngest()
	update.ServerMetrics = metrics
	update.ClockMHz = clockMHz

	b.state = linuxTickState{
		lastAt:          now,
		cgroupCPUBefore: readCgroupCPU(b.cgroup),
		hasCgroupCPU:    hasCgroupCPU(b.cgroup),
		diskstatsBefore: diskstatsAfter,
		cgroupIOBefore:  cgroupIOAfter,
		procBefore:      procAfter,
		powerBefore:     powerAfter,
		powerMaxBefore:  powerMaxAfter,
		hasPowerBefore:  hasPowerAfter,
		netBefore:       netAfter,
		zfsIOBefore:     zfsIOAfter,
	}
	if b.opts.HasZFS {
		b.state.arcBefore, b.state.hasArcBefore = zfs.ReadArcSnapshot()
	}

	profilePhase("tick_total", start)
	return update, nil
}

func (b *linuxBackend) RefreshSlow() (SlowUpdate, error) {
	start := time.Now()
	update := SlowUpdate{}

	if b.opts.HasZFS {
		b.poolStatus = zfs.ReadPoolStatus()
		b.cgroupDevice = resolveCgroupDevice()
		update.ZfsPoolMetrics = zfs.CollectPoolMetrics(b.poolIORates, b.poolStatus)
	}

	if b.opts.EnableDocker {
		update.DockerContainers = docker.CollectContainerMetrics()
	}
	if b.opts.EnableGPU {
		update.GPUMetrics = gpupkg.Collect()
	}

	mounts, err := disk.ListMounts()
	if err == nil {
		b.mounts = mounts
		update.Mounts = mounts
		update.HasMounts = true
	}

	profilePhase("refresh_slow", start)
	return update, nil
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
