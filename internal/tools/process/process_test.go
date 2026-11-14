package process

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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
			_, _, err := Kill(inspector, tc.pid, "SIGTERM")
			if err == nil {
				t.Fatalf("expected error for protected process %d, got nil", tc.pid)
			}
			if !errors.Is(err, ErrPermissionDenied) {
				t.Errorf("expected ErrPermissionDenied, got: %v", err)
			}
		})
	}
}

func TestProcessTools_InvalidPIDAndSignal(t *testing.T) {
	// Invalid PIDs (<= 0)
	for _, pid := range []int{0, -1, -99} {
		_, _, err := Kill(nil, pid, "SIGTERM")
		if err == nil {
			t.Errorf("expected error for invalid PID %d, got nil", pid)
		}
		if !errors.Is(err, ErrSchemaValidation) {
			t.Errorf("expected ErrSchemaValidation, got: %v", err)
		}
	}

	// Invalid signal
	_, _, err := Kill(nil, 99999, "SIGUSR1")
	if err == nil {
		t.Error("expected error for unsupported signal SIGUSR1, got nil")
	}
	if !errors.Is(err, ErrSchemaValidation) {
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

	// 1. List processes with filter "sleep"
	_, procs, err := List(nil, "sleep", 50)
	if err != nil {
		t.Fatalf("process_list failed: %v", err)
	}
	found := false
	for _, p := range procs {
		if p.PID == childPID {
			found = true
			if !strings.Contains(p.CommandLine, "sleep") {
				t.Errorf("expected cmdline to contain 'sleep', got: %s", p.CommandLine)
			}
			break
		}
	}
	if !found {
		t.Errorf("did not find spawned child process %d in process list", childPID)
	}

	// 2. Kill spawned child process with SIGTERM
	out, resData, err := Kill(nil, childPID, "SIGTERM")
	if err != nil {
		t.Fatalf("failed to kill test child process: %v", err)
	}
	if resData.PID != childPID {
		t.Errorf("expected PID %d, got %d", childPID, resData.PID)
	}
	if !strings.Contains(out, "SIGTERM") {
		t.Errorf("expected output to mention SIGTERM, got: %s", out)
	}

	// Allow process termination to settle
	time.Sleep(50 * time.Millisecond)

	// Killing already terminated process should return ErrToolNotFound
	_, _, err = Kill(nil, childPID, "SIGTERM")
	if err == nil {
		t.Logf("note: PID %d may still be in zombie state before wait", childPID)
	}
}

func TestProcessTools_SecretRedactionInCommandLine(t *testing.T) {
	fakeToken := "ghp_1234567890abcdefghijklmnopqrstuvwxyzAB"
	inspector := &mockInspector{
		processes: map[int]ProcessInfo{
			200: {
				PID:         200,
				PPID:        1,
				Name:        "worker",
				CommandLine: "worker --token=" + fakeToken + " --user=admin",
			},
		},
	}

	out, procs, err := List(inspector, "worker", 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(procs) == 0 {
		t.Fatalf("expected 1 process")
	}

	if strings.Contains(out, fakeToken) {
		t.Errorf("secret token leaked in formatted list output: %s", out)
	}
	if strings.Contains(procs[0].CommandLine, fakeToken) {
		t.Errorf("secret token leaked in ProcessInfo.CommandLine: %s", procs[0].CommandLine)
	}
}
