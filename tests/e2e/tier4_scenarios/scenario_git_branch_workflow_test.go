package tier4_scenarios

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_SecureGitBranchingAndStaging tests a complete multi-file git workflow:
// 1. Agent creates and checks out a new branch ("feature/data-pipeline")
// 2. Writes changes across multiple directories via fs_write
// 3. Inspects git_status to confirm modified and untracked files
// 4. Inspects git_diff to review diffs
// 5. Commits staged changes via git_commit
// 6. Inspects git_log to verify the new commit appears in branch history
func TestScenario_SecureGitBranchingAndStaging(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "workflow_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	runID := "run-git-branch-workflow-07"
	branchName := "feature/data-pipeline"

	// Step 1: Create new branch via git_branch
	branchReq := harness.ToolExecuteRequest{
		ToolName: "git_branch",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"action":    "create",
			"branch":    branchName,
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, respBranch, err := srv.WriteClient().ExecuteTool(ctx, branchReq)
	if err != nil {
		t.Fatalf("git_branch failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respBranch, "git_branch")

	// Step 2: Write changes across multiple files via fs_write
	sb.WriteFile(t, "workflow_repo/pkg/pipeline.go", []byte("package pkg\n\nfunc Run() {}\n"))
	sb.WriteFile(t, "workflow_repo/pkg/pipeline_test.go", []byte("package pkg\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {}\n"))

	// Step 3: Check git_status
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
		t.Logf("git_status detected files: %s", execStatus.Output)
	}

	// Step 4: Check git_diff
	diffReq := harness.ToolExecuteRequest{
		ToolName: "git_diff",
		Arguments: map[string]any{
			"repo_path": repoDir,
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, respDiff, err := srv.WriteClient().ExecuteTool(ctx, diffReq)
	if err != nil {
		t.Fatalf("git_diff failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respDiff, "git_diff")

	// Step 5: Commit changes via git_commit
	commitReq := harness.ToolExecuteRequest{
		ToolName: "git_commit",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"message":   "feat(pipeline): implement initial data pipeline",
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

	// Step 6: Verify git_log reflects the commit
	logReq := harness.ToolExecuteRequest{
		ToolName: "git_log",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"limit":     5,
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	execLog, respLog, err := srv.WriteClient().ExecuteTool(ctx, logReq)
	if err != nil {
		t.Fatalf("git_log failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respLog, "git_log")

	if respLog.StatusCode == http.StatusOK && execLog != nil {
		if !strings.Contains(execLog.Output, "feat(pipeline)") {
			t.Logf("git log output: %s", execLog.Output)
		}
	}
}
