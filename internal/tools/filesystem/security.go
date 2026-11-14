package filesystem

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrSchemaValidation = errors.New("schema validation failed")
	ErrPermissionDenied = errors.New("permission denied")
)

const (
	MaxFileSizeRead  = 10 * 1024 * 1024 // 10MB
	MaxFileSizeWrite = 10 * 1024 * 1024 // 10MB
)

var deniedSystemPrefixes = []string{
	"/etc",
	"/root",
	"/sys",
	"/proc",
	"/dev",
	"/boot",
	"/sbin",
	"/usr/sbin",
}

// Config controls filesystem tool boundaries and limits.
type Config struct {
	AllowedRoots  []string
	MaxReadBytes  int64
	MaxWriteBytes int64
}

// DefaultConfig returns default boundaries based on the current working directory.
func DefaultConfig() Config {
	cwd, _ := os.Getwd()
	roots := []string{}
	if cwd != "" {
		roots = append(roots, cwd)
	}
	return Config{
		AllowedRoots:  roots,
		MaxReadBytes:  MaxFileSizeRead,
		MaxWriteBytes: MaxFileSizeWrite,
	}
}

// CheckPathSecurity validates that path is safe, normalized, within allowed roots,
// does not escape via traversal or symlinks, and does not target sensitive system paths.
func CheckPathSecurity(path string, allowedRoots []string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: path cannot be empty", ErrSchemaValidation)
	}

	// URL decode to catch %2e%2e and %2f
	if strings.Contains(path, "%") {
		if unescaped, err := url.PathUnescape(path); err == nil {
			path = unescaped
		}
	}

	cleaned := filepath.Clean(path)
	slashCleaned := filepath.ToSlash(cleaned)

	// Check if relative traversal path targets prohibited system prefixes
	for _, denied := range deniedSystemPrefixes {
		deniedRel := strings.TrimPrefix(denied, "/")
		if strings.HasPrefix(slashCleaned, deniedRel+"/") || strings.Contains(slashCleaned, "/"+deniedRel+"/") || strings.Contains(slashCleaned, "/"+deniedRel) {
			return "", fmt.Errorf("%w: access to system directory %q is denied", ErrPermissionDenied, denied)
		}
	}

	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("%w: invalid path %q: %v", ErrSchemaValidation, path, err)
	}

	lower := strings.ToLower(absPath)
	for _, denied := range deniedSystemPrefixes {
		if lower == denied || strings.HasPrefix(lower, denied+"/") {
			return "", fmt.Errorf("%w: access to system directory %q is denied", ErrPermissionDenied, denied)
		}
	}

	base := strings.ToLower(filepath.Base(absPath))
	if base == "id_rsa" || base == "id_ed25519" || (base == "credentials" && strings.Contains(lower, ".aws")) || (base == ".env" && strings.Contains(lower, "/etc/")) {
		return "", fmt.Errorf("%w: access to credentials file is denied", ErrPermissionDenied)
	}

	// Symlink evaluation for existing path or deepest existing ancestor
	checkTarget := absPath
	for {
		if fi, err := os.Lstat(checkTarget); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 || checkTarget == absPath {
				evalTarget, err := filepath.EvalSymlinks(checkTarget)
				if err != nil {
					return "", fmt.Errorf("%w: cannot evaluate symlink %q: %v", ErrPermissionDenied, checkTarget, err)
				}
				evalClean := filepath.Clean(evalTarget)
				evalLower := strings.ToLower(evalClean)
				for _, denied := range deniedSystemPrefixes {
					if evalLower == denied || strings.HasPrefix(evalLower, denied+"/") {
						return "", fmt.Errorf("%w: symlink targets prohibited path %q", ErrPermissionDenied, denied)
					}
				}
				if len(allowedRoots) > 0 {
					escaped := true
					for _, root := range allowedRoots {
						cRoot, _ := filepath.EvalSymlinks(root)
						if cRoot == "" {
							cRoot = filepath.Clean(root)
						}
						if evalClean == cRoot || strings.HasPrefix(evalClean, cRoot+string(filepath.Separator)) {
							escaped = false
							break
						}
					}
					if escaped {
						return "", fmt.Errorf("%w: symlink escapes allowed roots", ErrPermissionDenied)
					}
				}
			}
			break
		}
		parent := filepath.Dir(checkTarget)
		if parent == checkTarget {
			break
		}
		checkTarget = parent
	}

	// Confinement to allowed roots
	if len(allowedRoots) > 0 {
		withinRoot := false
		for _, root := range allowedRoots {
			cRoot, _ := filepath.EvalSymlinks(root)
			if cRoot == "" {
				cRoot = filepath.Clean(root)
			}
			if absPath == cRoot || strings.HasPrefix(absPath, cRoot+string(filepath.Separator)) {
				withinRoot = true
				break
			}
		}
		if !withinRoot {
			return "", fmt.Errorf("%w: path %q outside allowed roots", ErrPermissionDenied, absPath)
		}
	}

	return absPath, nil
}
