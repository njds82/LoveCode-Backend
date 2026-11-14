package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = []string{
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
			"PATH=" + os.Getenv("PATH"),
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s (%v)", args, string(out), err)
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.com")

	initialFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(initialFile, []byte("# Initial commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	return dir
}

func TestGitTools_Lifecycle(t *testing.T) {
	repoDir := initTempRepo(t)
	cfg := Config{AllowedRoots: []string{repoDir}}

	statusTool := NewStatusTool(cfg)
	diffTool := NewDiffTool(cfg)
	logTool := NewLogTool(cfg)
	branchTool := NewBranchTool(cfg)
	commitTool := NewCommitTool(cfg)

	ctx := context.Background()

	// 1. Initial status should be clean
	_, st, err := statusTool.Status(ctx, repoDir, "")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !st.Clean {
		t.Errorf("expected clean repo, got: %+v", st)
	}

	// 2. Modify file and check diff
	readmePath := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Modified content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diffOut, err := diffTool.Diff(ctx, repoDir, "", false, "", "")
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	if !strings.Contains(diffOut, "Modified content") {
		t.Errorf("expected diff to contain 'Modified content', got: %s", diffOut)
	}

	// 3. Commit with all=true
	_, err = commitTool.Commit(ctx, repoDir, "", "feat: update readme", true, nil, "", "")
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// 4. Log should contain the new commit
	_, commits, err := logTool.Log(ctx, repoDir, "", 5, 0, "", "")
	if err != nil {
		t.Fatalf("log failed: %v", err)
	}
	if len(commits) < 2 {
		t.Errorf("expected at least 2 commits, got: %+v", commits)
	}
	if commits[0].Subject != "feat: update readme" {
		t.Errorf("expected latest commit 'feat: update readme', got %q", commits[0].Subject)
	}

	// 5. Branch create and switch
	_, _, err = branchTool.Branch(ctx, repoDir, "", "create", "feature-1", "")
	if err != nil {
		t.Fatalf("branch create failed: %v", err)
	}

	_, _, err = branchTool.Branch(ctx, repoDir, "", "switch", "feature-1", "")
	if err != nil {
		t.Fatalf("branch switch failed: %v", err)
	}

	_, data, err := branchTool.Branch(ctx, repoDir, "", "list", "", "")
	if err != nil {
		t.Fatalf("branch list failed: %v", err)
	}
	bList, ok := data.(BranchListResult)
	if !ok || bList.Current != "feature-1" {
		t.Errorf("expected current branch 'feature-1', got: %+v", data)
	}
}

func TestGitTools_DestructiveGuardrails(t *testing.T) {
	repoDir := initTempRepo(t)
	cfg := Config{AllowedRoots: []string{repoDir}}
	diffTool := NewDiffTool(cfg)

	ctx := context.Background()

	dangerousTargets := []string{
		"--exec-path",
		"-c",
		"--hard",
		"-fd",
	}

	for _, target := range dangerousTargets {
		t.Run("Diff_"+target, func(t *testing.T) {
			_, err := diffTool.Diff(ctx, repoDir, "", false, target, "")
			if err == nil {
				t.Fatalf("expected error for dangerous argument %q, got nil", target)
			}
		})
	}
}

func TestGitTools_CredentialRedaction(t *testing.T) {
	rawOutput := "remote: https://ghp_0123456789abcdef0123456789abcdef0123@github.com/org/repo.git\n" +
		"pushing to https://user:secretpassword@git.example.com/repo.git\n"

	sanitized := SanitizeGitOutput(rawOutput)
	if strings.Contains(sanitized, "ghp_0123456789abcdef") {
		t.Errorf("token credential leaked: %s", sanitized)
	}
	if strings.Contains(sanitized, "secretpassword") {
		t.Errorf("user password credential leaked: %s", sanitized)
	}
	if !strings.Contains(sanitized, "[REDACTED]@github.com") {
		t.Errorf("expected [REDACTED]@github.com, got: %s", sanitized)
	}
	if !strings.Contains(sanitized, "[REDACTED]@git.example.com") {
		t.Errorf("expected [REDACTED]@git.example.com, got: %s", sanitized)
	}
}

func TestGitTools_NotAGitRepo_Fails(t *testing.T) {
	notRepo := t.TempDir()
	cfg := Config{AllowedRoots: []string{notRepo}}
	statusTool := NewStatusTool(cfg)

	_, _, err := statusTool.Status(context.Background(), notRepo, "")
	if err == nil {
		t.Fatal("expected error for non-git repository, got nil")
	}
	if !errors.Is(err, ErrNotAGitRepo) {
		t.Errorf("expected ErrNotAGitRepo, got: %v", err)
	}
}
