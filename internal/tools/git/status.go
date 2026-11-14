package git

import (
	"context"
	"strconv"
	"strings"
)

type StatusTool struct {
	runner *Runner
}

func NewStatusTool(cfg Config) *StatusTool {
	return &StatusTool{runner: NewRunner(cfg)}
}

type StatusResult struct {
	Branch    string   `json:"branch"`
	Ahead     int      `json:"ahead"`
	Behind    int      `json:"behind"`
	Clean     bool     `json:"clean"`
	Staged    []string `json:"staged"`
	Unstaged  []string `json:"unstaged"`
	Untracked []string `json:"untracked"`
}

func (t *StatusTool) Status(ctx context.Context, repoPath, path string) (string, StatusResult, error) {
	repoDir := ResolveRepoPath(repoPath, path)
	out, err := t.runner.ExecGit(ctx, repoDir, "status", "--porcelain=v1", "-b")
	if err != nil {
		return "", StatusResult{}, err
	}

	res := StatusResult{
		Clean:     true,
		Staged:    []string{},
		Unstaged:  []string{},
		Untracked: []string{},
	}

	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			header := strings.TrimPrefix(line, "## ")
			parts := strings.Split(header, "...")
			res.Branch = parts[0]
			if len(parts) > 1 {
				rem := parts[1]
				if idx := strings.Index(rem, "[ahead "); idx != -1 {
					sub := rem[idx+7:]
					if endIdx := strings.Index(sub, "]"); endIdx != -1 {
						aheadPart := sub[:endIdx]
						if strings.Contains(aheadPart, ", behind ") {
							tokens := strings.Split(aheadPart, ", behind ")
							res.Ahead, _ = strconv.Atoi(tokens[0])
							res.Behind, _ = strconv.Atoi(tokens[1])
						} else {
							res.Ahead, _ = strconv.Atoi(aheadPart)
						}
					}
				} else if idx := strings.Index(rem, "[behind "); idx != -1 {
					sub := rem[idx+8:]
					if endIdx := strings.Index(sub, "]"); endIdx != -1 {
						res.Behind, _ = strconv.Atoi(sub[:endIdx])
					}
				}
			}
			continue
		}

		if len(line) >= 3 {
			res.Clean = false
			x := line[0]
			y := line[1]
			file := strings.TrimSpace(line[3:])

			if x == '?' && y == '?' {
				res.Untracked = append(res.Untracked, file)
			} else {
				if x != ' ' && x != '?' {
					res.Staged = append(res.Staged, file)
				}
				if y != ' ' && y != '?' {
					res.Unstaged = append(res.Unstaged, file)
				}
			}
		}
	}

	return out, res, nil
}
