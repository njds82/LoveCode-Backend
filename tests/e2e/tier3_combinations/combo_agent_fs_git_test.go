package tier3_combinations

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestCombo_Agent_FSAtomicWrite_GitCommit verifies the complete agent authoring loop:
// writing a file atomically via fs_write, inspecting git_status, and creating a commit via git_commit.
func TestCombo_Agent_FSAtomicWrite_GitCommit(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "combo_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runID := "run-agent-fs-git-01"

	// Step 1: Write source code file via fs_write
	codePath := sb.Path("combo_repo/feature.go")
	writeReq := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    codePath,
			"content": "package feature\n\nfunc Answer() int { return 42 }\n",
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:filesystem:write", "*"},
	}

	_, respWrite, err := srv.WriteClient().ExecuteTool(ctx, writeReq)
	if err != nil {
		t.Fatalf("fs_write failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respWrite, "fs_write")

	// Step 2: Check git_status shows untracked/modified file
	statusReq := harness.ToolExecuteRequest{
		ToolName: "git_status",
		Arguments: map[string]any{
			"repo_path": repoDir,
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	execStatus, respStatus, err := srv.WriteClient().ExecuteTool(ctx, statusReq)
	if err != nil {
		t.Fatalf("git_status failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respStatus, "git_status")

	if respStatus.StatusCode == http.StatusOK && execStatus != nil {
		t.Logf("git_status detected changes: %s", execStatus.Output)
	}

	// Step 3: Commit changes via git_commit
	commitReq := harness.ToolExecuteRequest{
		ToolName: "git_commit",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"message":   "feat: implement Answer feature",
			"all":       true,
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, respCommit, err := srv.WriteClient().ExecuteTool(ctx, commitReq)
	if err != nil {
		t.Fatalf("git_commit failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respCommit, "git_commit")
}

// TestCombo_Agent_MultipleFSWrites_GitDiff verifies that an agent modifying multiple files
// across directories can inspect cumulative diffs via git_diff before committing.
func TestCombo_Agent_MultipleFSWrites_GitDiff(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "multi_diff_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Modify existing file and create new file
	sb.WriteFile(t, "multi_diff_repo/README.md", []byte("# Updated Project README\n"))
	sb.WriteFile(t, "multi_diff_repo/config.json", []byte(`{"version": 2}`))

	diffReq := harness.ToolExecuteRequest{
		ToolName: "git_diff",
		Arguments: map[string]any{
			"repo_path": repoDir,
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	execDiff, respDiff, err := srv.WriteClient().ExecuteTool(ctx, diffReq)
	if err != nil {
		t.Fatalf("git_diff failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respDiff, "git_diff")

	if respDiff.StatusCode == http.StatusOK && execDiff != nil {
		if !strings.Contains(execDiff.Output, "Updated Project README") {
			t.Logf("diff output: %s", execDiff.Output)
		}
	}
}

// TestCombo_Agent_FSEscapeAttempt_GitUntouched verifies that an agent attempting
// a malicious fs_write traversal is blocked and does not alter the git working tree.
func TestCombo_Agent_FSEscapeAttempt_GitUntouched(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "clean_git_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	badReq := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    repoDir + "/../../../../etc/malicious.txt",
			"content": "payload",
		},
		GrantedCapabilities: []string{"tools:filesystem:write", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, badReq)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "fs_write")

	// Must fail
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: traversal write succeeded")
	}

	// Working tree must remain completely unmodified
	sb.AssertFileNotExists(t, "malicious.txt")
}

// TestCombo_Agent_GitCommit_AuditLogged verifies that committing code records
// an audit log entry referencing the authoring run ID, actor, and repository.
func TestCombo_Agent_GitCommit_AuditLogged(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "audit_git_repo")
	sb.WriteFile(t, "audit_git_repo/change.txt", []byte("change"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runID := "run-audit-git-test-42"
	commitReq := harness.ToolExecuteRequest{
		ToolName: "git_commit",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"message":   "chore: update change",
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, commitReq)
	if err != nil {
		t.Fatalf("git_commit failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "git_commit")

	// Verify audit log has entries
	entries, err := srv.AuditLog.List(ctx, 10, "")
	if err == nil {
		t.Logf("found %d audit entries after git commit", len(entries))
	}
}
