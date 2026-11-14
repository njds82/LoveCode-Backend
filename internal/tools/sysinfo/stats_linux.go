//go:build linux

package sysinfo

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func getKernelVersion() string {
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		return strings.TrimSpace(string(b))
	}
	var u syscall.Utsname
	if err := syscall.Uname(&u); err == nil {
		var b strings.Builder
		for _, c := range u.Release {
			if c == 0 {
				break
			}
			b.WriteByte(byte(c))
		}
		return b.String()
	}
	return "unknown"
}

func getUptimeSeconds() int64 {
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(b))
		if len(fields) > 0 {
			if f, err := strconv.ParseFloat(fields[0], 64); err == nil {
				return int64(f)
			}
		}
	}
	var si syscall.Sysinfo_t
	if err := syscall.Sysinfo(&si); err == nil {
		return int64(si.Uptime)
	}
	return 0
}

func getCPUInfo() CPUInfo {
	info := CPUInfo{
		Cores: runtime.NumCPU(),
	}

	// Parse model name from /proc/cpuinfo
	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "model name") {
				parts := strings.Split(line, ":")
				if len(parts) > 1 {
					info.Model = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}
	if info.Model == "" {
		info.Model = runtime.GOARCH
	}

	// Parse load averages from /proc/loadavg
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(b))
		if len(fields) >= 3 {
			l1, _ := strconv.ParseFloat(fields[0], 64)
			l5, _ := strconv.ParseFloat(fields[1], 64)
			l15, _ := strconv.ParseFloat(fields[2], 64)
			info.LoadAverages = [3]float64{l1, l5, l15}
		}
	}

	return info
}

func getMemoryInfo() MemoryInfo {
	info := MemoryInfo{}

	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		var totalKB, freeKB, availKB uint64
		for scanner.Scan() {
			line := scanner.Text()
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				key := strings.TrimSuffix(fields[0], ":")
				val, _ := strconv.ParseUint(fields[1], 10, 64)
				switch key {
				case "MemTotal":
					totalKB = val
				case "MemFree":
					freeKB = val
				case "MemAvailable":
					availKB = val
				}
			}
		}

		info.TotalBytes = totalKB * 1024
		info.FreeBytes = freeKB * 1024
		if availKB > 0 {
			info.AvailableBytes = availKB * 1024
		} else {
			info.AvailableBytes = info.FreeBytes
		}

		if info.TotalBytes > info.AvailableBytes {
			info.UsedBytes = info.TotalBytes - info.AvailableBytes
		}
		if info.TotalBytes > 0 {
			info.UsedPercent = (float64(info.UsedBytes) / float64(info.TotalBytes)) * 100.0
		}
		return info
	}

	// Fallback using syscall.Sysinfo
	var si syscall.Sysinfo_t
	if err := syscall.Sysinfo(&si); err == nil {
		unit := uint64(si.Unit)
		if unit == 0 {
			unit = 1
		}
		info.TotalBytes = uint64(si.Totalram) * unit
		info.FreeBytes = uint64(si.Freeram) * unit
		info.AvailableBytes = info.FreeBytes
		info.UsedBytes = info.TotalBytes - info.FreeBytes
		if info.TotalBytes > 0 {
			info.UsedPercent = (float64(info.UsedBytes) / float64(info.TotalBytes)) * 100.0
		}
	}

	return info
}

func getDiskInfo(path string) (DiskInfo, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return DiskInfo{}, err
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := uint64(0)
	if total > stat.Bfree*uint64(stat.Bsize) {
		used = total - (stat.Bfree * uint64(stat.Bsize))
	}
	var usedPercent float64
	if total > 0 {
		usedPercent = (float64(used) / float64(total)) * 100.0
	}

	return DiskInfo{
		MountPoint:  path,
		TotalBytes:  total,
		FreeBytes:   free,
		UsedBytes:   used,
		UsedPercent: usedPercent,
	}, nil
}
