package tier2_boundaries

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestBoundaries_Git_Destructive_ResetHard_Blocked verifies that destructive
// reset operations (such as reset --hard) are blocked.
func TestBoundaries_Git_Destructive_ResetHard_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_reset_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	destructiveOps := []map[string]any{
		{"repo_path": repoDir, "command": "reset", "args": []string{"--hard", "HEAD~1"}},
		{"repo_path": repoDir, "mode": "hard"},
	}

	for _, args := range destructiveOps {
		req := harness.ToolExecuteRequest{
			ToolName:            "git_reset",
			Arguments:           args,
			GrantedCapabilities: []string{"tools:git", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "git_reset")

		if resp.StatusCode == http.StatusOK {
			t.Fatalf("security violation: git reset --hard was permitted, got 200 OK")
		}
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 403 or 400 for destructive git reset, got %d: %s", resp.StatusCode, resp.String())
		}
	}
}

// TestBoundaries_Git_Destructive_CleanForce_Blocked verifies that git clean -fd
// or untracked file destruction operations are blocked.
func TestBoundaries_Git_Destructive_CleanForce_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_clean_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "git_clean",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"force":     true,
			"args":      []string{"-fd"},
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "git_clean")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: git clean -fd was permitted, got 200 OK")
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 400 for git clean -fd, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Git_Destructive_PushForce_Blocked verifies that force push
// (push --force or --force-with-lease) operations are blocked.
func TestBoundaries_Git_Destructive_PushForce_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_push_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "git_push",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"force":     true,
			"remote":    "origin",
			"branch":    "main",
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "git_push")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: git push --force was permitted, got 200 OK")
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 400 for git push --force, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Git_Destructive_CheckoutForce_Blocked verifies that force checkout
// (checkout -f) discarding uncommitted changes is blocked.
func TestBoundaries_Git_Destructive_CheckoutForce_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_checkout_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "git_checkout",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"branch":    "main",
			"force":     true,
			"args":      []string{"-f"},
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "git_checkout")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: git checkout -f was permitted, got 200 OK")
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 400 for git checkout -f, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Git_Destructive_BranchDeleteForce_Blocked verifies that force-deleting
// branches with unmerged work (branch -D) is blocked.
func TestBoundaries_Git_Destructive_BranchDeleteForce_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_branch_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "git_branch",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"action":    "delete",
			"branch":    "feature-branch",
			"force":     true,
			"args":      []string{"-D"},
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "git_branch")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: branch -D was permitted, got 200 OK")
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 400 for branch -D, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Git_RemoteURL_CredentialRedaction verifies that credentials embedded
// in remote URLs (https://user:pass@host/repo.git) are redacted from logs and tool outputs.
func TestBoundaries_Git_RemoteURL_CredentialRedaction(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "git_remote_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	secretURL := "https://service_user:superSecretPassword123@github.com/org/private-repo.git"

	req := harness.ToolExecuteRequest{
		ToolName: "git_remote",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"action":    "add",
			"name":      "origin",
			"url":       secretURL,
		},
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "git_remote")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		// Verify secret password does not appear in plain text
		if strings.Contains(execResp.Output, "superSecretPassword123") {
			t.Fatalf("security violation: plain-text password leaked in git remote output: %s", execResp.Output)
		}
	}
}
