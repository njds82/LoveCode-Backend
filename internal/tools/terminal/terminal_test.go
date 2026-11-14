package terminal

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTerminal_AllowlistBinary_Executes(t *testing.T) {
	runner := NewRunner(DefaultConfig())
	ctx := context.Background()

	out, _, err := runner.Run(ctx, "echo", []string{"hello_terminal"}, "", nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(out, "hello_terminal") {
		t.Errorf("expected output to contain hello_terminal, got %q", out)
	}
}

func TestTerminal_DisallowedBinary_Rejected(t *testing.T) {
	runner := NewRunner(DefaultConfig())
	ctx := context.Background()

	disallowed := []string{"sudo", "bash", "sh", "zsh", "curl", "python", "python3", "nc"}
	for _, bin := range disallowed {
		t.Run(bin, func(t *testing.T) {
			_, _, err := runner.Run(ctx, bin, []string{"--version"}, "", nil)
			if err == nil {
				t.Fatalf("expected error for disallowed binary %q, got nil", bin)
			}
			if !errors.Is(err, ErrPermissionDenied) {
				t.Errorf("expected ErrPermissionDenied, got: %v", err)
			}
		})
	}
}

func TestTerminal_PathSeparators_Rejected(t *testing.T) {
	runner := NewRunner(DefaultConfig())
	ctx := context.Background()

	invalidExecutables := []string{"/bin/echo", "./echo", "../echo", "subdir/echo"}
	for _, exe := range invalidExecutables {
		t.Run(exe, func(t *testing.T) {
			_, _, err := runner.Run(ctx, exe, nil, "", nil)
			if err == nil {
				t.Fatalf("expected error for executable with path separators %q, got nil", exe)
			}
			if !errors.Is(err, ErrPermissionDenied) {
				t.Errorf("expected ErrPermissionDenied, got: %v", err)
			}
		})
	}
}

func TestTerminal_NoShellInterpolation_MetacharsLiteral(t *testing.T) {
	runner := NewRunner(DefaultConfig())
	ctx := context.Background()

	metacharArgs := []string{
		"safe; echo injected",
		"safe | cat /etc/passwd",
		"safe && whoami",
		"safe $(id)",
		"safe > /tmp/out.txt",
	}

	for _, arg := range metacharArgs {
		t.Run(arg, func(t *testing.T) {
			out, _, err := runner.Run(ctx, "echo", []string{arg}, "", nil)
			if err != nil {
				t.Fatalf("execute failed: %v", err)
			}
			if !strings.Contains(out, arg) {
				t.Errorf("expected output to contain literal argument %q, got %q", arg, out)
			}
		})
	}
}

func TestTerminal_CleanEnvironment_PurgesSecrets(t *testing.T) {
	os.Setenv("LOVECODE_ADMIN_TOKEN", "lc_admin_super_secret_token_12345")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "AKIAIOSFODNN7EXAMPLE_SECRET")
	os.Setenv("GITHUB_PAT_TOKEN", "ghp_1234567890abcdef1234567890abcdef")
	defer func() {
		os.Unsetenv("LOVECODE_ADMIN_TOKEN")
		os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		os.Unsetenv("GITHUB_PAT_TOKEN")
	}()

	runner := NewRunner(DefaultConfig())
	ctx := context.Background()

	out, _, err := runner.Run(ctx, "env", nil, "", nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if strings.Contains(out, "lc_admin_super_secret") {
		t.Errorf("secret leaked in terminal env: %s", out)
	}
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("secret leaked in terminal env: %s", out)
	}
	if strings.Contains(out, "ghp_1234567890") {
		t.Errorf("secret leaked in terminal env: %s", out)
	}
}

func TestTerminal_ProcessGroup_SigkillOnTimeout(t *testing.T) {
	runner := NewRunner(DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := runner.Run(ctx, "sleep", []string{"5"}, "", nil)
	duration := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error for sleep 5 with 100ms timeout, got nil")
	}
	if !errors.Is(err, ErrExecutionTimeout) {
		t.Errorf("expected ErrExecutionTimeout, got: %v", err)
	}
	if duration > 3*time.Second {
		t.Errorf("timeout took too long (%v), process group SIGKILL might have failed", duration)
	}
}

func TestTerminal_EmptyExecutable_Rejected(t *testing.T) {
	runner := NewRunner(DefaultConfig())
	ctx := context.Background()

	for _, exe := range []string{"", "  ", "\t\n"} {
		_, _, err := runner.Run(ctx, exe, nil, "", nil)
		if err == nil {
			t.Errorf("expected error for empty executable %q, got nil", exe)
		}
		if !errors.Is(err, ErrSchemaValidation) {
			t.Errorf("expected ErrSchemaValidation, got: %v", err)
		}
	}
}
