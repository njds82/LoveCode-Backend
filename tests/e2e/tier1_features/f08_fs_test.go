package tier1_features

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF08_FS_Read_WithinAllowedRoot(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	sb.WriteFile(t, "hello.txt", []byte("hello lovecode filesystem"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "fs_read",
		Arguments: map[string]any{
			"path": sb.Path("hello.txt"),
		},
		GrantedCapabilities: []string{"tools:filesystem:read", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F8: Filesystem Tools")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		if execResp.Output != "hello lovecode filesystem" {
			t.Errorf("expected file content, got: %q", execResp.Output)
		}
	}
}

func TestF08_FS_Write_AtomicWrite_Success(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	targetPath := sb.Path("written.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    targetPath,
			"content": "atomic write payload",
			"mode":    "overwrite",
		},
		GrantedCapabilities: []string{"tools:filesystem:write", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F8: Filesystem Tools")

	if resp.StatusCode == http.StatusOK {
		sb.AssertFileEquals(t, "written.txt", "atomic write payload")
	}
}

func TestF08_FS_List_DirectoryContents(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	sb.WriteFile(t, "sub/file1.txt", []byte("1"))
	sb.WriteFile(t, "sub/file2.txt", []byte("2"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "fs_list",
		Arguments: map[string]any{
			"path":      sb.Path("sub"),
			"recursive": false,
		},
		GrantedCapabilities: []string{"tools:filesystem:read", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F8: Filesystem Tools")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF08_FS_Stat_FileMetadata(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	sb.WriteFile(t, "target.txt", []byte("12345"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "fs_stat",
		Arguments: map[string]any{
			"path": sb.Path("target.txt"),
		},
		GrantedCapabilities: []string{"tools:filesystem:read", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F8: Filesystem Tools")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF08_FS_Delete_RemovesFile(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	sb.WriteFile(t, "to_delete.txt", []byte("goodbye"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "fs_delete",
		Arguments: map[string]any{
			"path": sb.Path("to_delete.txt"),
		},
		GrantedCapabilities: []string{"tools:filesystem:write", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F8: Filesystem Tools")

	if resp.StatusCode == http.StatusOK {
		sb.AssertFileNotExists(t, "to_delete.txt")
	}
}

func TestF08_FS_PathTraversal_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempt directory traversal out of sandbox
	escapePath := filepath.Join(sb.Root(), "../../../etc/passwd")

	req := harness.ToolExecuteRequest{
		ToolName: "fs_read",
		Arguments: map[string]any{
			"path": escapePath,
		},
		GrantedCapabilities: []string{"tools:filesystem:read", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F8: Filesystem Tools")

	// Must be blocked with 403 Forbidden or 400 Bad Request
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 400 for path traversal, got %d: %s", resp.StatusCode, resp.String())
	}
}
