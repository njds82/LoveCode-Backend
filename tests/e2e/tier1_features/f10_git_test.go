package tier1_features

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF10_Git_Status_CleanAndModified(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_status_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "git_status",
		Arguments: map[string]any{
			"repo_path": repoDir,
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F10: Git Tools")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("git_status output: %s", execResp.Output)
	}
}

func TestF10_Git_Diff_WorkingTreeChanges(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_diff_repo")

	// Modify file
	sb.WriteFile(t, "git_diff_repo/README.md", []byte("# Modified content for diff test\n"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "git_diff",
		Arguments: map[string]any{
			"repo_path": repoDir,
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F10: Git Tools")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("git_diff output: %s", execResp.Output)
	}
}

func TestF10_Git_Log_CommitHistory(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_log_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "git_log",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"limit":     5,
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F10: Git Tools")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("git_log output: %s", execResp.Output)
	}
}

func TestF10_Git_Branch_ListAndCreate(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_branch_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// List branches
	reqList := harness.ToolExecuteRequest{
		ToolName: "git_branch",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"action":    "list",
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, reqList)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F10: Git Tools")

	// Create branch
	reqCreate := harness.ToolExecuteRequest{
		ToolName: "git_branch",
		Arguments: map[string]any{
			"repo_path":   repoDir,
			"action":      "create",
			"branch_name": "feature-test",
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, respCreate, err := srv.WriteClient().ExecuteTool(ctx, reqCreate)
	if err != nil {
		t.Fatalf("execute create branch failed: %v", err)
	}
	if respCreate.StatusCode != http.StatusOK && respCreate.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected branch create status: %d", respCreate.StatusCode)
	}
}

func TestF10_Git_Commit_StagedChanges(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_commit_repo")

	// Add new file
	sb.WriteFile(t, "git_commit_repo/new_file.txt", []byte("content to commit"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "git_commit",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"message":   "feat: add new file",
			"all":       true,
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F10: Git Tools")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("git_commit output: %s", execResp.Output)
	}
}

func TestF10_Git_DestructiveOperations_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_destructive_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempting destructive checkout -f or reset --hard
	destructiveActions := []string{"reset_hard", "clean_fd", "push_force"}
	for _, action := range destructiveActions {
		req := harness.ToolExecuteRequest{
			ToolName: "git_" + action,
			Arguments: map[string]any{
				"repo_path": repoDir,
			},
			GrantedCapabilities: []string{"*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		srv.WriteClient().SkipIfUnregistered(t, resp, "F10: Git Tools")

		// Destructive tools must either not exist (404) or be forbidden (403)
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected destructive action %s to be blocked (403 or 404), got %d", action, resp.StatusCode)
		}
	}
}
