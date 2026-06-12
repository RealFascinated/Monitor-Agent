package collector

import "fascinated.cc/monitor/agent/internal/ingest"

// Result is the latest metric snapshot returned by Sampler.Snapshot.
//
// Field ownership:
//   - Fast (tick): ServerMetrics, InterfaceMetrics, DiskMetrics IO rates,
//     ZfsArcMetrics, TCPConnectionMetrics
//   - Slow (refreshSlow): ZfsPoolMetrics, DockerContainers, GPUMetrics,
//     DiskMetrics capacity fields (used/total/inodes)
type Result struct {
	ServerMetrics        ingest.ServerMetrics
	InterfaceMetrics     []ingest.InterfaceMetrics
	DiskMetrics          []ingest.DiskMetric
	ZfsArcMetrics        *ingest.ZFSArcMetrics
	ZfsPoolMetrics       []ingest.ZfsPoolMetric
	DockerContainers     []ingest.DockerContainerMetric
	GPUMetrics           []ingest.GPUMetric
	TCPConnectionMetrics []ingest.TCPConnectionMetric
}
