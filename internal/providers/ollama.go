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
	"strconv"
	"strings"
	"time"

	"github.com/lovecode/lovecode/internal/models"
)

// OllamaConfig configures the optional Ollama adapter. Ollama is never required
// for LoveCode startup and the adapter never downloads models.
type OllamaConfig struct {
	ID          string
	DisplayName string
	Description string

	BaseURL string
	Timeout time.Duration

	HTTPClient *http.Client

	ChatPath    string // default /api/chat
	TagsPath    string // default /api/tags
	VersionPath string // default /api/version

	KeepAlive string
	ModelMap  map[string]string
}

// Ollama is a local Ollama API adapter.
type Ollama struct {
	cfg    OllamaConfig
	client *http.Client
	owned  bool
}

func NewOllama(cfg OllamaConfig) (*Ollama, error) {
	if cfg.ID == "" {
		return nil, NewError(KindInvalidConfig, "", "ollama provider id is required")
	}
	if !ValidProviderID(cfg.ID) {
		return nil, NewError(KindInvalidConfig, cfg.ID, "ollama provider id is invalid")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:11434"
	}
	if err := ValidateEndpoint(cfg.BaseURL); err != nil {
		return nil, NewError(KindInvalidConfig, cfg.ID, err.Error())
	}
	if cfg.ChatPath == "" {
		cfg.ChatPath = "/api/chat"
	}
	if cfg.TagsPath == "" {
		cfg.TagsPath = "/api/tags"
	}
	if cfg.VersionPath == "" {
		cfg.VersionPath = "/api/version"
	}
	if !strings.HasPrefix(cfg.ChatPath, "/") || !strings.HasPrefix(cfg.TagsPath, "/") || !strings.HasPrefix(cfg.VersionPath, "/") {
		return nil, NewError(KindInvalidConfig, cfg.ID, "ollama endpoint paths must start with /")
	}
	for k, v := range cfg.ModelMap {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return nil, NewError(KindInvalidConfig, cfg.ID, "model map keys and values must not be empty")
		}
	}
	client, owned := httpClientFor(cfg.HTTPClient, cfg.Timeout)
	return &Ollama{cfg: cfg, client: client, owned: owned}, nil
}

func (o *Ollama) ID() string   { return o.cfg.ID }
func (o *Ollama) Kind() string { return "ollama" }

func (o *Ollama) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		Streaming:        models.SupportSupported,
		ToolCalls:        models.SupportSupported,
		StructuredOutput: models.SupportSupported,
		ModelListing:     models.SupportSupported,
		HealthCheck:      models.SupportSupported,
	}
}

func (o *Ollama) Info() ProviderInfo {
	name := o.cfg.DisplayName
	if name == "" {
		name = o.cfg.ID
	}
	return ProviderInfo{
		ID:           o.cfg.ID,
		Kind:         "ollama",
		DisplayName:  name,
		Description:  o.cfg.Description,
		Capabilities: o.Capabilities(),
	}
}

func (o *Ollama) Init(context.Context) error { return nil }

func (o *Ollama) Close(context.Context) error {
	closeHTTPClient(o.client, o.owned)
	return nil
}

func (o *Ollama) ListModels(ctx context.Context) ([]ModelInfo, error) {
	resp, err := o.do(ctx, http.MethodGet, o.cfg.TagsPath, nil, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, maxJSONResponse)
	if err != nil {
		return nil, FromMalformed(o.cfg.ID, "could not read ollama model list")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, o.responseError(resp, body)
	}
	var parsed ollamaTagsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, FromMalformed(o.cfg.ID, "ollama returned invalid JSON for model list")
	}
	out := make([]ModelInfo, 0, len(parsed.Models))
	for _, item := range parsed.Models {
		id := item.Name
		if id == "" {
			id = item.Model
		}
		if strings.TrimSpace(id) == "" {
			continue
		}
		info := ModelInfo{
			ProviderModelID:  id,
			DisplayName:      id,
			Streaming:        models.SupportUnknown,
			ToolCalls:        models.SupportUnknown,
			StructuredOutput: models.SupportUnknown,
			Reasoning:        models.SupportUnknown,
		}
		meta := map[string]string{}
		if item.Model != "" {
			meta["model"] = item.Model
		}
		if item.ModifiedAt != "" {
			meta["modified_at"] = item.ModifiedAt
		}
		if item.Size != 0 {
			meta["size_bytes"] = strconv.FormatInt(item.Size, 10)
		}
		if item.Digest != "" {
			meta["digest"] = item.Digest
		}
		if item.Details.Format != "" {
			meta["format"] = item.Details.Format
		}
		if item.Details.Family != "" {
			meta["family"] = item.Details.Family
		}
		if item.Details.ParameterSize != "" {
			meta["parameter_size"] = item.Details.ParameterSize
		}
		if item.Details.QuantizationLevel != "" {
			meta["quantization_level"] = item.Details.QuantizationLevel
		}
		if len(meta) > 0 {
			info.Metadata = meta
		}
		out = append(out, info)
	}
	return out, nil
}

func (o *Ollama) Health(ctx context.Context) error {
	resp, err := o.do(ctx, http.MethodGet, o.cfg.VersionPath, nil, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, maxErrorBodyBytes)
	if err != nil {
		return FromMalformed(o.cfg.ID, "could not read ollama health response")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if resp.StatusCode == http.StatusNotFound {
			return NewError(KindUnavailable, o.cfg.ID, "ollama version endpoint not found")
		}
		return o.responseError(resp, body)
	}
	return nil
}

func (o *Ollama) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
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
		return nil, FromMalformed(o.cfg.ID, "could not read ollama response")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, o.responseError(resp, body)
	}
	var parsed ollamaChatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, FromMalformed(o.cfg.ID, "ollama returned invalid JSON chat response")
	}
	completed := time.Now()
	message := Message{Role: RoleAssistant, Content: []ContentPart{TextPart(parsed.Message.Content)}}
	if parsed.Message.Content == "" {
		message.Content = nil
	}
	for _, tc := range parsed.Message.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, ToolCall{
			Name: tc.Function.Name, Arguments: rawArgumentsString(tc.Function.Arguments),
		})
	}
	modelID := parsed.Model
	if modelID == "" {
		modelID = o.upstreamModel(req.Model)
	}
	return &ChatResponse{
		ProviderID:   o.cfg.ID,
		Model:        modelID,
		Message:      message,
		FinishReason: mapOllamaFinish(parsed.DoneReason),
		Usage:        ollamaUsage(parsed.PromptEvalCount, parsed.EvalCount),
		Metadata:     ollamaMetadata(parsed),
		Timing: Timing{
			StartedAt:   started,
			CompletedAt: completed,
			Duration:    completed.Sub(started),
		},
	}, nil
}

func (o *Ollama) ChatStream(ctx context.Context, req ChatRequest) (Stream, error) {
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
			return nil, FromMalformed(o.cfg.ID, "could not read ollama error response")
		}
		return nil, o.responseError(resp, body)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	return &ollamaStream{
		provider: o,
		ctx:      ctx,
		body:     resp.Body,
		scanner:  scanner,
		started:  time.Now(),
		model:    o.upstreamModel(req.Model),
	}, nil
}

func (o *Ollama) upstreamModel(model string) string {
	if mapped, ok := o.cfg.ModelMap[model]; ok {
		return mapped
	}
	return model
}

func (o *Ollama) requestBody(req ChatRequest, stream bool) ([]byte, error) {
	if req.ToolChoice != nil {
		switch req.ToolChoice.Mode {
		case ToolChoiceTool, ToolChoiceRequired:
			return nil, NewError(KindUnsupported, o.cfg.ID, "ollama does not support required or named tool_choice")
		}
	}
	for key := range req.ProviderOptions {
		switch key {
		case "model", "messages", "stream", "tools", "format", "keep_alive", "options":
			return nil, NewError(KindInvalidRequest, o.cfg.ID, fmt.Sprintf("provider option %q conflicts with the ollama request envelope", key))
		}
	}
	messages, err := convertOllamaMessages(req.Messages)
	if err != nil {
		e := NewError(KindUnsupported, o.cfg.ID, err.Error())
		e.cause = err
		return nil, e
	}
	body := make(map[string]any, len(req.ProviderOptions)+6)
	for k, v := range req.ProviderOptions {
		body[k] = v
	}
	body["model"] = o.upstreamModel(req.Model)
	body["messages"] = messages
	body["stream"] = stream
	if o.cfg.KeepAlive != "" {
		body["keep_alive"] = o.cfg.KeepAlive
	}
	options := map[string]any{}
	if req.Temperature != nil {
		options["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		options["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		options["num_predict"] = *req.MaxTokens
	}
	if len(req.Stop) > 0 {
		options["stop"] = req.Stop
	}
	if req.Seed != nil {
		options["seed"] = *req.Seed
	}
	if len(options) > 0 {
		body["options"] = options
	}
	if len(req.Tools) > 0 && (req.ToolChoice == nil || req.ToolChoice.Mode != ToolChoiceNone) {
		body["tools"] = convertTools(req.Tools)
	}
	if req.ResponseFormat != nil {
		switch req.ResponseFormat.Type {
		case FormatText:
		case FormatJSONObject:
			body["format"] = "json"
		case FormatJSONSchema:
			if req.ResponseFormat.Schema == nil {
				return nil, NewError(KindInvalidRequest, o.cfg.ID, "json_schema response format requires a schema")
			}
			var schema any
			if err := json.Unmarshal(req.ResponseFormat.Schema.Schema, &schema); err != nil {
				return nil, NewError(KindInvalidRequest, o.cfg.ID, "json_schema is not valid JSON")
			}
			body["format"] = schema
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, NewError(KindInvalidRequest, o.cfg.ID, "request could not be encoded")
	}
	return raw, nil
}

type ollamaRequestMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Images    []string         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
}

type ollamaToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	} `json:"function"`
}

func convertOllamaMessages(messages []Message) ([]ollamaRequestMessage, error) {
	out := make([]ollamaRequestMessage, 0, len(messages))
	for _, msg := range messages {
		m := ollamaRequestMessage{Role: string(msg.Role), Content: msg.Text()}
		for _, part := range msg.Content {
			switch part.Type {
			case PartText:
			case PartImage:
				if part.Image == nil {
					continue
				}
				if strings.HasPrefix(part.Image.URL, "data:image/") {
					if _, data, ok := strings.Cut(part.Image.URL, ","); ok && data != "" {
						m.Images = append(m.Images, data)
						continue
					}
				}
				return nil, errors.New("ollama accepts only base64 data URLs for image parts")
			case PartAudio, PartVideo:
				return nil, fmt.Errorf("ollama does not support %s content parts", part.Type)
			}
		}
		for _, tc := range msg.ToolCalls {
			var call ollamaToolCall
			call.Function.Name = tc.Name
			if strings.TrimSpace(tc.Arguments) != "" {
				var args any
				if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
					return nil, fmt.Errorf("tool call %q arguments are not valid JSON", tc.Name)
				}
				raw, _ := json.Marshal(args)
				call.Function.Arguments = raw
			}
			m.ToolCalls = append(m.ToolCalls, call)
		}
		if msg.Role == RoleTool {
			if msg.Name != "" {
				m.ToolName = msg.Name
			} else if msg.ToolCallID != "" {
				m.ToolName = msg.ToolCallID
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func (o *Ollama) do(ctx context.Context, method, path string, body []byte, stream bool) (*http.Response, error) {
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
		return nil, NewError(KindInvalidConfig, o.cfg.ID, "could not construct ollama request")
	}
	req.Header.Set("User-Agent", "LoveCode")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if stream {
		req.Header.Set("Accept", "application/x-ndjson")
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, FromTransport(ctx, o.cfg.ID, err)
	}
	return resp, nil
}

func (o *Ollama) responseError(resp *http.Response, body []byte) *Error {
	message := strings.TrimSpace(string(body))
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error != "" {
		message = env.Error
	}
	if message == "" {
		message = "ollama returned an error"
	}
	e := FromStatus(o.cfg.ID, resp.StatusCode, "", message, retryAfterFromResponse(resp), "")
	e.ProviderRequestID = providerRequestID(resp)
	return e
}

type ollamaTagsResponse struct {
	Models []struct {
		Name       string `json:"name"`
		Model      string `json:"model"`
		ModifiedAt string `json:"modified_at"`
		Size       int64  `json:"size"`
		Digest     string `json:"digest"`
		Details    struct {
			ParentModel       string   `json:"parent_model"`
			Format            string   `json:"format"`
			Family            string   `json:"family"`
			Families          []string `json:"families"`
			ParameterSize     string   `json:"parameter_size"`
			QuantizationLevel string   `json:"quantization_level"`
		} `json:"details"`
	} `json:"models"`
}

type ollamaChatResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		ToolCalls []ollamaToolCall `json:"tool_calls"`
	} `json:"message"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	PromptEvalCount    *int   `json:"prompt_eval_count"`
	EvalCount          *int   `json:"eval_count"`
	TotalDuration      int64  `json:"total_duration"`
	LoadDuration       int64  `json:"load_duration"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalDuration       int64  `json:"eval_duration"`
}

func ollamaUsage(prompt, completion *int) *Usage {
	if prompt == nil && completion == nil {
		return nil
	}
	return &Usage{PromptTokens: prompt, CompletionTokens: completion}
}

func ollamaMetadata(r ollamaChatResponse) map[string]string {
	m := map[string]string{}
	if r.CreatedAt != "" {
		m["created_at"] = r.CreatedAt
	}
	if r.TotalDuration != 0 {
		m["total_duration_ns"] = strconv.FormatInt(r.TotalDuration, 10)
	}
	if r.LoadDuration != 0 {
		m["load_duration_ns"] = strconv.FormatInt(r.LoadDuration, 10)
	}
	if r.PromptEvalDuration != 0 {
		m["prompt_eval_duration_ns"] = strconv.FormatInt(r.PromptEvalDuration, 10)
	}
	if r.EvalDuration != 0 {
		m["eval_duration_ns"] = strconv.FormatInt(r.EvalDuration, 10)
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func mapOllamaFinish(reason string) FinishReason {
	switch reason {
	case "stop":
		return FinishStop
	case "length":
		return FinishLength
	case "tool_calls":
		return FinishToolCalls
	case "":
		return FinishUnknown
	default:
		return FinishUnknown
	}
}

func rawArgumentsString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return SanitizeText(string(raw))
}

// ---- ollama streaming ----

type ollamaStream struct {
	provider  *Ollama
	ctx       context.Context
	body      io.ReadCloser
	scanner   *bufio.Scanner
	started   time.Time
	firstAt   *time.Time
	model     string
	usage     *Usage
	finish    FinishReason
	finalSeen bool
	toolIndex int
	doneSent  bool
	closed    bool
	terminal  error
	pending   []StreamEvent
}

func (s *ollamaStream) Recv(ctx context.Context) (StreamEvent, error) {
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
		if !s.scanner.Scan() {
			if err := s.scanner.Err(); err != nil {
				if s.ctx != nil && s.ctx.Err() != nil {
					s.terminal = ctxError(s.provider.cfg.ID, s.ctx.Err())
				} else {
					s.terminal = FromMalformed(s.provider.cfg.ID, "error reading ollama stream")
				}
				s.closeBody()
				return StreamEvent{}, s.terminal
			}
			return s.doneEvent(), nil
		}
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		var chunk ollamaStreamChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			s.terminal = FromMalformed(s.provider.cfg.ID, "ollama sent invalid JSON in the stream")
			s.closeBody()
			return StreamEvent{}, s.terminal
		}
		if chunk.Error != "" {
			s.terminal = NewError(KindInternal, s.provider.cfg.ID, chunk.Error)
			s.closeBody()
			return StreamEvent{}, s.terminal
		}
		if chunk.PromptEvalCount != nil || chunk.EvalCount != nil {
			s.usage = ollamaUsage(chunk.PromptEvalCount, chunk.EvalCount)
		}
		if chunk.Message.Content != "" {
			s.noteFirst()
			s.pending = append(s.pending, StreamEvent{Type: StreamTextDelta, Text: chunk.Message.Content})
		}
		for _, tc := range chunk.Message.ToolCalls {
			s.noteFirst()
			s.pending = append(s.pending, StreamEvent{
				Type: StreamToolCallDelta,
				ToolCall: &ToolCallDelta{
					Index: s.toolIndex,
					Name:  tc.Function.Name, ArgumentsDelta: rawArgumentsString(tc.Function.Arguments),
				},
			})
			s.toolIndex++
		}
		if chunk.Done {
			s.finish = mapOllamaFinish(chunk.DoneReason)
			s.finalSeen = true
		}
		if len(s.pending) > 0 {
			ev := s.pending[0]
			s.pending = s.pending[1:]
			return ev, nil
		}
		if s.finalSeen {
			return s.doneEvent(), nil
		}
	}
}

func (s *ollamaStream) doneEvent() StreamEvent {
	s.doneSent = true
	completed := time.Now()
	timing := &Timing{StartedAt: s.started, CompletedAt: completed, Duration: completed.Sub(s.started)}
	if s.firstAt != nil {
		timing.FirstTokenAt = s.firstAt
		timing.TimeToFirstToken = s.firstAt.Sub(s.started)
	}
	return StreamEvent{Type: StreamDone, FinishReason: s.finish, Usage: cloneUsage(s.usage), Timing: timing}
}

func (s *ollamaStream) noteFirst() {
	if s.firstAt == nil {
		now := time.Now()
		s.firstAt = &now
	}
}

func (s *ollamaStream) checkContext(ctx context.Context) error {
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

func (s *ollamaStream) closeBody() {
	if !s.closed {
		s.closed = true
		_ = s.body.Close()
	}
}

func (s *ollamaStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.body.Close()
}

type ollamaStreamChunk struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		ToolCalls []ollamaToolCall `json:"tool_calls"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount *int   `json:"prompt_eval_count"`
	EvalCount       *int   `json:"eval_count"`
	Error           string `json:"error"`
}

var _ Provider = (*Ollama)(nil)
