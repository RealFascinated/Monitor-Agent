//go:build windows

package platform

import (
	"context"
	"sync"
	"time"

	"fascinated.cc/monitor/agent/internal/counters"
	cpupkg "fascinated.cc/monitor/agent/internal/cpu"
	"fascinated.cc/monitor/agent/internal/disk"
	"fascinated.cc/monitor/agent/internal/docker"
	gpupkg "fascinated.cc/monitor/agent/internal/gpu"
	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/lhm"
	"fascinated.cc/monitor/agent/internal/loadavg"
	"fascinated.cc/monitor/agent/internal/memory"
	"fascinated.cc/monitor/agent/internal/network"
	"fascinated.cc/monitor/agent/internal/thermal"
	"fascinated.cc/monitor/agent/internal/zfs"

	"github.com/shirou/gopsutil/v4/cpu"
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

type windowsBackend struct {
	opts Options

	state       windowsTickState
	initialized bool

	mounts      []disk.Mount
	poolStatus  zfs.PoolStatusSnapshot
	poolIORates map[string]zfs.PoolIORates
}

func newWindowsBackend(opts Options) *windowsBackend {
	return &windowsBackend{
		opts:        opts,
		poolIORates: map[string]zfs.PoolIORates{},
	}
}

func (b *windowsBackend) Tick(ready bool) (TickUpdate, error) {
	start := time.Now()

	cpuAgg, err := cpu.Times(false)
	if err != nil || len(cpuAgg) == 0 {
		return TickUpdate{}, err
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
		return TickUpdate{}, netErr
	}
	if ioErr != nil {
		afterIO = map[string]disk.IOCounters{}
	}

	countersAfter, countersPerSecond, _ := counters.ReadSystemCounters()
	if b.opts.HasZFS {
		afterZFS = zfs.ReadPoolIOSnapshots()
	}

	now := time.Now()
	if !b.initialized {
		cpupkg.BeginIowaitSample()
		cpupkg.BeginCPUPowerSample()
		netBefore = netAfter
		b.state = windowsTickState{
			lastAt:         now,
			cpuBefore:      cpuAgg[0],
			perCPUBefore:   perCPU,
			netBefore:      netBefore,
			beforeIO:       afterIO,
			countersBefore: countersAfter,
			countersPerSec: countersPerSecond,
			beforeZFS:      afterZFS,
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

	metrics := cpupkg.ComputeWindowsTick(cpupkg.WindowsTickInput{
		PrevCPU:        prev.cpuBefore,
		CurrCPU:        cpuAgg[0],
		PrevPerCPU:     prev.perCPUBefore,
		CurrPerCPU:     perCPU,
		PrevCounters:   prev.countersBefore,
		CurrCounters:   countersAfter,
		CountersPerSec: prev.countersPerSec,
		Elapsed:        elapsed,
	})
	memory.ApplyTo(&metrics, memory.Read())

	avg := loadavg.Read()
	metrics.Load1 = avg.Load1
	metrics.Load5 = avg.Load5
	metrics.Load15 = avg.Load15

	if len(b.mounts) == 0 {
		mounts, err := disk.ListMounts()
		if err != nil {
			return TickUpdate{}, err
		}
		b.mounts = mounts
	}

	b.poolIORates = zfs.ComputePoolIORates(prev.beforeZFS, afterZFS, elapsed)
	update.InterfaceMetrics = network.ComputeMetrics(prev.netBefore, netAfter, elapsed)
	update.DiskMetrics = disk.BuildFromSamples(b.opts.HasZFS, b.mounts, prev.beforeIO, afterIO, prev.beforeZFS, afterZFS, elapsed)

	if b.opts.HasZFS {
		if arcAfter, ok := zfs.ReadArcSnapshot(); ok && prev.hasArcBefore {
			update.ZfsArcMetrics = zfs.ComputeArcMetrics(prev.arcBefore, arcAfter, elapsed)
		}
	}

	update.ServerMetrics = metrics
	if mhz, err := cpupkg.GetClockSpeedMHz(); err == nil {
		update.ClockMHz = mhz
	}

	b.state = windowsTickState{
		lastAt:         now,
		cpuBefore:      cpuAgg[0],
		perCPUBefore:   perCPU,
		netBefore:      netAfter,
		beforeIO:       afterIO,
		countersBefore: countersAfter,
		countersPerSec: countersPerSecond,
		beforeZFS:      afterZFS,
	}
	if b.opts.HasZFS {
		b.state.arcBefore, b.state.hasArcBefore = zfs.ReadArcSnapshot()
	}

	profilePhase("tick_total", start)
	return update, nil
}

func (b *windowsBackend) RefreshSlow() (SlowUpdate, error) {
	start := time.Now()
	update := SlowUpdate{}

	if b.opts.HasZFS {
		b.poolStatus = zfs.ReadPoolStatus()
		update.ZfsPoolMetrics = zfs.CollectPoolMetrics(b.poolIORates, b.poolStatus)
	}

	if b.opts.EnableDocker {
		update.DockerContainers = docker.CollectContainerMetrics()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	snap, err := lhm.GetServerMetrics(ctx)
	cancel()
	if err != nil {
		thermal.WarnLHMSlow(err)
	} else {
		memory.OverlayFromLHM(&update.ServerMetrics, snap)
		cpupkg.OverlayFromLHM(&update.ServerMetrics, snap)
		if temps := thermal.FromLHMSnapshot(snap); len(temps) > 0 {
			update.ServerMetrics.TemperatureMetrics = temps
		}
		if b.opts.EnableGPU {
			update.GPUMetrics = gpupkg.FromLHM(snap)
		}
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
