package git

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var validRefRe = regexp.MustCompile(`^[a-zA-Z0-9_./~^:-]+$`)

type DiffTool struct {
	runner *Runner
}

func NewDiffTool(cfg Config) *DiffTool {
	return &DiffTool{runner: NewRunner(cfg)}
}

func (t *DiffTool) Diff(ctx context.Context, repoPath, path string, staged bool, target, filePath string) (string, error) {
	repoDir := ResolveRepoPath(repoPath, path)
	cmdArgs := []string{"diff", "--no-ext-diff"}

	if staged {
		cmdArgs = append(cmdArgs, "--staged")
	}

	if target != "" {
		trimmed := strings.TrimSpace(target)
		if strings.HasPrefix(trimmed, "-") || !validRefRe.MatchString(trimmed) {
			return "", fmt.Errorf("%w: invalid target revision %q", ErrSchemaValidation, target)
		}
		cmdArgs = append(cmdArgs, trimmed)
	}

	if filePath != "" {
		cleaned := strings.TrimSpace(filePath)
		if strings.HasPrefix(cleaned, "-") {
			return "", fmt.Errorf("%w: invalid file path %q", ErrSchemaValidation, filePath)
		}
		cmdArgs = append(cmdArgs, "--", cleaned)
	}

	if err := ValidateGitArgs("diff", cmdArgs); err != nil {
		return "", err
	}

	return t.runner.ExecGit(ctx, repoDir, cmdArgs...)
}
