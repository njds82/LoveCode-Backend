package tier1_features

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF17_Adapters_Media_PlayPause(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "media_control",
		Arguments: map[string]any{
			"action": "play",
		},
		GrantedCapabilities: []string{"adapters:media", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F17: Adapters")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("media play result: %s", execResp.Output)
	}
}

func TestF17_Adapters_Media_VolumeControl(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "media_control",
		Arguments: map[string]any{
			"action": "set_volume",
			"volume": 75,
		},
		GrantedCapabilities: []string{"adapters:media", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F17: Adapters")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("media volume result: %s", execResp.Output)
	}
}

func TestF17_Adapters_Media_TrackInfo(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "media_control",
		Arguments: map[string]any{
			"action": "get_track",
		},
		GrantedCapabilities: []string{"adapters:media", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F17: Adapters")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("media track result: %s", execResp.Output)
	}
}

func TestF17_Adapters_Application_LaunchApp(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "app_control",
		Arguments: map[string]any{
			"action": "launch",
			"app":    "text_editor",
		},
		GrantedCapabilities: []string{"adapters:app", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F17: Adapters")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("app launch result: %s", execResp.Output)
	}
}

func TestF17_Adapters_Application_ListApps(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "app_control",
		Arguments: map[string]any{
			"action": "list",
		},
		GrantedCapabilities: []string{"adapters:app", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F17: Adapters")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("app list result: %s", execResp.Output)
	}
}

func TestF17_Adapters_Unconfigured_GracefulFallback(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling nonexistent / unconfigured desktop action
	req := harness.ToolExecuteRequest{
		ToolName: "media_control",
		Arguments: map[string]any{
			"action": "unknown_action_xyz",
		},
		GrantedCapabilities: []string{"adapters:media", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F17: Adapters")

	// Must handle gracefully without crashing (400, 404, or 200 with fallback)
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected unconfigured adapter status: %d", resp.StatusCode)
	}
}
