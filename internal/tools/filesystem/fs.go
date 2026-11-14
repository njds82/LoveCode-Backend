package filesystem

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EntryInfo models directory entries and file metadata.
type EntryInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
}

// Engine implements scoped filesystem operations.
type Engine struct {
	cfg Config
}

func NewEngine(cfg Config) *Engine {
	return &Engine{cfg: cfg}
}

func (e *Engine) Read(path string, offset, limit int64) (string, error) {
	safePath, err := CheckPathSecurity(path, e.cfg.AllowedRoots)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(safePath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: target is a directory", ErrSchemaValidation)
	}

	maxRead := e.cfg.MaxReadBytes
	if maxRead <= 0 {
		maxRead = MaxFileSizeRead
	}
	if info.Size() > maxRead {
		return "", fmt.Errorf("%w: file size %d exceeds limit of %d bytes", ErrSchemaValidation, info.Size(), maxRead)
	}

	f, err := os.Open(safePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return "", fmt.Errorf("seek error: %w", err)
		}
	}

	readLimit := maxRead
	if limit > 0 && limit < readLimit {
		readLimit = limit
	}

	content, err := io.ReadAll(io.LimitReader(f, readLimit))
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (e *Engine) Write(path, content, mode string) (string, error) {
	safePath, err := CheckPathSecurity(path, e.cfg.AllowedRoots)
	if err != nil {
		return "", err
	}

	maxWrite := e.cfg.MaxWriteBytes
	if maxWrite <= 0 {
		maxWrite = MaxFileSizeWrite
	}
	if int64(len(content)) > maxWrite {
		return "", fmt.Errorf("%w: content size %d exceeds limit of %d bytes", ErrSchemaValidation, len(content), maxWrite)
	}

	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Atomic write via temporary file in target directory
	tmpFile, err := os.CreateTemp(dir, ".tmp-write-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if mode == "append" {
		if existing, err := os.ReadFile(safePath); err == nil {
			if _, err := tmpFile.Write(existing); err != nil {
				tmpFile.Close()
				return "", err
			}
		}
	}

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return "", err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		return "", err
	}

	if err := os.Rename(tmpName, safePath); err != nil {
		return "", fmt.Errorf("failed to commit atomic write: %w", err)
	}

	return fmt.Sprintf("successfully wrote %d bytes to %s", len(content), safePath), nil
}

func (e *Engine) List(path string, recursive bool) (string, []EntryInfo, error) {
	safePath, err := CheckPathSecurity(path, e.cfg.AllowedRoots)
	if err != nil {
		return "", nil, err
	}

	fi, err := os.Stat(safePath)
	if err != nil {
		return "", nil, err
	}
	if !fi.IsDir() {
		return "", nil, fmt.Errorf("%w: target is not a directory", ErrSchemaValidation)
	}

	var list []EntryInfo
	var sb strings.Builder

	if !recursive {
		entries, err := os.ReadDir(safePath)
		if err != nil {
			return "", nil, err
		}
		for _, item := range entries {
			info, err := item.Info()
			var size int64
			var mode string
			var modTime string
			if err == nil {
				size = info.Size()
				mode = info.Mode().String()
				modTime = info.ModTime().Format(time.RFC3339)
			}
			ent := EntryInfo{
				Name:    item.Name(),
				Path:    filepath.Join(safePath, item.Name()),
				IsDir:   item.IsDir(),
				Size:    size,
				Mode:    mode,
				ModTime: modTime,
			}
			list = append(list, ent)
			typeChar := "F"
			if item.IsDir() {
				typeChar = "D"
			}
			sb.WriteString(fmt.Sprintf("[%s] %s (%d bytes)\n", typeChar, item.Name(), size))
		}
	} else {
		count := 0
		err := filepath.WalkDir(safePath, func(p string, d fs.DirEntry, err error) error {
			if err != nil || p == safePath {
				return nil
			}
			count++
			if count > 1000 {
				return fs.SkipAll
			}
			info, err := d.Info()
			var size int64
			var mode string
			var modTime string
			if err == nil {
				size = info.Size()
				mode = info.Mode().String()
				modTime = info.ModTime().Format(time.RFC3339)
			}
			rel, _ := filepath.Rel(safePath, p)
			ent := EntryInfo{
				Name:    rel,
				Path:    p,
				IsDir:   d.IsDir(),
				Size:    size,
				Mode:    mode,
				ModTime: modTime,
			}
			list = append(list, ent)
			typeChar := "F"
			if d.IsDir() {
				typeChar = "D"
			}
			sb.WriteString(fmt.Sprintf("[%s] %s (%d bytes)\n", typeChar, rel, size))
			return nil
		})
		if err != nil {
			return "", nil, err
		}
	}

	return sb.String(), list, nil
}

func (e *Engine) Stat(path string) (string, EntryInfo, error) {
	safePath, err := CheckPathSecurity(path, e.cfg.AllowedRoots)
	if err != nil {
		return "", EntryInfo{}, err
	}

	fi, err := os.Stat(safePath)
	if err != nil {
		return "", EntryInfo{}, err
	}

	info := EntryInfo{
		Name:    fi.Name(),
		Path:    safePath,
		IsDir:   fi.IsDir(),
		Size:    fi.Size(),
		Mode:    fi.Mode().String(),
		ModTime: fi.ModTime().Format(time.RFC3339),
	}

	out := fmt.Sprintf("name: %s\nis_dir: %t\nsize: %d\nmode: %s\nmod_time: %s",
		info.Name, info.IsDir, info.Size, info.Mode, info.ModTime)

	return out, info, nil
}

func (e *Engine) Delete(path string) (string, error) {
	safePath, err := CheckPathSecurity(path, e.cfg.AllowedRoots)
	if err != nil {
		return "", err
	}

	fi, err := os.Stat(safePath)
	if err != nil {
		return "", err
	}

	if fi.IsDir() {
		return "", fmt.Errorf("%w: deleting directories is not permitted via fs_delete", ErrPermissionDenied)
	}
	if safePath == "/" || safePath == "." {
		return "", fmt.Errorf("%w: cannot delete root directory", ErrPermissionDenied)
	}
	for _, root := range e.cfg.AllowedRoots {
		cRoot, _ := filepath.EvalSymlinks(root)
		if cRoot != "" && safePath == cRoot {
			return "", fmt.Errorf("%w: cannot delete root directory", ErrPermissionDenied)
		}
	}

	if err := os.Remove(safePath); err != nil {
		return "", fmt.Errorf("failed to delete file: %w", err)
	}

	return fmt.Sprintf("deleted file %s", safePath), nil
}
