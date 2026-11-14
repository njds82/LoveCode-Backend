package process

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lovecode/lovecode/internal/security"
)

var (
	ErrSchemaValidation = errors.New("schema validation failed")
	ErrPermissionDenied = errors.New("permission denied")
	ErrToolNotFound     = errors.New("tool not found")
)

type ProcessInfo struct {
	PID         int    `json:"pid"`
	PPID        int    `json:"ppid"`
	Name        string `json:"name"`
	State       string `json:"state"`
	RSSBytes    int64  `json:"rss_bytes"`
	CommandLine string `json:"command_line,omitempty"`
	IsProtected bool   `json:"is_protected"`
}

type ProcessInspector interface {
	ListProcesses() ([]ProcessInfo, error)
	GetProcess(pid int) (*ProcessInfo, error)
}

// LinuxProcInspector inspects processes via the Linux /proc pseudo-filesystem.
type LinuxProcInspector struct{}

func NewLinuxProcInspector() *LinuxProcInspector {
	return &LinuxProcInspector{}
}

func (insp *LinuxProcInspector) ListProcesses() ([]ProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc failed: %w", err)
	}

	var list []ProcessInfo
	pageSize := int64(os.Getpagesize())

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}

		pInfo, err := insp.readProcess(pid, pageSize)
		if err == nil && pInfo != nil {
			list = append(list, *pInfo)
		}
	}

	return list, nil
}

func (insp *LinuxProcInspector) GetProcess(pid int) (*ProcessInfo, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("%w: invalid PID %d", ErrSchemaValidation, pid)
	}
	pageSize := int64(os.Getpagesize())
	pInfo, err := insp.readProcess(pid, pageSize)
	if err != nil {
		return nil, fmt.Errorf("%w: process with PID %d not found: %v", ErrToolNotFound, pid, err)
	}
	return pInfo, nil
}

func (insp *LinuxProcInspector) readProcess(pid int, pageSize int64) (*ProcessInfo, error) {
	statPath := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	statBytes, err := os.ReadFile(statPath)
	if err != nil {
		return nil, err
	}

	// /proc/[pid]/stat format:
	// pid (comm) state ppid ...
	statStr := string(statBytes)
	openParen := strings.Index(statStr, "(")
	closeParen := strings.LastIndex(statStr, ")")
	if openParen == -1 || closeParen == -1 || closeParen <= openParen {
		return nil, fmt.Errorf("malformed stat for PID %d", pid)
	}

	name := statStr[openParen+1 : closeParen]
	afterParen := strings.TrimSpace(statStr[closeParen+1:])
	fields := strings.Fields(afterParen)

	state := "unknown"
	ppid := 0
	rssBytes := int64(0)

	if len(fields) >= 1 {
		state = fields[0]
	}
	if len(fields) >= 2 {
		ppid, _ = strconv.Atoi(fields[1])
	}
	if len(fields) >= 22 {
		rssPages, _ := strconv.ParseInt(fields[21], 10, 64)
		rssBytes = rssPages * pageSize
	}

	// Read cmdline, replacing null bytes with spaces and redacting secrets
	cmdlinePath := filepath.Join("/proc", strconv.Itoa(pid), "cmdline")
	var cmdline string
	if cmdBytes, err := os.ReadFile(cmdlinePath); err == nil && len(cmdBytes) > 0 {
		cmdBytes = bytes.ReplaceAll(cmdBytes, []byte{0}, []byte{' '})
		rawCmd := strings.TrimSpace(string(cmdBytes))
		cmdline = security.RedactString(rawCmd)
	} else {
		cmdline = fmt.Sprintf("[%s]", name)
	}

	isProtected := false
	if pid == 1 || pid == os.Getpid() || pid == os.Getppid() || pid == 2 || ppid == 2 || ProtectedDaemons[strings.ToLower(name)] {
		isProtected = true
	}

	return &ProcessInfo{
		PID:         pid,
		PPID:        ppid,
		Name:        name,
		State:       state,
		RSSBytes:    rssBytes,
		CommandLine: cmdline,
		IsProtected: isProtected,
	}, nil
}
