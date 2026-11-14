package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/lovecode/lovecode/internal/tools/filesystem"
)

// Config controls git tool allowed roots and boundaries.
type Config struct {
	AllowedRoots []string
}

// DefaultConfig returns git configuration default boundaries.
func DefaultConfig() Config {
	cwd, _ := os.Getwd()
	roots := []string{}
	if cwd != "" {
		roots = append(roots, cwd)
	}
	return Config{
		AllowedRoots: roots,
	}
}

// Runner handles safe git command execution.
type Runner struct {
	cfg Config
}

func NewRunner(cfg Config) *Runner {
	return &Runner{cfg: cfg}
}

// ExecGit executes git safely within the target repository directory.
func (r *Runner) ExecGit(ctx context.Context, repoDir string, args ...string) (string, error) {
	// 1. Path confinement
	safePath, err := filesystem.CheckPathSecurity(repoDir, r.cfg.AllowedRoots)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(safePath)
	if err != nil {
		return "", fmt.Errorf("%w: repository path %q error: %v", ErrSchemaValidation, safePath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: repository path %q is not a directory", ErrSchemaValidation, safePath)
	}

	// 2. Validate git repository
	chk := exec.CommandContext(ctx, "git", "-C", safePath, "rev-parse", "--is-inside-work-tree")
	chk.Env = []string{"GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "PATH=" + os.Getenv("PATH")}
	if err := chk.Run(); err != nil {
		return "", fmt.Errorf("%w: %s is not inside a git work tree", ErrNotAGitRepo, safePath)
	}

	// 3. Command execution with safe environment
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("%w: git binary not found in PATH", ErrToolNotFound)
	}

	cmdArgs := append([]string{"-C", safePath}, args...)
	cmd := exec.CommandContext(ctx, gitPath, cmdArgs...)
	cmd.Dir = safePath
	cmd.Env = []string{
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"LANG=C",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.TempDir(),
	}

	out, err := cmd.CombinedOutput()
	sanitized := SanitizeGitOutput(string(out))
	if err != nil {
		return sanitized, fmt.Errorf("git command failed: %s (%w)", sanitized, err)
	}
	return sanitized, nil
}
