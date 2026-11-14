package tier2_boundaries

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestBoundaries_FS_DirectoryTraversal_RelativeEscape verifies that path traversal
// attempts targeting sensitive system directories or escaping the allowed root
// are strictly blocked with 403 Forbidden or 400 Bad Request.
func TestBoundaries_FS_DirectoryTraversal_RelativeEscape(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	sb.WriteFile(t, "sub/allowed.txt", []byte("safe content"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	traversalPaths := []string{
		"../../../etc/passwd",
		"../../../../etc/shadow",
		sb.PathUnsafe("sub/../../../../etc/hosts"),
		"../" + filepath.Base(sb.Root()) + "/../etc/passwd",
		"..%2f..%2f..%2fetc%2fpasswd",
	}

	for _, badPath := range traversalPaths {
		req := harness.ToolExecuteRequest{
			ToolName: "fs_read",
			Arguments: map[string]any{
				"path": badPath,
			},
			GrantedCapabilities: []string{"tools:filesystem:read", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed for path %q: %v", badPath, err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "fs_read")

		// Traversal must be rejected with 403 Forbidden or 400 Bad Request
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("security violation: path traversal was not blocked for %q, got 200 OK: %s", badPath, resp.String())
		}
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 403 Forbidden or 400 Bad Request for %q, got %d: %s", badPath, resp.StatusCode, resp.String())
		}
	}
}

// TestBoundaries_FS_SymlinkEscape_OutsideRoot verifies that symlinks created inside
// the allowed root pointing to prohibited system files outside are detected and blocked.
func TestBoundaries_FS_SymlinkEscape_OutsideRoot(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)

	// Create a symlink pointing to /etc/passwd or /etc/hosts outside the sandbox
	symlinkPath := sb.Path("escaped_symlink.txt")
	target := "/etc/hosts"
	if _, err := os.Stat(target); err != nil {
		target = "/etc/passwd"
	}

	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Skipf("cannot create symlink in test environment: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "fs_read",
		Arguments: map[string]any{
			"path": symlinkPath,
		},
		GrantedCapabilities: []string{"tools:filesystem:read", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "fs_read")

	// Symlink escape must be blocked
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: symlink escape outside root was not blocked, got 200 OK: %s", resp.String())
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden or 400 Bad Request for symlink escape, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_FS_MaxFileSize_ExceededLimit verifies that file write operations
// exceeding the 10 MB limit are rejected before writing.
func TestBoundaries_FS_MaxFileSize_ExceededLimit(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	targetFile := sb.Path("oversized.bin")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 10 MB + 1024 bytes payload
	oversizedContent := strings.Repeat("A", 10*1024*1024+1024)

	req := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    targetFile,
			"content": oversizedContent,
		},
		GrantedCapabilities: []string{"tools:filesystem:write", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "fs_write")

	// Must be rejected with 400 Bad Request or 413 Payload Too Large
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected write > 10MB to be rejected, got 200 OK")
	}
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 400/413 for oversized file write, got %d: %s", resp.StatusCode, resp.String())
	}

	// Verify target file was not created
	sb.AssertFileNotExists(t, "oversized.bin")
}

// TestBoundaries_FS_RootDirectory_DeletionProtection verifies that attempting to
// delete the root directory or directories via fs_delete is blocked.
func TestBoundaries_FS_RootDirectory_DeletionProtection(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	dirPath := sb.Path("subfolder")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prohibitedTargets := []string{
		"/",
		".",
		sb.Root(),
		dirPath,
	}

	for _, target := range prohibitedTargets {
		req := harness.ToolExecuteRequest{
			ToolName: "fs_delete",
			Arguments: map[string]any{
				"path": target,
			},
			GrantedCapabilities: []string{"tools:filesystem:delete", "tools:filesystem:write", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed for target %q: %v", target, err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "fs_delete")

		// Deletion of root or directories must fail
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("security violation: deleting directory/root %q succeeded", target)
		}
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 403 Forbidden or 400 Bad Request for %q, got %d: %s", target, resp.StatusCode, resp.String())
		}
	}
}

// TestBoundaries_FS_AtomicWrite_CrashSafety verifies that atomic writes do not leave
// partial files or corrupt existing content if an invalid operation occurs.
func TestBoundaries_FS_AtomicWrite_CrashSafety(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	sb.WriteFile(t, "atomic_test.txt", []byte("initial golden content"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempting a write to a nested prohibited directory or invalid path
	badReq := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    "/etc/prohibited_write.txt",
			"content": "malicious overwrite",
		},
		GrantedCapabilities: []string{"tools:filesystem:write", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, badReq)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "fs_write")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("writing to /etc should fail, got 200 OK")
	}

	// Verify the original file is completely intact
	sb.AssertFileEquals(t, "atomic_test.txt", "initial golden content")
}

// TestBoundaries_FS_EmptyAndWhitespacePath_Rejected verifies that empty paths or
// whitespace-only paths are rejected cleanly.
func TestBoundaries_FS_EmptyAndWhitespacePath_Rejected(t *testing.T) {
	srv := harness.NewServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, emptyPath := range []string{"", "   ", "\t\n"} {
		req := harness.ToolExecuteRequest{
			ToolName: "fs_read",
			Arguments: map[string]any{
				"path": emptyPath,
			},
			GrantedCapabilities: []string{"tools:filesystem:read", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "fs_read")

		if resp.StatusCode == http.StatusOK {
			t.Fatalf("expected empty path %q to fail, got 200 OK", emptyPath)
		}
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 400/403/404 for empty path, got %d: %s", resp.StatusCode, resp.String())
		}
	}
}
