package sysinfo

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestSysinfo_ReportsMetrics(t *testing.T) {
	collector := NewCollector()

	output, info, err := collector.Collect("/")
	if err != nil {
		t.Fatalf("sysinfo execution failed: %v", err)
	}

	if output == "" {
		t.Fatalf("expected non-empty sysinfo output")
	}

	if info.OS != runtime.GOOS {
		t.Errorf("expected OS %s, got %s", runtime.GOOS, info.OS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("expected Arch %s, got %s", runtime.GOARCH, info.Arch)
	}
	if info.CPU.Cores != runtime.NumCPU() {
		t.Errorf("expected CPU cores %d, got %d", runtime.NumCPU(), info.CPU.Cores)
	}
	if info.Memory.TotalBytes == 0 {
		t.Errorf("expected non-zero total memory")
	}
	if info.Disk.TotalBytes == 0 {
		t.Errorf("expected non-zero disk capacity")
	}
	if info.UptimeSeconds < 0 {
		t.Errorf("expected non-negative uptime")
	}
}

func TestSysinfo_NoSecretOrEnvLeaks(t *testing.T) {
	collector := NewCollector()

	// Inject test secrets into host environment
	os.Setenv("SECRET_API_KEY_12345", "test_secret_value_abcdef")
	os.Setenv("LOVECODE_TEST_TOKEN", "lc_admin_test_token_xyz")
	defer func() {
		os.Unsetenv("SECRET_API_KEY_12345")
		os.Unsetenv("LOVECODE_TEST_TOKEN")
	}()

	output, info, err := collector.Collect("/")
	if err != nil {
		t.Fatalf("sysinfo execution failed: %v", err)
	}

	if strings.Contains(output, "test_secret_value_abcdef") || strings.Contains(output, "SECRET_API_KEY_12345") {
		t.Errorf("secret environment variable leaked in sysinfo output: %s", output)
	}
	if strings.Contains(output, "lc_admin_test_token_xyz") || strings.Contains(output, "LOVECODE_TEST_TOKEN") {
		t.Errorf("token leaked in sysinfo output: %s", output)
	}

	// Verify data json doesn't contain env vars
	dataJSON, _ := json.Marshal(info)
	if strings.Contains(string(dataJSON), "test_secret_value_abcdef") {
		t.Errorf("secret leaked in sysinfo Data: %s", string(dataJSON))
	}
}

func TestSysinfo_CustomMountPath(t *testing.T) {
	collector := NewCollector()

	// Valid mount path
	_, info, err := collector.Collect(".")
	if err != nil {
		t.Fatalf("execute with mount_path '.' failed: %v", err)
	}
	if info.Disk.MountPoint != "." {
		t.Errorf("expected mount_point '.', got %s", info.Disk.MountPoint)
	}

	// Non-existent mount path
	_, _, err = collector.Collect("/path/that/does/not/exist/ever")
	if err == nil {
		t.Fatal("expected error for non-existent mount_path, got nil")
	}
	if !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("expected ErrSchemaValidation, got: %v", err)
	}
}
