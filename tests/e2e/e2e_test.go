package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

var binaryPath string

func TestMain(m *testing.M) {
	// Build binary once for CLI tests
	tmpDir, err := os.MkdirTemp("", "lovecode-e2e-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for binary: %v\n", err)
		os.Exit(1)
	}

	bin := filepath.Join(tmpDir, "lovecode")
	buildCmd := exec.Command("go", "build", "-o", bin, "../../cmd/lovecode")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build lovecode binary: %v\noutput: %s\n", err, string(out))
		_ = os.RemoveAll(tmpDir)
		os.Exit(1)
	}

	binaryPath = bin
	code := m.Run()
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}

func TestCLI_VersionFlag(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "-version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("-version failed: %v, output: %s", err, string(out))
	}

	outStr := string(out)
	if !strings.Contains(outStr, "lovecode") {
		t.Fatalf("expected output to contain 'lovecode', got: %s", outStr)
	}
}

func TestCLI_BackupFlag(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	backupDir := t.TempDir()
	cfgPath := filepath.Join(backupDir, "backup_test.toml")
	backupFile := filepath.Join(backupDir, "backup.db")

	cfgContent := fmt.Sprintf(`
data_dir = %q

[server]
mode = "local"
listen = "127.0.0.1:0"

[logging]
level = "info"
format = "text"
`, backupDir)

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cmd := exec.CommandContext(ctx, binaryPath, "-config", cfgPath, "-backup", backupFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("-backup failed: %v, output: %s", err, string(out))
	}

	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		t.Fatalf("expected backup file to be created at %s", backupFile)
	}
}

func TestCLI_GracefulShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.toml")
	dbPath := filepath.Join(tmpDir, "cli_test.db")

	cfgContent := fmt.Sprintf(`
data_dir = %q

[server]
mode = "local"
listen = "127.0.0.1:0"

[logging]
level = "info"
format = "text"
`, tmpDir)

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "-config", cfgPath)
	cmd.Env = append(os.Environ(), "LOVECODE_DB_PATH="+dbPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start server process: %v", err)
	}

	// Wait briefly for server to bind and start
	time.Sleep(500 * time.Millisecond)

	// Send SIGTERM for graceful shutdown
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM: %v", err)
	}

	waitErr := cmd.Wait()
	if waitErr != nil && !strings.Contains(waitErr.Error(), "signal: terminated") && !strings.Contains(waitErr.Error(), "signal: interrupt") {
		// Normal exit from signal handling should be clean exit 0 or signal termination
		t.Logf("Process terminated with: %v (stdout: %s, stderr: %s)", waitErr, stdout.String(), stderr.String())
	}
}

func TestCLI_InvalidFlag(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "-invalid_flag_xyz")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error for invalid flag, but got exit code 0; output: %s", string(out))
	}
}

func TestCLI_InvalidConfigPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "-config", "/nonexistent/path/to/lovecode_config.toml")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error for nonexistent config file, but got exit code 0; output: %s", string(out))
	}
}

func TestCLI_HarnessIntegration(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.Client.Get(ctx, "/v1/health")
	if err != nil {
		t.Fatalf("GET /v1/health failed: %v", err)
	}
	srv.Client.AssertStatus(t, resp, http.StatusOK)
}
