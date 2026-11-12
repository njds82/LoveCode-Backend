package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Default filesystem limits
const (
	MaxFileSizeRead  = 10 * 1024 * 1024 // 10MB
	MaxFileSizeWrite = 10 * 1024 * 1024 // 10MB
)

// Denied system paths that must never be read or written to
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

// CheckPathSecurity validates that a path is safe, does not escape via traversal or symlinks,
// and does not point to sensitive system directories.
func CheckPathSecurity(path string, allowedRoots []string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path cannot be empty")
	}

	// Normalize
	cleaned := filepath.Clean(path)
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	// Check if path attempts path traversal targeting denied paths
	lower := strings.ToLower(absPath)
	for _, denied := range deniedSystemPrefixes {
		if lower == denied || strings.HasPrefix(lower, denied+"/") {
			return "", fmt.Errorf("%w: access to system directory %q is denied", ErrPermissionDenied, denied)
		}
	}

	// Check sensitive files (SSH keys, AWS credentials, env files)
	base := strings.ToLower(filepath.Base(absPath))
	if base == "id_rsa" || base == "id_ed25519" || base == "credentials" && strings.Contains(lower, ".aws") || base == ".env" && strings.Contains(lower, "/etc/") {
		return "", fmt.Errorf("%w: access to credentials file is denied", ErrPermissionDenied)
	}

	// If the file exists, evaluate symlinks to detect escapes
	if fi, err := os.Lstat(absPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(absPath)
			if err != nil {
				return "", fmt.Errorf("eval symlink failed: %w", err)
			}
			targetClean := filepath.Clean(target)
			targetLower := strings.ToLower(targetClean)
			for _, denied := range deniedSystemPrefixes {
				if targetLower == denied || strings.HasPrefix(targetLower, denied+"/") {
					return "", fmt.Errorf("%w: symlink targets prohibited path %q", ErrPermissionDenied, denied)
				}
			}
			if len(allowedRoots) > 0 {
				escaped := true
				for _, root := range allowedRoots {
					cRoot, _ := filepath.EvalSymlinks(root)
					if cRoot == "" {
						cRoot = root
					}
					if targetClean == cRoot || strings.HasPrefix(targetClean, cRoot+string(filepath.Separator)) {
						escaped = false
						break
					}
				}
				if escaped {
					return "", fmt.Errorf("%w: symlink escapes allowed roots", ErrPermissionDenied)
				}
			}
		}
	}

	// Check against explicit allowed roots if configured
	if len(allowedRoots) > 0 {
		withinRoot := false
		for _, root := range allowedRoots {
			cRoot, _ := filepath.EvalSymlinks(root)
			if cRoot == "" {
				cRoot = root
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

// --- FS Read Tool ---

type FSReadTool struct {
	AllowedRoots []string
}

func (t *FSReadTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "fs_read",
		Name:        "fs_read",
		Description: "Read the contents of a file within authorized filesystem boundaries.",
		Category:    CategoryFilesystem,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Path to the file to read"}
			},
			"required": ["path"]
		}`),
		RequiredCapabilities: []string{"tools:filesystem:read"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

type fsReadArgs struct {
	Path string `json:"path"`
}

func (t *FSReadTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a fsReadArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	safePath, err := CheckPathSecurity(a.Path, t.AllowedRoots)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(safePath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: target is a directory", ErrSchemaValidation)
	}
	if info.Size() > MaxFileSizeRead {
		return nil, fmt.Errorf("%w: file size exceeds limit of %d bytes", ErrSchemaValidation, MaxFileSizeRead)
	}

	f, err := os.Open(safePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	content, err := io.ReadAll(io.LimitReader(f, MaxFileSizeRead))
	if err != nil {
		return nil, err
	}

	return &ExecutionResult{
		ToolName: "fs_read",
		Output:   string(content),
	}, nil
}

// --- FS Write Tool ---

type FSWriteTool struct {
	AllowedRoots []string
}

func (t *FSWriteTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "fs_write",
		Name:        "fs_write",
		Description: "Write content to a file atomically within authorized filesystem boundaries.",
		Category:    CategoryFilesystem,
		RiskLevel:   RiskMedium,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Target file path"},
				"content": {"type": "string", "description": "File content to write"}
			},
			"required": ["path", "content"]
		}`),
		RequiredCapabilities: []string{"tools:filesystem:write"},
		RequiredPermissions:  []string{"write"},
		Enabled:              true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

type fsWriteArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *FSWriteTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a fsWriteArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	safePath, err := CheckPathSecurity(a.Path, t.AllowedRoots)
	if err != nil {
		return nil, err
	}

	if int64(len(a.Content)) > MaxFileSizeWrite {
		return nil, fmt.Errorf("%w: content size exceeds limit of %d bytes", ErrSchemaValidation, MaxFileSizeWrite)
	}

	// Ensure parent dir exists
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Atomic write via temporary file
	tmpFile, err := os.CreateTemp(dir, ".tmp-write-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.WriteString(a.Content); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write content: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to sync content: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, safePath); err != nil {
		return nil, fmt.Errorf("failed to commit atomic write: %w", err)
	}

	return &ExecutionResult{
		ToolName: "fs_write",
		Output:   fmt.Sprintf("successfully wrote %d bytes to %s", len(a.Content), safePath),
	}, nil
}

// --- FS List Tool ---

type FSListTool struct {
	AllowedRoots []string
}

func (t *FSListTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "fs_list",
		Name:        "fs_list",
		Description: "List entries in a directory within authorized filesystem boundaries.",
		Category:    CategoryFilesystem,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Directory path"}
			},
			"required": ["path"]
		}`),
		RequiredCapabilities: []string{"tools:filesystem:read"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

type fsListArgs struct {
	Path string `json:"path"`
}

type EntryInfo struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
}

func (t *FSListTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a fsListArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	safePath, err := CheckPathSecurity(a.Path, t.AllowedRoots)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(safePath)
	if err != nil {
		return nil, err
	}

	list := make([]EntryInfo, 0, len(entries))
	var sb strings.Builder
	for _, e := range entries {
		info, err := e.Info()
		var size int64
		var mode string
		var modTime string
		if err == nil {
			size = info.Size()
			mode = info.Mode().String()
			modTime = info.ModTime().Format(time.RFC3339)
		}
		item := EntryInfo{
			Name:    e.Name(),
			IsDir:   e.IsDir(),
			Size:    size,
			Mode:    mode,
			ModTime: modTime,
		}
		list = append(list, item)
		typeChar := "F"
		if e.IsDir() {
			typeChar = "D"
		}
		sb.WriteString(fmt.Sprintf("[%s] %s (%d bytes)\n", typeChar, e.Name(), size))
	}

	return &ExecutionResult{
		ToolName: "fs_list",
		Output:   sb.String(),
		Data:     list,
	}, nil
}

// --- FS Stat Tool ---

type FSStatTool struct {
	AllowedRoots []string
}

func (t *FSStatTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "fs_stat",
		Name:        "fs_stat",
		Description: "Get metadata for a file or directory within authorized filesystem boundaries.",
		Category:    CategoryFilesystem,
		RiskLevel:   RiskNone,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Target file or directory path"}
			},
			"required": ["path"]
		}`),
		RequiredCapabilities: []string{"tools:filesystem:read"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

type fsStatArgs struct {
	Path string `json:"path"`
}

func (t *FSStatTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a fsStatArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	safePath, err := CheckPathSecurity(a.Path, t.AllowedRoots)
	if err != nil {
		return nil, err
	}

	fi, err := os.Stat(safePath)
	if err != nil {
		return nil, err
	}

	info := EntryInfo{
		Name:    fi.Name(),
		IsDir:   fi.IsDir(),
		Size:    fi.Size(),
		Mode:    fi.Mode().String(),
		ModTime: fi.ModTime().Format(time.RFC3339),
	}

	out := fmt.Sprintf("name: %s\nis_dir: %t\nsize: %d\nmode: %s\nmod_time: %s",
		info.Name, info.IsDir, info.Size, info.Mode, info.ModTime)

	return &ExecutionResult{
		ToolName: "fs_stat",
		Output:   out,
		Data:     info,
	}, nil
}

// --- FS Delete Tool ---

type FSDeleteTool struct {
	AllowedRoots []string
}

func (t *FSDeleteTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "fs_delete",
		Name:        "fs_delete",
		Description: "Delete a file within authorized filesystem boundaries.",
		Category:    CategoryFilesystem,
		RiskLevel:   RiskHigh,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "File path to delete"}
			},
			"required": ["path"]
		}`),
		RequiredCapabilities: []string{"tools:filesystem:delete", "tools:filesystem:write"},
		RequiredPermissions:  []string{"write"},
		Enabled:              true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

type fsDeleteArgs struct {
	Path string `json:"path"`
}

func (t *FSDeleteTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a fsDeleteArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	safePath, err := CheckPathSecurity(a.Path, t.AllowedRoots)
	if err != nil {
		return nil, err
	}

	fi, err := os.Stat(safePath)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%w: deleting directories is not permitted via fs_delete", ErrPermissionDenied)
	}

	if err := os.Remove(safePath); err != nil {
		return nil, fmt.Errorf("failed to delete file: %w", err)
	}

	return &ExecutionResult{
		ToolName: "fs_delete",
		Output:   fmt.Sprintf("deleted file %s", safePath),
	}, nil
}

// --- FS Mkdir Tool ---

type FSMkdirTool struct {
	AllowedRoots []string
}

func (t *FSMkdirTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "fs_mkdir",
		Name:        "fs_mkdir",
		Description: "Create a directory within authorized filesystem boundaries.",
		Category:    CategoryFilesystem,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Directory path to create"}
			},
			"required": ["path"]
		}`),
		RequiredCapabilities: []string{"tools:filesystem:write"},
		RequiredPermissions:  []string{"write"},
		Enabled:              true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

type fsMkdirArgs struct {
	Path string `json:"path"`
}

func (t *FSMkdirTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a fsMkdirArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	safePath, err := CheckPathSecurity(a.Path, t.AllowedRoots)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(safePath, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	return &ExecutionResult{
		ToolName: "fs_mkdir",
		Output:   fmt.Sprintf("created directory %s", safePath),
	}, nil
}

// --- FS Search Tool ---

type FSSearchTool struct {
	AllowedRoots []string
}

func (t *FSSearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "fs_search",
		Name:        "fs_search",
		Description: "Search for files by pattern within authorized filesystem boundaries.",
		Category:    CategoryFilesystem,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Base directory to search"},
				"pattern": {"type": "string", "description": "Filename match pattern (e.g. *.go)"}
			},
			"required": ["path", "pattern"]
		}`),
		RequiredCapabilities: []string{"tools:filesystem:read"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

type fsSearchArgs struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
}

func (t *FSSearchTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a fsSearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	safePath, err := CheckPathSecurity(a.Path, t.AllowedRoots)
	if err != nil {
		return nil, err
	}

	var matches []string
	err = filepath.WalkDir(safePath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		matched, _ := filepath.Match(a.Pattern, d.Name())
		if matched {
			matches = append(matches, p)
		}
		if len(matches) >= 100 {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &ExecutionResult{
		ToolName: "fs_search",
		Output:   strings.Join(matches, "\n"),
		Data:     matches,
	}, nil
}
