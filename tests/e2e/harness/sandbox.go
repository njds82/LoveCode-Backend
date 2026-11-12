package harness

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Sandbox provides a safe, isolated directory structure for filesystem
// and git tool testing, evaluating canonical symlink paths.
type Sandbox struct {
	root string
}

// NewSandbox creates a new Sandbox rooted in a temporary directory.
func NewSandbox(t testing.TB) *Sandbox {
	t.Helper()
	dir := t.TempDir()

	// Evaluate canonical symlink path to avoid false positive escape detections
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		canonical = dir
	}

	return &Sandbox{
		root: canonical,
	}
}

// Root returns the canonical root directory of this sandbox.
func (s *Sandbox) Root() string {
	return s.root
}

// Path returns an absolute path joined with the sandbox root.
func (s *Sandbox) Path(relParts ...string) string {
	parts := append([]string{s.root}, relParts...)
	return filepath.Join(parts...)
}

// WriteFile writes a file at the given relative path with parent directories created.
func (s *Sandbox) WriteFile(t testing.TB, relPath string, content []byte) string {
	t.Helper()
	fullPath := s.Path(relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("Sandbox.WriteFile: mkdir failed: %v", err)
	}
	if err := os.WriteFile(fullPath, content, 0o644); err != nil {
		t.Fatalf("Sandbox.WriteFile: write file failed: %v", err)
	}
	return fullPath
}

// ReadFile reads a file at the given relative path and returns its content as string.
func (s *Sandbox) ReadFile(t testing.TB, relPath string) string {
	t.Helper()
	fullPath := s.Path(relPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("Sandbox.ReadFile: read file failed: %v", err)
	}
	return string(data)
}

// InitGitRepo initializes a git repository at the given subdirectory.
// It sets local user info, writes a README.md, and creates an initial commit.
func (s *Sandbox) InitGitRepo(t testing.TB, relSubdir string) string {
	t.Helper()
	repoDir := s.Path(relSubdir)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("Sandbox.InitGitRepo: mkdir failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runGit := func(args ...string) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %s failed: %v, stderr: %s", strings.Join(args, " "), err, stderr.String())
		}
	}

	runGit("init")
	runGit("config", "user.name", "LoveCode Test")
	runGit("config", "user.email", "test@lovecode.local")
	runGit("config", "commit.gpgsign", "false")

	// Create initial file and commit
	initialFile := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(initialFile, []byte("# Test Repo\nInitial content\n"), 0o644); err != nil {
		t.Fatalf("Sandbox.InitGitRepo: write README failed: %v", err)
	}

	runGit("add", "README.md")
	runGit("commit", "-m", "Initial commit")

	return repoDir
}

// AssertFileEquals verifies that the file at relPath contains the expected string.
func (s *Sandbox) AssertFileEquals(t testing.TB, relPath string, expected string) {
	t.Helper()
	actual := s.ReadFile(t, relPath)
	if actual != expected {
		t.Fatalf("file %s content mismatch:\nexpected: %q\ngot:      %q", relPath, expected, actual)
	}
}

// AssertFileExists verifies that a file exists at relPath.
func (s *Sandbox) AssertFileExists(t testing.TB, relPath string) {
	t.Helper()
	fullPath := s.Path(relPath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Fatalf("expected file to exist at %s, but it does not", relPath)
	}
}

// AssertFileNotExists verifies that a file does NOT exist at relPath.
func (s *Sandbox) AssertFileNotExists(t testing.TB, relPath string) {
	t.Helper()
	fullPath := s.Path(relPath)
	if _, err := os.Stat(fullPath); err == nil {
		t.Fatalf("expected file NOT to exist at %s, but it exists", relPath)
	}
}
