package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type LogTool struct {
	runner *Runner
}

func NewLogTool(cfg Config) *LogTool {
	return &LogTool{runner: NewRunner(cfg)}
}

type GitCommitInfo struct {
	Hash        string `json:"hash"`
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	Date        string `json:"date"`
	Subject     string `json:"subject"`
}

func (t *LogTool) Log(ctx context.Context, repoPath, path string, limit, maxCount int, revision, filePath string) (string, []GitCommitInfo, error) {
	repoDir := ResolveRepoPath(repoPath, path)

	l := limit
	if l <= 0 {
		l = maxCount
	}
	if l <= 0 {
		l = 10
	}
	if l > 100 {
		l = 100
	}

	cmdArgs := []string{"log", "-n", strconv.Itoa(l), "--format=%H%x00%an%x00%ae%x00%aI%x00%s"}

	if revision != "" {
		rev := strings.TrimSpace(revision)
		if strings.HasPrefix(rev, "-") || !validRefRe.MatchString(rev) {
			return "", nil, fmt.Errorf("%w: invalid revision %q", ErrSchemaValidation, revision)
		}
		cmdArgs = append(cmdArgs, rev)
	}

	if filePath != "" {
		cleaned := strings.TrimSpace(filePath)
		if strings.HasPrefix(cleaned, "-") {
			return "", nil, fmt.Errorf("%w: invalid file path %q", ErrSchemaValidation, filePath)
		}
		cmdArgs = append(cmdArgs, "--", cleaned)
	}

	if err := ValidateGitArgs("log", cmdArgs); err != nil {
		return "", nil, err
	}

	out, err := t.runner.ExecGit(ctx, repoDir, cmdArgs...)
	if err != nil {
		return "", nil, err
	}

	var commits []GitCommitInfo
	var sb strings.Builder

	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x00")
		if len(parts) >= 5 {
			c := GitCommitInfo{
				Hash:        parts[0],
				AuthorName:  parts[1],
				AuthorEmail: parts[2],
				Date:        parts[3],
				Subject:     parts[4],
			}
			commits = append(commits, c)
			sb.WriteString(fmt.Sprintf("%s (%s): %s\n", c.Hash[:min(7, len(c.Hash))], c.AuthorName, c.Subject))
		}
	}

	return sb.String(), commits, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
