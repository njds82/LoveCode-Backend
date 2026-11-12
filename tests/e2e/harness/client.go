package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Client is an opaque-box HTTP client for communicating with LoveCode server.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Token      string
}

// Response wraps an HTTP response with buffered body.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// JSON decodes the response body into target object v.
func (r *Response) JSON(v any) error {
	return json.Unmarshal(r.Body, v)
}

// String returns body as string.
func (r *Response) String() string {
	return string(r.Body)
}

// IsUnregisteredRoute returns true if the server returned standard Go 404
// indicating the route is not registered on the router.
func (r *Response) IsUnregisteredRoute() bool {
	if r.StatusCode != http.StatusNotFound {
		return false
	}
	bodyStr := strings.TrimSpace(string(r.Body))
	return bodyStr == "404 page not found" || strings.Contains(bodyStr, "no such endpoint")
}

// IsToolNotFound returns true if the response indicates the tool was not found or route is unregistered.
func (r *Response) IsToolNotFound() bool {
	if r.StatusCode != http.StatusNotFound {
		return false
	}
	bodyStr := strings.TrimSpace(string(r.Body))
	return bodyStr == "404 page not found" || strings.Contains(bodyStr, "tool_not_found") || strings.Contains(bodyStr, "not_found")
}

// ToolExecuteRequest defines the request payload for POST /v1/tools/execute.
type ToolExecuteRequest struct {
	ToolName            string   `json:"tool_name"`
	Arguments           any      `json:"arguments"`
	RunID               string   `json:"run_id,omitempty"`
	TaskID              string   `json:"task_id,omitempty"`
	AgentID             string   `json:"agent_id,omitempty"`
	TeamID              string   `json:"team_id,omitempty"`
	GrantedCapabilities []string `json:"granted_capabilities,omitempty"`
	TimeoutMS           int64    `json:"timeout_ms,omitempty"`
	MaxOutputBytes      int64    `json:"max_output_bytes,omitempty"`
}

// ToolExecuteResponse defines the response structure from POST /v1/tools/execute.
type ToolExecuteResponse struct {
	ToolName   string `json:"tool_name"`
	Output     string `json:"output"`
	Data       any    `json:"data"`
	Truncated  bool   `json:"truncated"`
	DurationMS int64  `json:"duration_ms"`
	Error      any    `json:"error,omitempty"`
}

// ToolDefinitionResponse defines a tool definition returned by GET /v1/tools.
type ToolDefinitionResponse struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	Category             string          `json:"category"`
	RiskLevel            string          `json:"risk_level"`
	ParametersSchema     json.RawMessage `json:"parameters_schema"`
	RequiredCapabilities []string        `json:"required_capabilities"`
	RequiredPermissions  []string        `json:"required_permissions"`
	TimeoutMS            int64           `json:"timeout_ms"`
	MaxOutputBytes       int64           `json:"max_output_bytes"`
	Enabled              bool            `json:"enabled"`
}

// NewClient creates a new HTTP client.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// WithToken returns a copy of Client with the specified bearer token.
func (c *Client) WithToken(token string) *Client {
	return &Client{
		BaseURL:    c.BaseURL,
		HTTPClient: c.HTTPClient,
		Token:      token,
	}
}

// Do executes an HTTP request with JSON serialization and bearer token injection.
func (c *Client) Do(ctx context.Context, method, path string, body any) (*Response, error) {
	var bodyReader io.Reader
	if body != nil {
		switch v := body.(type) {
		case []byte:
			bodyReader = bytes.NewReader(v)
		case string:
			bodyReader = strings.NewReader(v)
		case io.Reader:
			bodyReader = v
		default:
			data, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("harness.Client: marshal body failed: %w", err)
			}
			bodyReader = bytes.NewReader(data)
		}
	}

	url := c.BaseURL + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("harness.Client: create request failed: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("harness.Client: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("harness.Client: read response body failed: %w", err)
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       respBytes,
	}, nil
}

// Get executes a GET request.
func (c *Client) Get(ctx context.Context, path string) (*Response, error) {
	return c.Do(ctx, http.MethodGet, path, nil)
}

// Post executes a POST request.
func (c *Client) Post(ctx context.Context, path string, body any) (*Response, error) {
	return c.Do(ctx, http.MethodPost, path, body)
}

// Patch executes a PATCH request.
func (c *Client) Patch(ctx context.Context, path string, body any) (*Response, error) {
	return c.Do(ctx, http.MethodPatch, path, body)
}

// Put executes a PUT request.
func (c *Client) Put(ctx context.Context, path string, body any) (*Response, error) {
	return c.Do(ctx, http.MethodPut, path, body)
}

// Delete executes a DELETE request.
func (c *Client) Delete(ctx context.Context, path string) (*Response, error) {
	return c.Do(ctx, http.MethodDelete, path, nil)
}

// GetJSON performs a GET request and decodes the response body into out.
func (c *Client) GetJSON(ctx context.Context, path string, out any) (*Response, error) {
	resp, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	if out != nil && len(resp.Body) > 0 {
		if err := resp.JSON(out); err != nil {
			return resp, fmt.Errorf("decode json failed: %w, body: %s", err, resp.String())
		}
	}
	return resp, nil
}

// PostJSON performs a POST request with body in and decodes the response body into out.
func (c *Client) PostJSON(ctx context.Context, path string, in, out any) (*Response, error) {
	resp, err := c.Post(ctx, path, in)
	if err != nil {
		return nil, err
	}
	if out != nil && len(resp.Body) > 0 {
		if err := resp.JSON(out); err != nil {
			return resp, fmt.Errorf("decode json failed: %w, body: %s", err, resp.String())
		}
	}
	return resp, nil
}

// PatchJSON performs a PATCH request with body in and decodes the response body into out.
func (c *Client) PatchJSON(ctx context.Context, path string, in, out any) (*Response, error) {
	resp, err := c.Patch(ctx, path, in)
	if err != nil {
		return nil, err
	}
	if out != nil && len(resp.Body) > 0 {
		if err := resp.JSON(out); err != nil {
			return resp, fmt.Errorf("decode json failed: %w, body: %s", err, resp.String())
		}
	}
	return resp, nil
}

// DeleteJSON performs a DELETE request and decodes the response body into out.
func (c *Client) DeleteJSON(ctx context.Context, path string, out any) (*Response, error) {
	resp, err := c.Delete(ctx, path)
	if err != nil {
		return nil, err
	}
	if out != nil && len(resp.Body) > 0 {
		if err := resp.JSON(out); err != nil {
			return resp, fmt.Errorf("decode json failed: %w, body: %s", err, resp.String())
		}
	}
	return resp, nil
}

// AssertStatus asserts that the response status matches the expected status code.
func (c *Client) AssertStatus(t testing.TB, resp *Response, expectedStatus int) {
	t.Helper()
	if resp == nil {
		t.Fatalf("expected HTTP status %d, got nil response", expectedStatus)
	}
	if resp.StatusCode != expectedStatus {
		t.Fatalf("expected HTTP status %d, got %d with body: %s", expectedStatus, resp.StatusCode, resp.String())
	}
}

// SkipIfUnregistered skips the test if the response indicates the route is not yet registered.
func (c *Client) SkipIfUnregistered(t testing.TB, resp *Response, featureName string) {
	t.Helper()
	if resp != nil && resp.IsUnregisteredRoute() {
		t.Skipf("Feature %s is not yet registered on server (route not implemented)", featureName)
	}
}

// SkipIfToolNotFound skips the test if the response indicates the tool is not yet registered or found.
func (c *Client) SkipIfToolNotFound(t testing.TB, resp *Response, toolName string) {
	t.Helper()
	if resp != nil && (resp.IsUnregisteredRoute() || resp.IsToolNotFound()) {
		t.Skipf("Tool %s is not yet registered on server", toolName)
	}
}

// ExecuteTool calls POST /v1/tools/execute and returns decoded ToolExecuteResponse.
func (c *Client) ExecuteTool(ctx context.Context, req ToolExecuteRequest) (*ToolExecuteResponse, *Response, error) {
	resp, err := c.Post(ctx, "/v1/tools/execute", req)
	if err != nil {
		return nil, nil, err
	}
	var execResp ToolExecuteResponse
	if len(resp.Body) > 0 {
		_ = json.Unmarshal(resp.Body, &execResp)
	}
	return &execResp, resp, nil
}
