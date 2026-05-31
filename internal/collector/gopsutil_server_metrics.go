//go:build !linux

package collector

import (
	"fascinated.cc/monitor/agent/internal/ingest"
	"fascinated.cc/monitor/agent/internal/metric"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

func serverMetricsFromGopsutil(
	cpuBefore, cpuAfter cpu.TimesStat,
	perCPUBefore, perCPUAfter []cpu.TimesStat,
	iowait float64,
) ingest.ServerMetrics {
	cpuMetrics := metric.ComputeCPUMetrics(cpuBefore, cpuAfter)
	metrics := ingest.ServerMetrics{
		CPUUsage:         cpuMetrics.Total,
		CPUUserPercent:   cpuMetrics.User,
		CPUSystemPercent: cpuMetrics.System,
		CPUIowaitPercent: iowait,
		CPUStealPercent:  cpuMetrics.Steal,
	}
	metrics.CPUCoreMetrics = coreMetricsFromGopsutil(metric.ComputePerCoreCPUMetrics(perCPUBefore, perCPUAfter))
	metrics.TemperatureMetrics = temperatureMetricsFromGopsutil(metric.ReadTemperatures())

	if vm, err := mem.VirtualMemory(); err == nil {
		metrics.MemoryUsage = float64(vm.Used)
		metrics.MemoryAvailable = float64(vm.Available)
		metrics.MemoryTotal = float64(vm.Total)
		metrics.MemoryBuffers = int64(vm.Buffers)
		metrics.MemoryCached = int64(vm.Cached)
		metrics.SwapTotal = int64(vm.SwapTotal)
		metrics.SwapUsed = int64(vm.SwapTotal - vm.SwapFree)
	}
	return metrics
}
