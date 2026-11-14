package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lovecode/lovecode/internal/tools/filesystem"
	"github.com/lovecode/lovecode/internal/tools/git"
)

func wrapGitErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, git.ErrPermissionDenied) || errors.Is(err, filesystem.ErrPermissionDenied) {
		return fmt.Errorf("%w: %v", ErrPermissionDenied, err)
	}
	if errors.Is(err, git.ErrSchemaValidation) || errors.Is(err, filesystem.ErrSchemaValidation) {
		return fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}
	return err
}

// --- git_status ---

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
		Description: "Show the working tree status of a git repository.",
		Category:    CategoryGit,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_path": {"type": "string", "description": "Path to git repository"},
				"path": {"type": "string", "description": "Alternative path to git repository"}
			}
		}`),
		RequiredCapabilities: []string{"tools:git"},
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
	if len(args) > 0 && string(args) != "{}" {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
		}
	}

	out, res, err := t.tool.Status(ctx, a.RepoPath, a.Path)
	if err != nil {
		return nil, wrapGitErr(err)
	}

	return &ExecutionResult{
		ToolName: "git_status",
		Output:   out,
		Data:     res,
	}, nil
}

// --- git_diff ---

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
				"repo_path": {"type": "string", "description": "Path to git repository"},
				"path": {"type": "string", "description": "Alternative path to git repository"},
				"staged": {"type": "boolean", "description": "Show staged diff"},
				"target": {"type": "string", "description": "Target revision or branch"},
				"file_path": {"type": "string", "description": "Optional file path filter"}
			}
		}`),
		RequiredCapabilities: []string{"tools:git"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type gitDiffArgs struct {
	RepoPath string `json:"repo_path"`
	Path     string `json:"path"`
	Staged   bool   `json:"staged"`
	Target   string `json:"target"`
	FilePath string `json:"file_path"`
}

func (t *GitDiffTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a gitDiffArgs
	if len(args) > 0 && string(args) != "{}" {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
		}
	}

	out, err := t.tool.Diff(ctx, a.RepoPath, a.Path, a.Staged, a.Target, a.FilePath)
	if err != nil {
		return nil, wrapGitErr(err)
	}

	return &ExecutionResult{
		ToolName: "git_diff",
		Output:   out,
	}, nil
}

// --- git_log ---

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
		Description: "Show commit logs for a git repository.",
		Category:    CategoryGit,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_path": {"type": "string", "description": "Path to git repository"},
				"path": {"type": "string", "description": "Alternative path to git repository"},
				"limit": {"type": "integer", "description": "Number of commits to return (max 100)"},
				"revision": {"type": "string", "description": "Revision range or commit"},
				"file_path": {"type": "string", "description": "Optional file path filter"}
			}
		}`),
		RequiredCapabilities: []string{"tools:git"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type gitLogArgs struct {
	RepoPath string `json:"repo_path"`
	Path     string `json:"path"`
	Limit    int    `json:"limit"`
	Revision string `json:"revision"`
	FilePath string `json:"file_path"`
}

func (t *GitLogTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a gitLogArgs
	if len(args) > 0 && string(args) != "{}" {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
		}
	}

	out, commits, err := t.tool.Log(ctx, a.RepoPath, a.Path, a.Limit, a.Limit, a.Revision, a.FilePath)
	if err != nil {
		return nil, wrapGitErr(err)
	}

	return &ExecutionResult{
		ToolName: "git_log",
		Output:   out,
		Data:     commits,
	}, nil
}

// --- git_branch ---

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
		Description: "List, create, or switch branches in a git repository.",
		Category:    CategoryGit,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_path": {"type": "string", "description": "Path to git repository"},
				"path": {"type": "string", "description": "Alternative path to git repository"},
				"action": {"type": "string", "enum": ["list", "create", "switch"], "default": "list"},
				"branch_name": {"type": "string", "description": "Branch name to create or switch to"},
				"start_point": {"type": "string", "description": "Starting commit/branch for new branch"}
			}
		}`),
		RequiredCapabilities: []string{"tools:git"},
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
	StartPoint string `json:"start_point"`
}

func (t *GitBranchTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a gitBranchArgs
	if len(args) > 0 && string(args) != "{}" {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
		}
	}

	out, data, err := t.tool.Branch(ctx, a.RepoPath, a.Path, a.Action, a.BranchName, a.StartPoint)
	if err != nil {
		return nil, wrapGitErr(err)
	}

	return &ExecutionResult{
		ToolName: "git_branch",
		Output:   out,
		Data:     data,
	}, nil
}

// --- git_commit ---

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
		Description: "Record changes to the repository with a commit message.",
		Category:    CategoryGit,
		RiskLevel:   RiskMedium,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"repo_path": {"type": "string", "description": "Path to git repository"},
				"path": {"type": "string", "description": "Alternative path to git repository"},
				"message": {"type": "string", "description": "Commit message"},
				"all": {"type": "boolean", "description": "Stage all changes before commit"},
				"files": {"type": "array", "items": {"type": "string"}, "description": "Specific files to stage"},
				"author_name": {"type": "string", "description": "Author name"},
				"author_email": {"type": "string", "description": "Author email"}
			},
			"required": ["message"]
		}`),
		RequiredCapabilities: []string{"tools:git"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type gitCommitArgs struct {
	RepoPath    string   `json:"repo_path"`
	Path        string   `json:"path"`
	Message     string   `json:"message"`
	All         bool     `json:"all"`
	Files       []string `json:"files"`
	AuthorName  string   `json:"author_name"`
	AuthorEmail string   `json:"author_email"`
}

func (t *GitCommitTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a gitCommitArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	out, err := t.tool.Commit(ctx, a.RepoPath, a.Path, a.Message, a.All, a.Files, a.AuthorName, a.AuthorEmail)
	if err != nil {
		return nil, wrapGitErr(err)
	}

	return &ExecutionResult{
		ToolName: "git_commit",
		Output:   out,
	}, nil
}
