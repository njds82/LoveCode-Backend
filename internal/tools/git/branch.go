package git

import (
	"context"
	"fmt"
	"strings"
)

type BranchTool struct {
	runner *Runner
}

func NewBranchTool(cfg Config) *BranchTool {
	return &BranchTool{runner: NewRunner(cfg)}
}

type BranchListResult struct {
	Current  string   `json:"current"`
	Branches []string `json:"branches"`
}

func (t *BranchTool) Branch(ctx context.Context, repoPath, path, action, branchName, startPoint string) (string, any, error) {
	repoDir := ResolveRepoPath(repoPath, path)
	act := strings.ToLower(strings.TrimSpace(action))
	if act == "" {
		act = "list"
	}

	switch act {
	case "list":
		out, err := t.runner.ExecGit(ctx, repoDir, "branch", "--list", "-a")
		if err != nil {
			return "", nil, err
		}
		var res BranchListResult
		lines := strings.Split(out, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(line, "* ") {
				res.Current = strings.TrimPrefix(line, "* ")
				res.Branches = append(res.Branches, res.Current)
			} else {
				res.Branches = append(res.Branches, trimmed)
			}
		}
		return out, res, nil

	case "create":
		bName := strings.TrimSpace(branchName)
		if bName == "" {
			return "", nil, fmt.Errorf("%w: branch_name is required for create action", ErrSchemaValidation)
		}
		if strings.HasPrefix(bName, "-") || !validRefRe.MatchString(bName) {
			return "", nil, fmt.Errorf("%w: invalid branch name %q", ErrSchemaValidation, bName)
		}

		cmdArgs := []string{"branch", bName}
		if startPoint != "" {
			start := strings.TrimSpace(startPoint)
			if strings.HasPrefix(start, "-") || !validRefRe.MatchString(start) {
				return "", nil, fmt.Errorf("%w: invalid start point %q", ErrSchemaValidation, startPoint)
			}
			cmdArgs = append(cmdArgs, start)
		}

		if err := ValidateGitArgs("branch", cmdArgs); err != nil {
			return "", nil, err
		}

		out, err := t.runner.ExecGit(ctx, repoDir, cmdArgs...)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("created branch %s\n%s", bName, out), nil, nil

	case "switch":
		bName := strings.TrimSpace(branchName)
		if bName == "" {
			return "", nil, fmt.Errorf("%w: branch_name is required for switch action", ErrSchemaValidation)
		}
		if strings.HasPrefix(bName, "-") || !validRefRe.MatchString(bName) {
			return "", nil, fmt.Errorf("%w: invalid branch name %q", ErrSchemaValidation, bName)
		}

		cmdArgs := []string{"switch", bName}
		if err := ValidateGitArgs("switch", cmdArgs); err != nil {
			return "", nil, err
		}

		out, err := t.runner.ExecGit(ctx, repoDir, cmdArgs...)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("switched to branch %s\n%s", bName, out), nil, nil

	default:
		return "", nil, fmt.Errorf("%w: unsupported branch action %q (allowed: list, create, switch)", ErrSchemaValidation, action)
	}
}
