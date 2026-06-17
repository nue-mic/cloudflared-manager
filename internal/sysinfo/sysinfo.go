// Package sysinfo wraps gopsutil to expose host / process metrics that
// are useful when running cfdmgrd inside a container. All functions are
// safe to call from HTTP handlers.
package sysinfo

import (
	"context"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// HostInfo bundles the slow-changing host facts.
type HostInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platform_version"`
	KernelVersion   string `json:"kernel_version"`
	KernelArch      string `json:"kernel_arch"`
	Virtualization  string `json:"virtualization,omitempty"`
	UptimeSeconds   uint64 `json:"uptime_seconds"`
	BootTime        uint64 `json:"boot_time"`
}

// Host returns host-level information.
func Host(ctx context.Context) (HostInfo, error) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return HostInfo{}, err
	}
	return HostInfo{
		Hostname:        info.Hostname,
		OS:              info.OS,
		Platform:        info.Platform,
		PlatformVersion: info.PlatformVersion,
		KernelVersion:   info.KernelVersion,
		KernelArch:      info.KernelArch,
		Virtualization:  info.VirtualizationSystem,
		UptimeSeconds:   info.Uptime,
		BootTime:        info.BootTime,
	}, nil
}

// CPUInfo describes CPU usage and topology.
type CPUInfo struct {
	LogicalCount  int       `json:"logical_count"`
	PhysicalCount int       `json:"physical_count"`
	ModelName     string    `json:"model_name,omitempty"`
	MhzPerCore    float64   `json:"mhz_per_core,omitempty"`
	UsagePercent  float64   `json:"usage_percent"`
	PerCore       []float64 `json:"per_core,omitempty"`
	LoadAvg1      float64   `json:"load_avg_1,omitempty"`
	LoadAvg5      float64   `json:"load_avg_5,omitempty"`
	LoadAvg15     float64   `json:"load_avg_15,omitempty"`
}

// CPU returns CPU usage averaged over a short sample. window is the time
// to sample (e.g. 200ms); larger values are more accurate but block
// longer.
func CPU(ctx context.Context, window time.Duration) (CPUInfo, error) {
	if window <= 0 {
		window = 250 * time.Millisecond
	}
	logical, _ := cpu.CountsWithContext(ctx, true)
	physical, _ := cpu.CountsWithContext(ctx, false)
	out := CPUInfo{LogicalCount: logical, PhysicalCount: physical}

	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		out.ModelName = infos[0].ModelName
		out.MhzPerCore = infos[0].Mhz
	}
	if total, err := cpu.PercentWithContext(ctx, window, false); err == nil && len(total) > 0 {
		out.UsagePercent = total[0]
	}
	if per, err := cpu.PercentWithContext(ctx, 0, true); err == nil {
		out.PerCore = per
	}
	if avg, err := loadAverage(); err == nil {
		out.LoadAvg1 = avg[0]
		out.LoadAvg5 = avg[1]
		out.LoadAvg15 = avg[2]
	}
	return out, nil
}

// MemInfo describes virtual + swap memory.
type MemInfo struct {
	Total       uint64  `json:"total"`
	Available   uint64  `json:"available"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"used_percent"`
	Free        uint64  `json:"free"`
	SwapTotal   uint64  `json:"swap_total"`
	SwapUsed    uint64  `json:"swap_used"`
}

// Memory returns memory utilization.
func Memory(ctx context.Context) (MemInfo, error) {
	v, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return MemInfo{}, err
	}
	out := MemInfo{
		Total:       v.Total,
		Available:   v.Available,
		Used:        v.Used,
		UsedPercent: v.UsedPercent,
		Free:        v.Free,
	}
	if s, err := mem.SwapMemoryWithContext(ctx); err == nil {
		out.SwapTotal = s.Total
		out.SwapUsed = s.Used
	}
	return out, nil
}

// DiskUsage is the usage of a single filesystem.
type DiskUsage struct {
	Path        string  `json:"path"`
	Fstype      string  `json:"fstype,omitempty"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

// Disk returns usage for paths. If paths is empty, returns the root + data dir.
func Disk(ctx context.Context, paths ...string) ([]DiskUsage, error) {
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	out := make([]DiskUsage, 0, len(paths))
	for _, p := range paths {
		u, err := disk.UsageWithContext(ctx, p)
		if err != nil {
			continue
		}
		out = append(out, DiskUsage{
			Path:        u.Path,
			Fstype:      u.Fstype,
			Total:       u.Total,
			Used:        u.Used,
			Free:        u.Free,
			UsedPercent: u.UsedPercent,
		})
	}
	return out, nil
}

// IfaceStats is the cumulative byte/packet counters for one interface.
type IfaceStats struct {
	Name        string `json:"name"`
	BytesSent   uint64 `json:"bytes_sent"`
	BytesRecv   uint64 `json:"bytes_recv"`
	PacketsSent uint64 `json:"packets_sent"`
	PacketsRecv uint64 `json:"packets_recv"`
	Errin       uint64 `json:"err_in"`
	Errout      uint64 `json:"err_out"`
	Dropin      uint64 `json:"drop_in"`
	Dropout     uint64 `json:"drop_out"`
}

// Interfaces returns per-interface counters. Loopback is excluded.
func Interfaces(ctx context.Context) ([]IfaceStats, error) {
	counters, err := psnet.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]IfaceStats, 0, len(counters))
	for _, c := range counters {
		if c.Name == "lo" || c.Name == "lo0" {
			continue
		}
		out = append(out, IfaceStats{
			Name:        c.Name,
			BytesSent:   c.BytesSent,
			BytesRecv:   c.BytesRecv,
			PacketsSent: c.PacketsSent,
			PacketsRecv: c.PacketsRecv,
			Errin:       c.Errin,
			Errout:      c.Errout,
			Dropin:      c.Dropin,
			Dropout:     c.Dropout,
		})
	}
	return out, nil
}

// ConnSummary aggregates TCP/UDP socket counts.
type ConnSummary struct {
	TCPTotal       int            `json:"tcp_total"`
	UDPTotal       int            `json:"udp_total"`
	TCPByStatus    map[string]int `json:"tcp_by_status"`
	OwnedTCPConns  int            `json:"owned_tcp_conns"`
	OwnedUDPConns  int            `json:"owned_udp_conns"`
}

// Connections returns a counted summary plus the daemon-owned subset.
func Connections(ctx context.Context) (ConnSummary, error) {
	out := ConnSummary{TCPByStatus: map[string]int{}}
	pid := int32(os.Getpid())
	conns, err := psnet.ConnectionsWithContext(ctx, "all")
	if err != nil {
		return out, err
	}
	for _, c := range conns {
		switch c.Type {
		case 1: // SOCK_STREAM
			out.TCPTotal++
			if c.Status != "" {
				out.TCPByStatus[c.Status]++
			}
			if c.Pid == pid {
				out.OwnedTCPConns++
			}
		case 2: // SOCK_DGRAM
			out.UDPTotal++
			if c.Pid == pid {
				out.OwnedUDPConns++
			}
		}
	}
	return out, nil
}

// ProcInfo describes the running daemon process.
type ProcInfo struct {
	PID         int32   `json:"pid"`
	CPUPercent  float64 `json:"cpu_percent"`
	RSSBytes    uint64  `json:"rss_bytes"`
	VMSBytes    uint64  `json:"vms_bytes"`
	NumThreads  int32   `json:"num_threads"`
	NumGoroutines int   `json:"num_goroutines"`
	OpenFiles   int     `json:"open_files,omitempty"`
	StartTime   int64   `json:"start_time"`
}

// Process returns information about the daemon process itself.
func Process(ctx context.Context) (ProcInfo, error) {
	p, err := process.NewProcessWithContext(ctx, int32(os.Getpid()))
	if err != nil {
		return ProcInfo{}, err
	}
	out := ProcInfo{PID: p.Pid, NumGoroutines: runtime.NumGoroutine()}
	if cp, err := p.CPUPercentWithContext(ctx); err == nil {
		out.CPUPercent = cp
	}
	if mi, err := p.MemoryInfoWithContext(ctx); err == nil {
		out.RSSBytes = mi.RSS
		out.VMSBytes = mi.VMS
	}
	if nt, err := p.NumThreadsWithContext(ctx); err == nil {
		out.NumThreads = nt
	}
	if ct, err := p.CreateTimeWithContext(ctx); err == nil {
		out.StartTime = ct
	}
	if of, err := p.OpenFilesWithContext(ctx); err == nil {
		out.OpenFiles = len(of)
	}
	return out, nil
}
