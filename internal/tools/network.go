package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lovecode/lovecode/internal/tools/network"
)

// HTTPRequestTool exposes scoped HTTP requests with strict SSRF protection.
type HTTPRequestTool struct {
	client *network.Client
}

func NewHTTPRequestTool() *HTTPRequestTool {
	return &HTTPRequestTool{
		client: network.NewClient(network.DefaultMaxRedirects),
	}
}

func (t *HTTPRequestTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "http_request",
		Name:        "http_request",
		Description: "Send an HTTP request with strict anti-SSRF protection, timeout, and response size limits.",
		Category:    CategoryNetwork,
		RiskLevel:   RiskMedium,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"url": {
					"type": "string",
					"description": "Destination URL (http or https only)"
				},
				"method": {
					"type": "string",
					"description": "HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS)",
					"enum": ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"],
					"default": "GET"
				},
				"headers": {
					"type": "object",
					"description": "Optional request headers",
					"additionalProperties": { "type": "string" }
				},
				"body": {
					"type": "string",
					"description": "Optional request body"
				},
				"timeout_seconds": {
					"type": "integer",
					"description": "Request timeout in seconds (1 to 60)",
					"minimum": 1,
					"maximum": 60
				},
				"max_response_bytes": {
					"type": "integer",
					"description": "Maximum response body bytes to read",
					"minimum": 1,
					"maximum": 10485760
				}
			},
			"required": ["url"]
		}`),
		RequiredCapabilities: []string{"network:request"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

func (t *HTTPRequestTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var req network.RequestArgs
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("%w: invalid arguments: %v", ErrSchemaValidation, err)
	}

	data, err := t.client.Execute(ctx, req)
	if err != nil {
		if errors.Is(err, network.ErrSSRFBlocked) {
			// Wrap with ErrPermissionDenied so API layer returns 403 Forbidden
			return nil, fmt.Errorf("%w: %v", ErrPermissionDenied, err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrExecutionTimeout
		}
		return nil, err
	}

	output := fmt.Sprintf("HTTP %d %s\n\n%s", data.StatusCode, data.Status, data.Body)
	if data.Truncated {
		output += "\n\n[Response truncated: exceeded byte limit]"
	}

	return &ExecutionResult{
		ToolName:  "http_request",
		Output:    output,
		Data:      data,
		Truncated: data.Truncated,
		Duration:  time.Duration(data.DurationMS) * time.Millisecond,
	}, nil
}
