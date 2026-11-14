package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lovecode/lovecode/internal/tools/git"
)

// --- Git Status Tool ---

type GitStatusTool struct {
	tool *git.StatusTool
}

func NewGitStatusTool(cfg git.Config) *GitStatusTool {
	return &GitStatusTool{tool: git.NewStatusTool(cfg)}
}

func (t *GitStatusTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "git_status",
		Name:        "git_status",
		Description: "Show working tree status and branch divergence in a git repository.",
		Category:    CategoryGit,
		RiskLevel:   RiskNone,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_path": {"type": "string", "description": "Path to the git repository"},
				"path": {"type": "string", "description": "Alias for repo_path"}
			}
		}`),
		RequiredCapabilities: []string{"tools:git"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type gitStatusArgs struct {
	RepoPath string `json:"repo_path"`
	Path     string `json:"path"`
}

func (t *GitStatusTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a gitStatusArgs
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	out, res, err := t.tool.Status(ctx, a.RepoPath, a.Path)
	if err != nil {
		return nil, err
	}
	return &ExecutionResult{
		ToolName: "git_status",
		Output:   out,
		Data:     res,
	}, nil
}

// --- Git Diff Tool ---

type GitDiffTool struct {
	tool *git.DiffTool
}

func NewGitDiffTool(cfg git.Config) *GitDiffTool {
	return &GitDiffTool{tool: git.NewDiffTool(cfg)}
}

func (t *GitDiffTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "git_diff",
		Name:        "git_diff",
		Description: "Show changes between commits, commit and working tree, etc.",
		Category:    CategoryGit,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_path": {"type": "string", "description": "Path to the git repository"},
				"path": {"type": "string", "description": "Alias for repo_path"},
				"staged": {"type": "boolean", "description": "Show staged changes (--staged/--cached)"},
				"file": {"type": "string", "description": "Restrict diff to specific file"}
			}
		}`),
		RequiredCapabilities: []string{"tools:git"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type gitDiffArgs struct {
	RepoPath string `json:"repo_path"`
	Path     string `json:"path"`
	Staged   bool   `json:"staged"`
	File     string `json:"file"`
}

func (t *GitDiffTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a gitDiffArgs
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	out, err := t.tool.Diff(ctx, a.RepoPath, a.Path, a.Staged, a.File, "")
	if err != nil {
		return nil, err
	}
	return &ExecutionResult{
		ToolName: "git_diff",
		Output:   out,
	}, nil
}

// --- Git Log Tool ---

type GitLogTool struct {
	tool *git.LogTool
}

func NewGitLogTool(cfg git.Config) *GitLogTool {
	return &GitLogTool{tool: git.NewLogTool(cfg)}
}

func (t *GitLogTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "git_log",
		Name:        "git_log",
		Description: "Show commit logs in a git repository.",
		Category:    CategoryGit,
		RiskLevel:   RiskNone,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_path": {"type": "string", "description": "Path to the git repository"},
				"path": {"type": "string", "description": "Alias for repo_path"},
				"max_count": {"type": "integer", "description": "Maximum number of commits to list"},
				"revision": {"type": "string", "description": "Revision range or commit reference"}
			}
		}`),
		RequiredCapabilities: []string{"tools:git"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type gitLogArgs struct {
	RepoPath string `json:"repo_path"`
	Path     string `json:"path"`
	MaxCount int    `json:"max_count"`
	Revision string `json:"revision"`
}

func (t *GitLogTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a gitLogArgs
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	out, entries, err := t.tool.Log(ctx, a.RepoPath, a.Path, a.MaxCount, 0, a.Revision, "")
	if err != nil {
		return nil, err
	}
	return &ExecutionResult{
		ToolName: "git_log",
		Output:   out,
		Data:     entries,
	}, nil
}

// --- Git Branch Tool ---

type GitBranchTool struct {
	tool *git.BranchTool
}

func NewGitBranchTool(cfg git.Config) *GitBranchTool {
	return &GitBranchTool{tool: git.NewBranchTool(cfg)}
}

func (t *GitBranchTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "git_branch",
		Name:        "git_branch",
		Description: "List or create branches in a git repository.",
		Category:    CategoryGit,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_path": {"type": "string", "description": "Path to the git repository"},
				"path": {"type": "string", "description": "Alias for repo_path"},
				"action": {"type": "string", "enum": ["list", "create"], "description": "Operation to perform"},
				"branch_name": {"type": "string", "description": "Branch name when action is create"}
			}
		}`),
		RequiredCapabilities: []string{"tools:git"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type gitBranchArgs struct {
	RepoPath   string `json:"repo_path"`
	Path       string `json:"path"`
	Action     string `json:"action"`
	BranchName string `json:"branch_name"`
}

func (t *GitBranchTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a gitBranchArgs
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	if a.Action == "" {
		a.Action = "list"
	}
	out, branches, err := t.tool.Branch(ctx, a.RepoPath, a.Path, a.Action, a.BranchName, "")
	if err != nil {
		return nil, err
	}
	return &ExecutionResult{
		ToolName: "git_branch",
		Output:   out,
		Data:     branches,
	}, nil
}

// --- Git Commit Tool ---

type GitCommitTool struct {
	tool *git.CommitTool
}

func NewGitCommitTool(cfg git.Config) *GitCommitTool {
	return &GitCommitTool{tool: git.NewCommitTool(cfg)}
}

func (t *GitCommitTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "git_commit",
		Name:        "git_commit",
		Description: "Stage files and create a git commit with a message.",
		Category:    CategoryGit,
		RiskLevel:   RiskMedium,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_path": {"type": "string", "description": "Path to the git repository"},
				"path": {"type": "string", "description": "Alias for repo_path"},
				"message": {"type": "string", "description": "Commit message"},
				"files": {"type": "array", "items": {"type": "string"}, "description": "Files to stage"}
			},
			"required": ["message"]
		}`),
		RequiredCapabilities: []string{"tools:git:commit"},
		RequiredPermissions:  []string{"write"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type gitCommitArgs struct {
	RepoPath string   `json:"repo_path"`
	Path     string   `json:"path"`
	Message  string   `json:"message"`
	Files    []string `json:"files"`
}

func (t *GitCommitTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a gitCommitArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}
	out, err := t.tool.Commit(ctx, a.RepoPath, a.Path, a.Message, false, a.Files, "", "")
	if err != nil {
		return nil, err
	}
	return &ExecutionResult{
		ToolName: "git_commit",
		Output:   out,
	}, nil
}
