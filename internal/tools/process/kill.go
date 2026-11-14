package process

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
)

type KillResultData struct {
	PID    int    `json:"pid"`
	Signal string `json:"signal"`
	Status string `json:"status"`
}

// Kill signals a target process with strict immunity checks.
func Kill(inspector ProcessInspector, pid int, sigStr string) (string, KillResultData, error) {
	if inspector == nil {
		inspector = NewLinuxProcInspector()
	}

	sigName := strings.ToUpper(strings.TrimSpace(sigStr))
	if sigName == "" {
		sigName = "SIGTERM"
	} else if !strings.HasPrefix(sigName, "SIG") {
		sigName = "SIG" + sigName
	}

	sig, err := ParseSignal(sigStr)
	if err != nil {
		return "", KillResultData{}, err
	}

	// Validate target PID and immunity
	pInfo, err := ValidateKillTarget(pid, inspector)
	if err != nil {
		return "", KillResultData{}, err
	}

	// Dispatch signal to process
	if err := syscall.Kill(pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return "", KillResultData{}, fmt.Errorf("%w: process with PID %d not found", ErrToolNotFound, pid)
		}
		if errors.Is(err, syscall.EPERM) {
			return "", KillResultData{}, fmt.Errorf("%w: operation not permitted for PID %d", ErrPermissionDenied, pid)
		}
		return "", KillResultData{}, fmt.Errorf("failed to signal process %d: %w", pid, err)
	}

	procName := "process"
	if pInfo != nil && pInfo.Name != "" {
		procName = pInfo.Name
	}

	output := fmt.Sprintf("sent signal %s to %s (PID %d)", sigName, procName, pid)
	data := KillResultData{
		PID:    pid,
		Signal: sigName,
		Status: "signaled",
	}

	return output, data, nil
}
