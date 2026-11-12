package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lovecode/lovecode/internal/models"
)

// OpenAIConfig configures the OpenAI-compatible adapter. The adapter implements
// the subset of the chat-completions protocol that LoveCode normalizes; it does
// not claim that every OpenAI-compatible service supports every feature.
type OpenAIConfig struct {
	ID          string
	DisplayName string
	Description string

	BaseURL string
	APIKey  string

	DefaultHeaders map[string]string
	Timeout        time.Duration
	HealthTimeout  time.Duration

	// HTTPClient is for tests or callers that need a custom transport. When nil
	// the adapter owns a client with a bounded idle-connection pool.
	HTTPClient *http.Client

	ChatPath   string // default /chat/completions
	ModelsPath string // default /models
	HealthPath string // default ModelsPath
	// SkipHealthCheck makes Health return nil without network I/O.
	SkipHealthCheck bool

	// AuthHeader defaults to Authorization. AuthScheme defaults to Bearer;
	// the special value "none" sends the API key as the raw header value.
	AuthHeader string
	AuthScheme string

	// MaxTokensField is max_tokens (default) or max_completion_tokens.
	MaxTokensField string
	// StreamUsage requests stream_options.include_usage (default true).
	StreamUsage *bool

	// Organization and Project are optional OpenAI headers.
	Organization string
	Project      string

	// ModelMap maps LoveCode model IDs (or aliases) to provider model IDs.
	ModelMap map[string]string
}

// OpenAI is an OpenAI-compatible chat-completions adapter.
type OpenAI struct {
	cfg         OpenAIConfig
	client      *http.Client
	owned       bool
	streamUsage bool
}

// NewOpenAI validates cfg and returns an adapter.
func NewOpenAI(cfg OpenAIConfig) (*OpenAI, error) {
	if cfg.ID == "" {
		return nil, NewError(KindInvalidConfig, "", "openai provider id is required")
	}
	if !ValidProviderID(cfg.ID) {
		return nil, NewError(KindInvalidConfig, cfg.ID, "openai provider id is invalid")
	}
	if cfg.BaseURL == "" {
		return nil, NewError(KindInvalidConfig, cfg.ID, "openai base URL is required")
	}
	if err := ValidateEndpoint(cfg.BaseURL); err != nil {
		return nil, NewError(KindInvalidConfig, cfg.ID, err.Error())
	}
	if err := ValidateHeaders(cfg.DefaultHeaders); err != nil {
		return nil, NewError(KindInvalidConfig, cfg.ID, err.Error())
	}
	if cfg.ChatPath == "" {
		cfg.ChatPath = "/chat/completions"
	}
	if cfg.ModelsPath == "" {
		cfg.ModelsPath = "/models"
	}
	if cfg.HealthPath == "" {
		cfg.HealthPath = cfg.ModelsPath
	}
	if !strings.HasPrefix(cfg.ChatPath, "/") || !strings.HasPrefix(cfg.ModelsPath, "/") || !strings.HasPrefix(cfg.HealthPath, "/") {
		return nil, NewError(KindInvalidConfig, cfg.ID, "openai endpoint paths must start with /")
	}
	if cfg.AuthHeader == "" {
		cfg.AuthHeader = "Authorization"
	}
	if cfg.AuthScheme == "" {
		cfg.AuthScheme = "Bearer"
	}
	switch cfg.MaxTokensField {
	case "":
		cfg.MaxTokensField = "max_tokens"
	case "max_tokens", "max_completion_tokens":
	default:
		return nil, NewError(KindInvalidConfig, cfg.ID, "max_tokens_field must be max_tokens or max_completion_tokens")
	}
	for k, v := range cfg.ModelMap {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return nil, NewError(KindInvalidConfig, cfg.ID, "model map keys and values must not be empty")
		}
	}
	if cfg.MaxTokensField == "" {
		cfg.MaxTokensField = "max_tokens"
	}
	streamUsage := true
	if cfg.StreamUsage != nil {
		streamUsage = *cfg.StreamUsage
	}
	client, owned := httpClientFor(cfg.HTTPClient, cfg.Timeout)
	return &OpenAI{cfg: cfg, client: client, owned: owned, streamUsage: streamUsage}, nil
}

func (o *OpenAI) ID() string   { return o.cfg.ID }
func (o *OpenAI) Kind() string { return "openai" }

func (o *OpenAI) Capabilities() ProviderCapabilities {
	health := models.SupportSupported
	if o.cfg.SkipHealthCheck {
		health = models.SupportUnsupported
	}
	return ProviderCapabilities{
		Streaming:        models.SupportSupported,
		ToolCalls:        models.SupportSupported,
		StructuredOutput: models.SupportSupported,
		ModelListing:     models.SupportSupported,
		HealthCheck:      health,
	}
}

func (o *OpenAI) Info() ProviderInfo {
	name := o.cfg.DisplayName
	if name == "" {
		name = o.cfg.ID
	}
	return ProviderInfo{
		ID:           o.cfg.ID,
		Kind:         "openai",
		DisplayName:  name,
		Description:  o.cfg.Description,
		Capabilities: o.Capabilities(),
	}
}

func (o *OpenAI) Init(context.Context) error { return nil }

func (o *OpenAI) Close(context.Context) error {
	closeHTTPClient(o.client, o.owned)
	return nil
}

func (o *OpenAI) ListModels(ctx context.Context) ([]ModelInfo, error) {
	resp, err := o.do(ctx, http.MethodGet, o.cfg.ModelsPath, nil, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, maxJSONResponse)
	if err != nil {
		return nil, FromMalformed(o.cfg.ID, "could not read model list")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, o.responseError(resp, body)
	}
	var parsed openAIModelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, FromMalformed(o.cfg.ID, "provider returned invalid JSON for model list")
	}
	out := make([]ModelInfo, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		info := ModelInfo{
			ProviderModelID:  item.ID,
			DisplayName:      item.ID,
			Streaming:        models.SupportUnknown,
			ToolCalls:        models.SupportUnknown,
			StructuredOutput: models.SupportUnknown,
			Reasoning:        models.SupportUnknown,
		}
		if item.OwnedBy != "" || item.Created != 0 {
			info.Metadata = map[string]string{}
			if item.OwnedBy != "" {
				info.Metadata["owned_by"] = item.OwnedBy
			}
			if item.Created != 0 {
				info.Metadata["created"] = fmt.Sprintf("%d", item.Created)
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (o *OpenAI) Health(ctx context.Context) error {
	if o.cfg.SkipHealthCheck {
		return nil
	}
	resp, err := o.do(ctx, http.MethodGet, o.cfg.HealthPath, nil, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, maxErrorBodyBytes)
	if err != nil {
		return FromMalformed(o.cfg.ID, "could not read health response")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return o.responseError(resp, body)
	}
	return nil
}

func (o *OpenAI) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, wrapInvalidRequest(o.cfg.ID, err)
	}
	payload, err := o.requestBody(req, false)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	resp, err := o.do(ctx, http.MethodPost, o.cfg.ChatPath, payload, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, maxJSONResponse)
	if err != nil {
		return nil, FromMalformed(o.cfg.ID, "could not read provider response")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, o.responseError(resp, body)
	}
	var parsed openAIChatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, FromMalformed(o.cfg.ID, "provider returned invalid JSON chat response")
	}
	if len(parsed.Choices) == 0 {
		return nil, FromMalformed(o.cfg.ID, "provider response contained no choices")
	}
	choice := parsed.Choices[0]
	text, err := decodeContent(choice.Message.Content)
	if err != nil {
		return nil, FromMalformed(o.cfg.ID, "provider returned malformed message content")
	}
	message := Message{
		Role:    RoleAssistant,
		Content: []ContentPart{TextPart(text)},
	}
	for _, tc := range choice.Message.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, ToolCall{
			ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
		})
	}
	if text == "" {
		message.Content = nil
	}
	completed := time.Now()
	respID := parsed.ID
	if respID == "" {
		respID = providerRequestID(resp)
	}
	modelID := parsed.Model
	if modelID == "" {
		modelID = o.upstreamModel(req.Model)
	}
	out := &ChatResponse{
		ProviderID:        o.cfg.ID,
		Model:             modelID,
		Message:           message,
		FinishReason:      mapFinishReason(choice.FinishReason),
		Usage:             convertUsage(parsed.Usage),
		ProviderRequestID: respID,
		Timing: Timing{
			StartedAt:   started,
			CompletedAt: completed,
			Duration:    completed.Sub(started),
		},
	}
	return out, nil
}

func (o *OpenAI) ChatStream(ctx context.Context, req ChatRequest) (Stream, error) {
	if err := req.Validate(); err != nil {
		return nil, wrapInvalidRequest(o.cfg.ID, err)
	}
	payload, err := o.requestBody(req, true)
	if err != nil {
		return nil, err
	}
	resp, err := o.do(ctx, http.MethodPost, o.cfg.ChatPath, payload, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, readErr := readLimited(resp.Body, maxErrorBodyBytes)
		resp.Body.Close()
		if readErr != nil {
			return nil, FromMalformed(o.cfg.ID, "could not read provider error response")
		}
		return nil, o.responseError(resp, body)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	return &openAIStream{
		provider: o,
		ctx:      ctx,
		body:     resp.Body,
		scanner:  scanner,
		started:  time.Now(),
		model:    o.upstreamModel(req.Model),
		reqID:    providerRequestID(resp),
		finish:   FinishUnknown,
	}, nil
}

func (o *OpenAI) upstreamModel(model string) string {
	if mapped, ok := o.cfg.ModelMap[model]; ok {
		return mapped
	}
	return model
}

func (o *OpenAI) requestBody(req ChatRequest, stream bool) ([]byte, error) {
	messages, err := convertMessages(req.Messages)
	if err != nil {
		e := NewError(KindUnsupported, o.cfg.ID, err.Error())
		e.cause = err
		return nil, e
	}
	body := make(map[string]any, len(req.ProviderOptions)+12)
	for k, v := range req.ProviderOptions {
		body[k] = v
	}
	body["model"] = o.upstreamModel(req.Model)
	body["messages"] = messages
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		body[o.cfg.MaxTokensField] = *req.MaxTokens
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
	if len(req.Tools) > 0 {
		body["tools"] = convertTools(req.Tools)
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = convertToolChoice(req.ToolChoice)
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type != FormatText {
		rf, err := convertResponseFormat(req.ResponseFormat)
		if err != nil {
			return nil, NewError(KindInvalidRequest, o.cfg.ID, err.Error())
		}
		body["response_format"] = rf
	}
	if stream {
		body["stream"] = true
		if o.streamUsage {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, NewError(KindInvalidRequest, o.cfg.ID, "request could not be encoded")
	}
	return raw, nil
}

func convertMessages(messages []Message) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		m := map[string]any{"role": string(msg.Role)}
		content, err := convertContent(msg.Content)
		if err != nil {
			return nil, fmt.Errorf("message for role %s: %w", msg.Role, err)
		}
		if content != nil {
			m["content"] = content
		}
		if msg.Name != "" {
			m["name"] = msg.Name
		}
		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		if len(msg.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				calls = append(calls, map[string]any{
					"id": tc.ID, "type": "function",
					"function": map[string]any{"name": tc.Name, "arguments": tc.Arguments},
				})
			}
			m["tool_calls"] = calls
		}
		out = append(out, m)
	}
	return out, nil
}

func convertContent(parts []ContentPart) (any, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	allText := true
	for _, p := range parts {
		if p.Type != PartText {
			allText = false
			break
		}
	}
	if allText {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String(), nil
	}
	arr := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case PartText:
			arr = append(arr, map[string]any{"type": "text", "text": p.Text})
		case PartImage:
			img := map[string]any{"url": p.Image.URL}
			if p.Image.Detail != "" {
				img["detail"] = p.Image.Detail
			}
			arr = append(arr, map[string]any{"type": "image_url", "image_url": img})
		case PartAudio, PartVideo:
			return nil, fmt.Errorf("content part type %q is not supported by the OpenAI-compatible adapter", p.Type)
		default:
			return nil, fmt.Errorf("content part type %q is not supported", p.Type)
		}
	}
	return arr, nil
}

func convertTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn := map[string]any{"name": t.Name, "parameters": t.Parameters}
		if t.Description != "" {
			fn["description"] = t.Description
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func convertToolChoice(tc *ToolChoice) any {
	switch tc.Mode {
	case ToolChoiceTool:
		return map[string]any{"type": "function", "function": map[string]any{"name": tc.Name}}
	default:
		return string(tc.Mode)
	}
}

func convertResponseFormat(rf *ResponseFormat) (map[string]any, error) {
	switch rf.Type {
	case FormatJSONObject:
		return map[string]any{"type": "json_object"}, nil
	case FormatJSONSchema:
		if rf.Schema == nil {
			return nil, errors.New("json_schema requires a schema")
		}
		js := map[string]any{"name": rf.Schema.Name, "schema": rf.Schema.Schema}
		if rf.Schema.Description != "" {
			js["description"] = rf.Schema.Description
		}
		if rf.Schema.Strict != nil {
			js["strict"] = *rf.Schema.Strict
		}
		return map[string]any{"type": "json_schema", "json_schema": js}, nil
	default:
		return nil, fmt.Errorf("response format %q is not supported", rf.Type)
	}
}

func (o *OpenAI) do(ctx context.Context, method, path string, body []byte, stream bool) (*http.Response, error) {
	endpoint, err := joinURL(o.cfg.BaseURL, path)
	if err != nil {
		return nil, NewError(KindInvalidConfig, o.cfg.ID, err.Error())
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, NewError(KindInvalidConfig, o.cfg.ID, "could not construct provider request")
	}
	req.Header.Set("User-Agent", "LoveCode")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	applyHeaders(req, o.cfg.DefaultHeaders)
	if o.cfg.APIKey != "" {
		value := o.cfg.APIKey
		if o.cfg.AuthScheme != "" && o.cfg.AuthScheme != "none" {
			value = o.cfg.AuthScheme + " " + value
		}
		req.Header.Set(o.cfg.AuthHeader, value)
	}
	if o.cfg.Organization != "" {
		req.Header.Set("OpenAI-Organization", o.cfg.Organization)
	}
	if o.cfg.Project != "" {
		req.Header.Set("OpenAI-Project", o.cfg.Project)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, FromTransport(ctx, o.cfg.ID, err)
	}
	return resp, nil
}

func (o *OpenAI) responseError(resp *http.Response, body []byte) *Error {
	msg, code := decodeOpenAIError(body)
	e := FromStatus(o.cfg.ID, resp.StatusCode, code, msg, retryAfterFromResponse(resp), o.cfg.APIKey)
	if e.ProviderRequestID == "" {
		e.ProviderRequestID = providerRequestID(resp)
	}
	return e
}

func mapFinishReason(s *string) FinishReason {
	if s == nil {
		return FinishUnknown
	}
	switch *s {
	case "stop":
		return FinishStop
	case "length":
		return FinishLength
	case "tool_calls", "function_call":
		return FinishToolCalls
	case "content_filter":
		return FinishContentFilter
	case "error":
		return FinishError
	default:
		return FinishUnknown
	}
}

type openAIModelsResponse struct {
	Data []struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
		Created int64  `json:"created"`
	} `json:"data"`
}

type openAIUsage struct {
	PromptTokens        *int `json:"prompt_tokens"`
	CompletionTokens    *int `json:"completion_tokens"`
	TotalTokens         *int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens *int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func convertUsage(u *openAIUsage) *Usage {
	if u == nil {
		return nil
	}
	out := &Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		out.CachedPromptTokens = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil {
		out.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return out
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

func decodeContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "" || p.Type == "text" || p.Type == "output_text" {
			b.WriteString(p.Text)
		}
	}
	return b.String(), nil
}

type openAIErrorBody struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
	Param   string          `json:"param"`
}

type openAIErrorEnvelope struct {
	Error openAIErrorBody `json:"error"`
}

func decodeOpenAIError(body []byte) (message, code string) {
	var env openAIErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && (env.Error.Message != "" || len(env.Error.Code) > 0) {
		code = rawCodeString(env.Error.Code)
		if code == "" {
			code = env.Error.Type
		}
		return env.Error.Message, code
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		text = "provider returned an error"
	}
	return text, ""
}

func rawCodeString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

// ---- streaming ----

type openAIStream struct {
	provider *OpenAI
	ctx      context.Context
	body     io.ReadCloser
	scanner  *bufio.Scanner
	started  time.Time
	firstAt  *time.Time
	model    string
	reqID    string
	usage    *Usage
	finish   FinishReason
	pending  []StreamEvent
	doneSent bool
	closed   bool
	terminal error
}

func (s *openAIStream) Recv(ctx context.Context) (StreamEvent, error) {
	if s.terminal != nil {
		return StreamEvent{}, s.terminal
	}
	if s.closed {
		return StreamEvent{}, io.EOF
	}
	if err := s.checkContext(ctx); err != nil {
		s.terminal = err
		return StreamEvent{}, err
	}
	if len(s.pending) > 0 {
		ev := s.pending[0]
		s.pending = s.pending[1:]
		return ev, nil
	}
	if s.doneSent {
		s.closed = true
		_ = s.body.Close()
		return StreamEvent{}, io.EOF
	}

	for {
		data, err := s.nextEvent()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if s.doneSent {
					s.closed = true
					_ = s.body.Close()
					return StreamEvent{}, io.EOF
				}
				return s.doneEvent(), nil
			}
			return StreamEvent{}, err
		}
		if data == "" {
			continue
		}
		if strings.TrimSpace(data) == "[DONE]" {
			return s.doneEvent(), nil
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			s.terminal = FromMalformed(s.provider.cfg.ID, "provider sent invalid JSON in the event stream")
			s.closeBody()
			return StreamEvent{}, s.terminal
		}
		if chunk.Error != nil {
			kind := kindFromStreamError(chunk.Error)
			s.terminal = &Error{
				Kind:              kind,
				ProviderID:        s.provider.cfg.ID,
				Message:           sanitizeWithSecret(chunk.Error.Message, s.provider.cfg.APIKey),
				ProviderCode:      SanitizeText(chunk.Error.CodeString()),
				ProviderRequestID: s.reqID,
			}
			s.closeBody()
			return StreamEvent{}, s.terminal
		}
		if chunk.Usage != nil {
			s.usage = convertUsage(chunk.Usage)
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil {
				s.finish = mapFinishReason(choice.FinishReason)
			}
			text, err := decodeContent(choice.Delta.Content)
			if err != nil {
				s.terminal = FromMalformed(s.provider.cfg.ID, "provider sent malformed stream content")
				s.closeBody()
				return StreamEvent{}, s.terminal
			}
			if text != "" {
				s.noteFirst()
				s.pending = append(s.pending, StreamEvent{Type: StreamTextDelta, Text: text})
			}
			for _, tc := range choice.Delta.ToolCalls {
				s.noteFirst()
				s.pending = append(s.pending, StreamEvent{
					Type: StreamToolCallDelta,
					ToolCall: &ToolCallDelta{
						Index: tc.Index, ID: tc.ID, Name: tc.Function.Name, ArgumentsDelta: tc.Function.Arguments,
					},
				})
			}
		}
		if len(s.pending) > 0 {
			ev := s.pending[0]
			s.pending = s.pending[1:]
			return ev, nil
		}
	}
}

// nextEvent returns one SSE event payload. An empty string with nil error means
// a non-data event (comment/keepalive). io.EOF means the body ended.
func (s *openAIStream) nextEvent() (string, error) {
	var data strings.Builder
	for s.scanner.Scan() {
		line := strings.TrimSuffix(s.scanner.Text(), "\r")
		if line == "" {
			if data.Len() > 0 {
				return data.String(), nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		if field == "data" {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if err := s.scanner.Err(); err != nil {
		if s.ctx.Err() != nil {
			s.terminal = ctxError(s.provider.cfg.ID, s.ctx.Err())
		} else {
			s.terminal = FromMalformed(s.provider.cfg.ID, "error reading event stream")
		}
		s.closeBody()
		return "", s.terminal
	}
	if data.Len() > 0 {
		return data.String(), nil
	}
	return "", io.EOF
}

func (s *openAIStream) doneEvent() StreamEvent {
	s.doneSent = true
	completed := time.Now()
	timing := &Timing{StartedAt: s.started, CompletedAt: completed, Duration: completed.Sub(s.started)}
	if s.firstAt != nil {
		timing.FirstTokenAt = s.firstAt
		timing.TimeToFirstToken = s.firstAt.Sub(s.started)
	}
	return StreamEvent{
		Type:         StreamDone,
		FinishReason: s.finish,
		Usage:        cloneUsage(s.usage),
		Timing:       timing,
	}
}

func (s *openAIStream) noteFirst() {
	if s.firstAt == nil {
		now := time.Now()
		s.firstAt = &now
	}
}

func (s *openAIStream) checkContext(ctx context.Context) error {
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.closeBody()
			return ctxError(s.provider.cfg.ID, err)
		}
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			s.closeBody()
			return ctxError(s.provider.cfg.ID, err)
		}
	}
	return nil
}

func (s *openAIStream) closeBody() {
	if !s.closed {
		s.closed = true
		_ = s.body.Close()
	}
}

func (s *openAIStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.body.Close()
}

type openAIStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage       `json:"usage"`
	Error *openAIStreamError `json:"error"`
}

type openAIStreamError struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
}

func (e *openAIStreamError) CodeString() string {
	c := rawCodeString(e.Code)
	if c == "" {
		c = e.Type
	}
	return c
}

func kindFromStreamError(e *openAIStreamError) Kind {
	code := strings.ToLower(e.CodeString() + " " + e.Type)
	switch {
	case strings.Contains(code, "quota") || strings.Contains(code, "billing"):
		return KindQuotaExceeded
	case strings.Contains(code, "rate_limit") || strings.Contains(code, "rate limit"):
		return KindRateLimited
	case strings.Contains(code, "auth") || strings.Contains(code, "api_key") || strings.Contains(code, "unauthorized"):
		return KindAuthentication
	case strings.Contains(code, "model") && strings.Contains(code, "not"):
		return KindModelNotFound
	case strings.Contains(code, "timeout"):
		return KindTimeout
	default:
		return KindInternal
	}
}

var _ Provider = (*OpenAI)(nil)
