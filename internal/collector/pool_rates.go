package collector

import (
	"time"

	"fascinated.cc/monitor/agent/internal/delta"
	"fascinated.cc/monitor/agent/internal/iostats"
	"fascinated.cc/monitor/agent/internal/zfs"
)

func computePoolIORates(before, after map[string]zfs.PoolIO, elapsed time.Duration) map[string]zfs.PoolIORates {
	rates := make(map[string]zfs.PoolIORates)
	for pool, curr := range after {
		prev, ok := before[pool]
		if !ok {
			continue
		}
		rates[pool] = zfs.PoolIORates{
			ReadBytesPerSecond:  iostats.PerSecond(delta.Uint64(curr.Nread, prev.Nread), elapsed),
			WriteBytesPerSecond: iostats.PerSecond(delta.Uint64(curr.Nwritten, prev.Nwritten), elapsed),
		}
	}
	return rates
}
