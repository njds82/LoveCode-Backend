//go:build !linux

package sysinfo

import (
	"runtime"
)

func getKernelVersion() string {
	return runtime.GOOS
}

func getUptimeSeconds() int64 {
	return 0
}

func getCPUInfo() CPUInfo {
	return CPUInfo{
		Cores: runtime.NumCPU(),
		Model: runtime.GOARCH,
	}
}

func getMemoryInfo() MemoryInfo {
	return MemoryInfo{}
}

func getDiskInfo(path string) (DiskInfo, error) {
	return DiskInfo{
		MountPoint: path,
	}, nil
}
