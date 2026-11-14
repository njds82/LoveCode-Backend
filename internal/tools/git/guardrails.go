package git

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lovecode/lovecode/internal/security"
)

var (
	ErrDestructiveCommand = errors.New("destructive git operation blocked")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrInvalidGitArgument = errors.New("invalid or dangerous git argument")
	ErrNotAGitRepo        = errors.New("not a git repository")
	ErrSchemaValidation   = errors.New("schema validation failed")
	ErrToolNotFound       = errors.New("binary not found")
)

var (
	// Redact remote credentials in URLs (e.g. https://token@github.com or https://user:pass@host)
	gitURLCredRe = regexp.MustCompile(`(?i)((?:https?|ssh|git)://)[^/\s@]+@`)

	// Blocked dangerous options across commands
	blockedOptions = []string{
		"--exec-path",
		"--upload-pack",
		"-c",
		"--config",
		"--hard",
		"-fd",
		"-f",
		"--force",
		"--force-with-lease",
		"-D",
		"--delete",
		"--discard-changes",
	}
)

// ValidateGitArgs validates that git subcommand and arguments contain no dangerous or destructive flags.
func ValidateGitArgs(subcommand string, args []string) error {
	for _, arg := range args {
		lower := strings.ToLower(strings.TrimSpace(arg))
		for _, blocked := range blockedOptions {
			if lower == blocked || strings.HasPrefix(lower, blocked+"=") {
				return fmt.Errorf("%w: argument %q is prohibited: %w", ErrPermissionDenied, arg, ErrDestructiveCommand)
			}
		}
	}
	return nil
}

// SanitizeGitOutput masks credentials and secrets from git command output.
func SanitizeGitOutput(s string) string {
	s = gitURLCredRe.ReplaceAllString(s, "${1}[REDACTED]@")
	return security.RedactString(s)
}

// ResolveRepoPath picks repo_path or path, returning current directory if empty.
func ResolveRepoPath(repoPath, path string) string {
	if repoPath != "" {
		return repoPath
	}
	if path != "" {
		return path
	}
	return "."
}
