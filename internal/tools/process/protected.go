package process

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

var (
	ErrProtectedProcess = errors.New("process is protected from termination")
	ErrInvalidPID       = errors.New("invalid process ID")
	ErrProcessNotFound  = errors.New("process not found")
)

var ProtectedDaemons = map[string]bool{
	"init":             true,
	"systemd":          true,
	"kthreadd":         true,
	"systemd-journald": true,
	"systemd-udevd":    true,
	"systemd-logind":   true,
	"systemd-resolved": true,
	"systemd-networkd": true,
	"dbus-daemon":      true,
	"dbus-broker":      true,
	"auditd":           true,
	"sshd":             true,
	"containerd":       true,
	"dockerd":          true,
	"polkitd":          true,
}

var allowedSignals = map[string]syscall.Signal{
	"SIGTERM": syscall.SIGTERM,
	"SIGKILL": syscall.SIGKILL,
	"SIGINT":  syscall.SIGINT,
	"SIGHUP":  syscall.SIGHUP,
	"SIGQUIT": syscall.SIGQUIT,
}

// ParseSignal converts signal string or default to syscall.Signal.
func ParseSignal(sigStr string) (syscall.Signal, error) {
	if sigStr == "" {
		return syscall.SIGTERM, nil
	}
	upper := strings.ToUpper(strings.TrimSpace(sigStr))
	if !strings.HasPrefix(upper, "SIG") {
		upper = "SIG" + upper
	}
	if sig, ok := allowedSignals[upper]; ok {
		return sig, nil
	}
	return 0, fmt.Errorf("%w: signal %q is not permitted (allowed: SIGTERM, SIGKILL, SIGINT, SIGHUP, SIGQUIT)", ErrSchemaValidation, sigStr)
}

// ValidateKillTarget checks PID and daemon immunity before allowing termination.
func ValidateKillTarget(pid int, inspector ProcessInspector) (*ProcessInfo, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("%w: PID must be a positive integer (got %d)", ErrSchemaValidation, pid)
	}
	if pid == 1 {
		return nil, fmt.Errorf("%w: PID 1 (init/systemd) cannot be terminated", ErrPermissionDenied)
	}
	if pid == os.Getpid() {
		return nil, fmt.Errorf("%w: LoveCode server PID (%d) cannot terminate itself", ErrPermissionDenied, pid)
	}
	if pid == os.Getppid() {
		return nil, fmt.Errorf("%w: parent process PID (%d) cannot be terminated", ErrPermissionDenied, pid)
	}
	if pid == 2 {
		return nil, fmt.Errorf("%w: PID 2 (kthreadd) cannot be terminated", ErrPermissionDenied)
	}

	if inspector != nil {
		p, err := inspector.GetProcess(pid)
		if err != nil {
			return nil, err
		}
		if p.PPID == 2 {
			return nil, fmt.Errorf("%w: kernel thread cannot be terminated", ErrPermissionDenied)
		}
		norm := strings.ToLower(p.Name)
		if ProtectedDaemons[norm] {
			return nil, fmt.Errorf("%w: critical system daemon %q cannot be terminated", ErrPermissionDenied, p.Name)
		}
		return p, nil
	}

	return nil, nil
}
