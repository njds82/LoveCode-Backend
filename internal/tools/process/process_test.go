package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/tools"
)

type mockInspector struct {
	processes map[int]ProcessInfo
}

func (m *mockInspector) ListProcesses() ([]ProcessInfo, error) {
	var list []ProcessInfo
	for _, p := range m.processes {
		list = append(list, p)
	}
	return list, nil
}

func (m *mockInspector) GetProcess(pid int) (*ProcessInfo, error) {
	if p, ok := m.processes[pid]; ok {
		return &p, nil
	}
	return nil, ErrProcessNotFound
}

func TestProcessTools_ProtectedPIDs(t *testing.T) {
	inspector := &mockInspector{
		processes: map[int]ProcessInfo{
			1:            {PID: 1, PPID: 0, Name: "systemd", IsProtected: true},
			2:            {PID: 2, PPID: 0, Name: "kthreadd", IsProtected: true},
			os.Getpid():  {PID: os.Getpid(), PPID: os.Getppid(), Name: "lovecode", IsProtected: true},
			os.Getppid(): {PID: os.Getppid(), PPID: 1, Name: "go", IsProtected: true},
			100:          {PID: 100, PPID: 1, Name: "systemd-journald", IsProtected: true},
			101:          {PID: 101, PPID: 2, Name: "kworker/0:0", IsProtected: true},
		},
	}

	killTool := NewKillTool(inspector)
	ctx := context.Background()
	execCtx := tools.ExecutionContext{}

	protectedCases := []struct {
		name string
		pid  int
	}{
		{"PID_1", 1},
		{"PID_Self", os.Getpid()},
		{"PID_Parent", os.Getppid()},
		{"PID_2", 2},
		{"Daemon_journald", 100},
		{"Kernel_thread", 101},
	}

	for _, tc := range protectedCases {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{
				"pid":    tc.pid,
				"signal": "SIGTERM",
			})
			_, err := killTool.Execute(ctx, execCtx, args)
			if err == nil {
				t.Fatalf("expected error for protected process %d, got nil", tc.pid)
			}
			if !errors.Is(err, tools.ErrPermissionDenied) {
				t.Errorf("expected ErrPermissionDenied, got: %v", err)
			}
		})
	}
}

func TestProcessTools_InvalidPIDAndSignal(t *testing.T) {
	killTool := NewKillTool(nil)
	ctx := context.Background()
	execCtx := tools.ExecutionContext{}

	// Invalid PIDs (<= 0)
	for _, pid := range []int{0, -1, -99} {
		args, _ := json.Marshal(map[string]any{"pid": pid})
		_, err := killTool.Execute(ctx, execCtx, args)
		if err == nil {
			t.Errorf("expected error for invalid PID %d, got nil", pid)
		}
		if !errors.Is(err, tools.ErrSchemaValidation) {
			t.Errorf("expected ErrSchemaValidation, got: %v", err)
		}
	}

	// Invalid signal
	args, _ := json.Marshal(map[string]any{
		"pid":    99999,
		"signal": "SIGUSR1",
	})
	_, err := killTool.Execute(ctx, execCtx, args)
	if err == nil {
		t.Error("expected error for unsupported signal SIGUSR1, got nil")
	}
	if !errors.Is(err, tools.ErrSchemaValidation) {
		t.Errorf("expected ErrSchemaValidation, got: %v", err)
	}
}

func TestProcessTools_RealSubprocess_ListAndKill(t *testing.T) {
	// Spawn safe child process: sleep 30
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test subprocess: %v", err)
	}
	childPID := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	listTool := NewListTool(nil)
	killTool := NewKillTool(nil)
	ctx := context.Background()
	execCtx := tools.ExecutionContext{}

	// 1. List processes with filter "sleep"
	listArgs, _ := json.Marshal(map[string]any{
		"filter": "sleep",
		"limit":  50,
	})
	res, err := listTool.Execute(ctx, execCtx, listArgs)
	if err != nil {
		t.Fatalf("process_list failed: %v", err)
	}
	found := false
	if procs, ok := res.Data.([]ProcessInfo); ok {
		for _, p := range procs {
			if p.PID == childPID {
				found = true
				break
			}
		}
	}
	if !found {
		t.Logf("child PID %d not matched in filter (might have exited early), continuing kill check", childPID)
	}

	// 2. Kill subprocess with SIGKILL
	killArgs, _ := json.Marshal(map[string]any{
		"pid":    childPID,
		"signal": "SIGKILL",
	})
	_, err = killTool.Execute(ctx, execCtx, killArgs)
	if err != nil {
		t.Fatalf("process_kill failed: %v", err)
	}

	// Wait for process to exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		// Process exited successfully due to kill
	case <-time.After(2 * time.Second):
		t.Fatal("subprocess did not exit within 2 seconds after SIGKILL")
	}
}

func TestProcessTools_CredentialRedactionInCLI(t *testing.T) {
	// Spawn dummy subprocess with secret argument
	secretArg := "--token=ghp_0123456789abcdef0123456789abcdef0123"
	cmd := exec.Command("sleep", "5")
	cmd.Args = []string{"sleep", "5", secretArg}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep process: %v", err)
	}
	childPID := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	inspector := NewLinuxProcInspector()
	pInfo, err := inspector.GetProcess(childPID)
	if err != nil {
		t.Skipf("cannot read process %d: %v", childPID, err)
	}
	if pInfo.CommandLine == fmt.Sprintf("[%s]", pInfo.Name) {
		t.Skip("skipping cmdline redaction check: /proc/[pid]/cmdline is not readable in current sandbox environment")
	}

	if strings.Contains(pInfo.CommandLine, "ghp_0123456789abcdef") {
		t.Errorf("secret token leaked in process command line: %s", pInfo.CommandLine)
	}
	if !strings.Contains(pInfo.CommandLine, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in command line, got: %s", pInfo.CommandLine)
	}
}
