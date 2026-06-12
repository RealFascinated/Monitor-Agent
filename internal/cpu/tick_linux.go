//go:build linux

package cpu

import (
	"time"

	"fascinated.cc/monitor/agent/internal/delta"
	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/iostats"
	"fascinated.cc/monitor/agent/internal/linux"
)

type LinuxTickInput struct {
	PrevProc, CurrProc       linux.ProcStatSnapshot
	PrevPower, CurrPower     uint64
	PrevPowerMax, CurrPowerMax uint64
	HasPowerBefore, HasPowerAfter bool
	PrevCgroupCPU            uint64
	HasCgroupCPU             bool
	Cgroup                   string
	Elapsed                  time.Duration
}

func ComputeLinuxTick(in LinuxTickInput) ingest.ServerMetrics {
	var metrics ingest.ServerMetrics

	if in.PrevProc.HasCPU && in.CurrProc.HasCPU {
		usage, user, system, iowait, steal := linux.ComputeCPUFromProcStat(in.PrevProc.CPU, in.CurrProc.CPU)
		metrics.CPUUsage = usage
		metrics.CPUUserPercent = user
		metrics.CPUSystemPercent = system
		metrics.CPUIowaitPercent = iowait
		metrics.CPUStealPercent = steal
	}
	if len(in.PrevProc.PerCPU) > 0 && len(in.CurrProc.PerCPU) > 0 {
		metrics.CPUCoreMetrics = coreMetricsFromUsage(ComputePerCoreCPU(in.PrevProc.PerCPU, in.CurrProc.PerCPU))
	}
	if in.HasPowerBefore && in.HasPowerAfter {
		maxEnergy := in.CurrPowerMax
		if maxEnergy == 0 {
			maxEnergy = in.PrevPowerMax
		}
		if watts, ok := ComputePowerWatts(in.PrevPower, in.CurrPower, maxEnergy, in.Elapsed); ok {
			metrics.CPUPowerWatts = watts
		}
	}
	if in.HasCgroupCPU {
		if afterUsage, ok := ReadCgroupUsageUsec(in.Cgroup); ok {
			if usage, ok := CgroupUsagePercent(in.PrevCgroupCPU, afterUsage, in.Cgroup, in.Elapsed); ok {
				metrics.CPUUsage = usage
			}
		}
	}

	metrics.ContextSwitchesPerSecond = iostats.PerSecond(delta.Uint64(in.CurrProc.ContextSwitches, in.PrevProc.ContextSwitches), in.Elapsed)
	metrics.InterruptsPerSecond = iostats.PerSecond(delta.Uint64(in.CurrProc.Interrupts, in.PrevProc.Interrupts), in.Elapsed)

	return metrics
}

func coreMetricsFromUsage(cores []CoreUsage) []ingest.CPUCoreMetric {
	if len(cores) == 0 {
		return nil
	}
	out := make([]ingest.CPUCoreMetric, len(cores))
	for i, core := range cores {
		out[i] = ingest.CPUCoreMetric{
			CPU:          core.CPU,
			UsagePercent: core.UsagePercent,
		}
	}
	return out
}
