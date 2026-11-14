package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemTools_ReadWriteListStatDelete(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		AllowedRoots:  []string{tempDir},
		MaxReadBytes:  MaxFileSizeRead,
		MaxWriteBytes: MaxFileSizeWrite,
	}

	engine := NewEngine(cfg)

	// 1. Write file
	targetFile := filepath.Join(tempDir, "test.txt")
	out, err := engine.Write(targetFile, "hello lovecode", "overwrite")
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if !strings.Contains(out, "successfully wrote") {
		t.Errorf("unexpected write output: %s", out)
	}

	// 2. Stat file
	_, info, err := engine.Stat(targetFile)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Size != int64(len("hello lovecode")) {
		t.Errorf("unexpected stat info: %+v", info)
	}

	// 3. Read file
	content, err := engine.Read(targetFile, 0, 0)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if content != "hello lovecode" {
		t.Errorf("expected 'hello lovecode', got %q", content)
	}

	// 4. Append to file
	_, err = engine.Write(targetFile, " appended", "append")
	if err != nil {
		t.Fatalf("append failed: %v", err)
	}
	content, err = engine.Read(targetFile, 0, 0)
	if err != nil {
		t.Fatalf("read after append failed: %v", err)
	}
	if content != "hello lovecode appended" {
		t.Errorf("expected appended content, got %q", content)
	}

	// 5. List directory
	_, entries, err := engine.List(tempDir, false)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "test.txt" {
		t.Errorf("unexpected list data: %+v", entries)
	}

	// 6. Delete file
	_, err = engine.Delete(targetFile)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted, stat err: %v", err)
	}
}

func TestFilesystemTools_PathTraversal_Rejected(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		AllowedRoots: []string{tempDir},
	}
	engine := NewEngine(cfg)

	traversalPaths := []string{
		"../../../etc/passwd",
		filepath.Join(tempDir, "../../../etc/shadow"),
		tempDir + "/sub/../../../../etc/hosts",
		"/etc/passwd",
		"/root/.ssh/id_rsa",
	}

	for _, p := range traversalPaths {
		t.Run("Read_"+p, func(t *testing.T) {
			_, err := engine.Read(p, 0, 0)
			if err == nil {
				t.Fatalf("expected error for traversal path %q, got nil", p)
			}
			if !errors.Is(err, ErrPermissionDenied) && !errors.Is(err, ErrSchemaValidation) {
				t.Errorf("expected ErrPermissionDenied or ErrSchemaValidation, got: %v", err)
			}
		})

		t.Run("Write_"+p, func(t *testing.T) {
			_, err := engine.Write(p, "attack", "overwrite")
			if err == nil {
				t.Fatalf("expected error for traversal write %q, got nil", p)
			}
		})

		t.Run("Delete_"+p, func(t *testing.T) {
			_, err := engine.Delete(p)
			if err == nil {
				t.Fatalf("expected error for traversal delete %q, got nil", p)
			}
		})
	}
}

func TestFilesystemTools_SymlinkEscape_Rejected(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		AllowedRoots: []string{tempDir},
	}
	engine := NewEngine(cfg)

	symlinkPath := filepath.Join(tempDir, "escape_link")
	if err := os.Symlink("/etc/hosts", symlinkPath); err != nil {
		t.Skipf("cannot create symlink on this OS: %v", err)
	}

	_, err := engine.Read(symlinkPath, 0, 0)
	if err == nil {
		t.Fatal("expected error reading symlink escaping root, got nil")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied for symlink escape, got %v", err)
	}
}

func TestFilesystemTools_DirectoryDeletion_Protected(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		AllowedRoots: []string{tempDir},
	}
	engine := NewEngine(cfg)

	subDir := filepath.Join(tempDir, "subfolder")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Attempting to delete directory must fail
	_, err := engine.Delete(subDir)
	if err == nil {
		t.Fatal("expected error deleting directory, got nil")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}

	// Attempting to delete root directory must fail
	_, err = engine.Delete(tempDir)
	if err == nil {
		t.Fatal("expected error deleting root directory, got nil")
	}
}

func TestFilesystemTools_SizeLimits(t *testing.T) {
	tempDir := t.TempDir()
	cfg := Config{
		AllowedRoots:  []string{tempDir},
		MaxReadBytes:  100,
		MaxWriteBytes: 100,
	}

	engine := NewEngine(cfg)

	// Write exceeding limit
	largePayload := strings.Repeat("A", 150)
	target := filepath.Join(tempDir, "large.txt")
	_, err := engine.Write(target, largePayload, "overwrite")
	if err == nil {
		t.Fatal("expected error for write exceeding max size, got nil")
	}
	if !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("expected ErrSchemaValidation, got %v", err)
	}

	// Write small file then read with tiny limit
	_ = os.WriteFile(target, []byte(strings.Repeat("B", 150)), 0o644)
	_, err = engine.Read(target, 0, 0)
	if err == nil {
		t.Fatal("expected error for read exceeding max size, got nil")
	}
	if !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("expected ErrSchemaValidation, got %v", err)
	}
}

func TestFilesystemTools_EmptyAndWhitespacePath_Rejected(t *testing.T) {
	cfg := Config{}
	engine := NewEngine(cfg)

	for _, p := range []string{"", "   ", "\t\n"} {
		_, err := engine.Read(p, 0, 0)
		if err == nil {
			t.Errorf("expected error for empty path %q, got nil", p)
		}
		if !errors.Is(err, ErrSchemaValidation) {
			t.Errorf("expected ErrSchemaValidation, got: %v", err)
		}
	}
}
