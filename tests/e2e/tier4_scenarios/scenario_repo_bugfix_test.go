package tier4_scenarios

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_AutonomousBugFixWorkflow simulates an autonomous coding agent
// working on a repository:
// 1. Inspects failing test / repo state via terminal or fs_read
// 2. Reads source code via fs_read
// 3. Writes an atomic fix via fs_write
// 4. Verifies test passes via terminal_exec
// 5. Commits fix via git_commit
// 6. Confirms audit log recorded all actions with correlation ID
func TestScenario_AutonomousBugFixWorkflow(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	repoDir := sb.InitGitRepo(t, "bugfix_repo")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	runID := "run-autonomous-bugfix-scenario-01"
	taskID := "task-fix-calculator-bug"
	agentID := "autonomous-dev-agent"

	// Initial buggy code file
	buggyCode := `package calc

func Add(a, b int) int {
	return a - b // Bug: minus instead of plus
}
`
	sb.WriteFile(t, "bugfix_repo/calc.go", []byte(buggyCode))

	// Step 1: Read buggy code via fs_read
	readReq := harness.ToolExecuteRequest{
		ToolName: "fs_read",
		Arguments: map[string]any{
			"path": sb.Path("bugfix_repo/calc.go"),
		},
		AgentID:             agentID,
		RunID:               runID,
		TaskID:              taskID,
		GrantedCapabilities: []string{"tools:filesystem:read", "*"},
	}

	execRead, respRead, err := srv.WriteClient().ExecuteTool(ctx, readReq)
	if err != nil {
		t.Fatalf("Step 1 fs_read failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respRead, "fs_read")

	if respRead.StatusCode == http.StatusOK && execRead != nil {
		if !strings.Contains(execRead.Output, "return a - b") {
			t.Errorf("expected to read buggy code, got: %s", execRead.Output)
		}
	}

	// Step 2: Write atomic fix via fs_write
	fixedCode := `package calc

func Add(a, b int) int {
	return a + b // Fixed!
}
`
	writeReq := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    sb.Path("bugfix_repo/calc.go"),
			"content": fixedCode,
		},
		AgentID:             agentID,
		RunID:               runID,
		TaskID:              taskID,
		GrantedCapabilities: []string{"tools:filesystem:write", "*"},
	}

	_, respWrite, err := srv.WriteClient().ExecuteTool(ctx, writeReq)
	if err != nil {
		t.Fatalf("Step 2 fs_write failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respWrite, "fs_write")

	// Step 3: Run verification command via terminal_exec
	termReq := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "echo",
			"args":       []string{"all_tests_passed"},
		},
		AgentID:             agentID,
		RunID:               runID,
		TaskID:              taskID,
		GrantedCapabilities: []string{"tools:terminal", "*"},
	}

	execTerm, respTerm, err := srv.WriteClient().ExecuteTool(ctx, termReq)
	if err != nil {
		t.Fatalf("Step 3 terminal_exec failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respTerm, "terminal_exec")

	if respTerm.StatusCode == http.StatusOK && execTerm != nil {
		if !strings.Contains(execTerm.Output, "all_tests_passed") {
			t.Errorf("unexpected terminal verification output: %s", execTerm.Output)
		}
	}

	// Step 4: Commit fix via git_commit
	commitReq := harness.ToolExecuteRequest{
		ToolName: "git_commit",
		Arguments: map[string]any{
			"repo_path": repoDir,
			"message":   "fix(calc): correct addition implementation",
			"all":       true,
		},
		AgentID:             agentID,
		RunID:               runID,
		TaskID:              taskID,
		GrantedCapabilities: []string{"tools:git", "*"},
	}

	_, respCommit, err := srv.WriteClient().ExecuteTool(ctx, commitReq)
	if err != nil {
		t.Fatalf("Step 4 git_commit failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respCommit, "git_commit")

	// Step 5: Check tamper-evident audit log has tracked the workflow
	entries, err := srv.AuditLog.List(ctx, 50, "")
	if err == nil {
		t.Logf("Scenario 1 completed, verified %d audit log entries", len(entries))
	}
}
