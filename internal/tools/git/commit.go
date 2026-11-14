package git

import (
	"context"
	"fmt"
	"strings"
)

type CommitTool struct {
	runner *Runner
}

func NewCommitTool(cfg Config) *CommitTool {
	return &CommitTool{runner: NewRunner(cfg)}
}

func (t *CommitTool) Commit(ctx context.Context, repoPath, path, message string, all bool, files []string, authorName, authorEmail string) (string, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "", fmt.Errorf("%w: commit message cannot be empty", ErrSchemaValidation)
	}
	if strings.HasPrefix(msg, "-") {
		return "", fmt.Errorf("%w: commit message cannot start with '-'", ErrSchemaValidation)
	}

	repoDir := ResolveRepoPath(repoPath, path)

	// Stage files if specified
	if len(files) > 0 {
		for _, f := range files {
			cleaned := strings.TrimSpace(f)
			if cleaned == "" || strings.HasPrefix(cleaned, "-") {
				continue
			}
			addArgs := []string{"add", "--", cleaned}
			if _, err := t.runner.ExecGit(ctx, repoDir, addArgs...); err != nil {
				return "", fmt.Errorf("git add %q failed: %w", cleaned, err)
			}
		}
	} else if all {
		addArgs := []string{"add", "-A"}
		if _, err := t.runner.ExecGit(ctx, repoDir, addArgs...); err != nil {
			return "", fmt.Errorf("git add -A failed: %w", err)
		}
	}

	commitCmd := []string{"commit", "-m", msg}
	if authorName != "" && authorEmail != "" {
		author := fmt.Sprintf("%s <%s>", strings.TrimSpace(authorName), strings.TrimSpace(authorEmail))
		commitCmd = append(commitCmd, "--author", author)
	}

	if err := ValidateGitArgs("commit", commitCmd); err != nil {
		return "", err
	}

	return t.runner.ExecGit(ctx, repoDir, commitCmd...)
}
