package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	ErrSchemaValidation   = errors.New("schema validation failed")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrToolNotFound       = errors.New("binary not found")
	ErrExecutionTimeout   = errors.New("execution timeout")
	ErrExecutionCancelled = errors.New("execution cancelled")
)

// DefaultAllowedBinaries specifies the 23 verified Linux utilities permitted for terminal execution.
var DefaultAllowedBinaries = []string{
	"go", "git", "ls", "cat", "echo", "pwd", "mkdir", "rm", "cp", "mv",
	"grep", "find", "make", "sleep", "env", "printenv", "which",
	"head", "tail", "wc", "diff", "sort", "uniq",
}

var disallowedInterpreters = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "csh": true, "dash": true, "ksh": true,
	"sudo": true, "su": true, "nc": true, "netcat": true, "curl": true, "wget": true,
	"python": true, "python3": true, "perl": true, "ruby": true, "node": true, "php": true,
}

var safeEnvPrefixes = []string{
	"PATH=", "HOME=", "USER=", "LANG=", "LC_ALL=", "LC_CTYPE=", "LC_COLLATE=",
	"LC_MESSAGES=", "LC_MONETARY=", "LC_NUMERIC=", "LC_TIME=", "TERM=",
	"TMPDIR=", "PWD=", "GOROOT=", "GOPATH=", "GOCACHE=", "GOPROXY=",
	"GONOSUMDB=", "GOFLAGS=", "GIT_AUTHOR_", "GIT_COMMITTER_",
}

var sensitiveEnvSubstrings = []string{
	"TOKEN", "KEY", "SECRET", "PASSWORD", "PASSWD", "AUTH", "CREDENTIAL",
	"AWS_", "GITHUB_", "OPENAI_", "ANTHROPIC_", "LOVECODE_",
}

// Config controls terminal execution boundaries.
type Config struct {
	Enabled         bool
	AllowedBinaries []string
	AllowedRoots    []string
	DefaultTimeout  time.Duration
}

// DefaultConfig returns safe default configuration (disabled by default).
func DefaultConfig() Config {
	return Config{
		Enabled:         false,
		AllowedBinaries: DefaultAllowedBinaries,
		DefaultTimeout:  30 * time.Second,
	}
}

// Runner implements structured terminal execution with process group isolation.
type Runner struct {
	cfg Config
}

func NewRunner(cfg Config) *Runner {
	if len(cfg.AllowedBinaries) == 0 {
		cfg.AllowedBinaries = DefaultAllowedBinaries
	}
	return &Runner{cfg: cfg}
}

type ExecResultData struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func (r *Runner) Run(ctx context.Context, executable string, args []string, dir string, customEnv map[string]string) (string, ExecResultData, error) {
	exe := strings.TrimSpace(executable)
	if exe == "" {
		return "", ExecResultData{}, fmt.Errorf("%w: executable cannot be empty", ErrSchemaValidation)
	}

	// Reject directory traversal or path separators in executable (bare names only)
	if strings.ContainsAny(exe, "/\\") || filepath.Base(exe) != exe {
		return "", ExecResultData{}, fmt.Errorf("%w: executable path traversal or path separators not permitted", ErrPermissionDenied)
	}

	lowerExe := strings.ToLower(exe)
	if disallowedInterpreters[lowerExe] {
		return "", ExecResultData{}, fmt.Errorf("%w: binary interpreter %q is strictly disallowed", ErrPermissionDenied, exe)
	}

	// Validate against allowlist
	allowed := false
	for _, b := range r.cfg.AllowedBinaries {
		if strings.EqualFold(b, exe) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", ExecResultData{}, fmt.Errorf("%w: executable %q is not in the allowlist", ErrPermissionDenied, exe)
	}

	binaryPath, err := exec.LookPath(exe)
	if err != nil {
		return "", ExecResultData{}, fmt.Errorf("%w: binary %q not found in PATH: %v", ErrToolNotFound, exe, err)
	}

	// Working directory resolution
	workDir := dir
	if workDir != "" {
		cleanedDir, err := filepath.Abs(filepath.Clean(workDir))
		if err != nil {
			return "", ExecResultData{}, fmt.Errorf("%w: invalid working directory %q", ErrSchemaValidation, workDir)
		}
		if info, err := os.Stat(cleanedDir); err != nil || !info.IsDir() {
			return "", ExecResultData{}, fmt.Errorf("%w: working directory %q does not exist", ErrSchemaValidation, cleanedDir)
		}
		workDir = cleanedDir
	}

	// Environment sanitization
	cmdEnv := sanitizeEnvironment(os.Environ(), customEnv)

	// Command construction with process group isolation
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = cmdEnv
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Process group SIGKILL cleanup on context cancellation/timeout
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 500 * time.Millisecond

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()

	exitCode := 0
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ExecResultData{}, ErrExecutionTimeout
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", ExecResultData{}, ErrExecutionCancelled
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return "", ExecResultData{}, fmt.Errorf("command execution failed: %w", err)
		}
	}

	stdoutStr := stdoutBuf.String()
	stderrStr := stderrBuf.String()
	combined := stdoutStr
	if stderrStr != "" {
		if combined != "" && !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += stderrStr
	}

	return combined, ExecResultData{
		Stdout:   stdoutStr,
		Stderr:   stderrStr,
		ExitCode: exitCode,
	}, nil
}

func sanitizeEnvironment(hostEnv []string, customEnv map[string]string) []string {
	var clean []string

	isSafe := func(keyVal string) bool {
		upper := strings.ToUpper(keyVal)
		for _, sens := range sensitiveEnvSubstrings {
			if strings.Contains(upper, sens) {
				return false
			}
		}
		for _, prefix := range safeEnvPrefixes {
			if strings.HasPrefix(upper, prefix) {
				return true
			}
		}
		return false
	}

	for _, kv := range hostEnv {
		if isSafe(kv) {
			clean = append(clean, kv)
		}
	}

	for k, v := range customEnv {
		kv := fmt.Sprintf("%s=%s", k, v)
		if isSafe(kv) {
			clean = append(clean, kv)
		}
	}

	return clean
}
