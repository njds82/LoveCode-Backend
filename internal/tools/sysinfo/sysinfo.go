package sysinfo

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
)

// ErrSchemaValidation indicates an invalid mount path or telemetry parameter.
var ErrSchemaValidation = errors.New("schema validation failed")

type SystemInfo struct {
	OS            string        `json:"os"`
	Arch          string        `json:"arch"`
	KernelVersion string        `json:"kernel_version"`
	Hostname      string        `json:"hostname"`
	UptimeSeconds int64         `json:"uptime_seconds"`
	UptimeHuman   string        `json:"uptime_human"`
	CPU           CPUInfo       `json:"cpu"`
	Memory        MemoryInfo    `json:"memory"`
	Disk          DiskInfo      `json:"disk"`
	Runtime       GoRuntimeInfo `json:"runtime"`
}

type CPUInfo struct {
	Cores        int        `json:"cores"`
	Model        string     `json:"model"`
	LoadAverages [3]float64 `json:"load_averages"`
}

type MemoryInfo struct {
	TotalBytes     uint64  `json:"total_bytes"`
	FreeBytes      uint64  `json:"free_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

type DiskInfo struct {
	MountPoint  string  `json:"mount_point"`
	TotalBytes  uint64  `json:"total_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

type GoRuntimeInfo struct {
	GoVersion    string `json:"go_version"`
	NumGoroutine int    `json:"num_goroutine"`
	AllocBytes   uint64 `json:"alloc_bytes"`
}

// Collector inspects host operating system and resource metrics without credential leaks.
type Collector struct{}

// NewCollector constructs a sysinfo collector.
func NewCollector() *Collector {
	return &Collector{}
}

// Collect returns human-readable summary, structured telemetry, or error.
func (c *Collector) Collect(mount string) (string, SystemInfo, error) {
	if mount == "" {
		mount = "/"
	}

	hostname, _ := os.Hostname()
	kernel := getKernelVersion()
	uptimeSec := getUptimeSeconds()

	cpuInfo := getCPUInfo()
	memInfo := getMemoryInfo()
	diskInfo, err := getDiskInfo(mount)
	if err != nil {
		return "", SystemInfo{}, fmt.Errorf("%w: cannot inspect disk for %q: %v", ErrSchemaValidation, mount, err)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	goRuntime := GoRuntimeInfo{
		GoVersion:    runtime.Version(),
		NumGoroutine: runtime.NumGoroutine(),
		AllocBytes:   m.Alloc,
	}

	info := SystemInfo{
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		KernelVersion: kernel,
		Hostname:      hostname,
		UptimeSeconds: uptimeSec,
		UptimeHuman:   formatDuration(time.Duration(uptimeSec) * time.Second),
		CPU:           cpuInfo,
		Memory:        memInfo,
		Disk:          diskInfo,
		Runtime:       goRuntime,
	}

	output := fmt.Sprintf("OS: %s/%s\nKernel: %s\nHostname: %s\nUptime: %s\nCPU: %d cores (%s), Load: %.2f %.2f %.2f\nMemory: %.1f MB / %.1f MB (%.1f%% used)\nDisk (%s): %.1f GB / %.1f GB (%.1f%% used)\nGo: %s (%d goroutines)",
		info.OS, info.Arch, info.KernelVersion, info.Hostname, info.UptimeHuman,
		info.CPU.Cores, info.CPU.Model, info.CPU.LoadAverages[0], info.CPU.LoadAverages[1], info.CPU.LoadAverages[2],
		float64(info.Memory.UsedBytes)/(1024*1024), float64(info.Memory.TotalBytes)/(1024*1024), info.Memory.UsedPercent,
		info.Disk.MountPoint, float64(info.Disk.UsedBytes)/(1024*1024*1024), float64(info.Disk.TotalBytes)/(1024*1024*1024), info.Disk.UsedPercent,
		info.Runtime.GoVersion, info.Runtime.NumGoroutine,
	)

	return output, info, nil
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm %ds", days, hours, minutes, seconds)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	return fmt.Sprintf("%dm %ds", minutes, seconds)
}
