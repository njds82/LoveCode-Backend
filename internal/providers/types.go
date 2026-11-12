// Package providers defines LoveCode's provider abstraction, provider registry,
// normalized chat types, errors, streaming contract and built-in adapters.
//
// Adapters never expose provider SDK types. All requests and responses use the
// types in this package so routing and agent layers remain provider-agnostic.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lovecode/lovecode/internal/models"
)

// Role is a normalized chat role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

func (r Role) Valid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// PartType identifies a content part.
type PartType string

const (
	PartText  PartType = "text"
	PartImage PartType = "image"
	PartAudio PartType = "audio"
	PartVideo PartType = "video"
)

func (p PartType) Valid() bool {
	switch p {
	case PartText, PartImage, PartAudio, PartVideo:
		return true
	default:
		return false
	}
}

// ImageSource is a remote image reference.
type ImageSource struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// AudioSource is inline base64 audio.
type AudioSource struct {
	Data   string `json:"data"`
	Format string `json:"format,omitempty"`
}

// VideoSource is a remote video reference.
type VideoSource struct {
	URL string `json:"url"`
}

// ContentPart is one part of a message. Only the field matching Type is used.
type ContentPart struct {
	Type  PartType     `json:"type"`
	Text  string       `json:"text,omitempty"`
	Image *ImageSource `json:"image,omitempty"`
	Audio *AudioSource `json:"audio,omitempty"`
	Video *VideoSource `json:"video,omitempty"`
}

// TextPart is a convenience constructor.
func TextPart(text string) ContentPart { return ContentPart{Type: PartText, Text: text} }

// ToolCall is an assistant tool call. Arguments is the raw JSON text emitted by
// the provider; it is intentionally not decoded or normalized here because a
// model can emit invalid JSON, and hiding that would misrepresent the response.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a function/tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolChoiceMode is a normalized tool selection mode.
type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
	ToolChoiceTool     ToolChoiceMode = "tool"
)

// ToolChoice selects tool usage.
type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode"`
	Name string         `json:"name,omitempty"`
}

// ResponseFormatType is a normalized structured-output mode.
type ResponseFormatType string

const (
	FormatText       ResponseFormatType = "text"
	FormatJSONObject ResponseFormatType = "json_object"
	FormatJSONSchema ResponseFormatType = "json_schema"
)

// JSONSchema describes a structured response schema.
type JSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
	Strict      *bool           `json:"strict,omitempty"`
}

// ResponseFormat requests text, JSON object or JSON-schema output.
type ResponseFormat struct {
	Type   ResponseFormatType `json:"type"`
	Schema *JSONSchema        `json:"json_schema,omitempty"`
}

// Message is one normalized conversation message.
type Message struct {
	Role       Role          `json:"role"`
	Content    []ContentPart `json:"content,omitempty"`
	Name       string        `json:"name,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// TextMessage builds a single-text-part message.
func TextMessage(role Role, text string) Message {
	return Message{Role: role, Content: []ContentPart{TextPart(text)}}
}

// Text concatenates all text parts. Non-text parts are ignored.
func (m Message) Text() string {
	var b strings.Builder
	for _, p := range m.Content {
		if p.Type == PartText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// ChatRequest is the normalized generation request.
//
// Model is the provider-specific model identifier, unless the adapter was
// configured with a model map, in which case a mapped LoveCode model ID is also
// accepted and translated. Higher layers select a model record and pass its
// ProviderModelID.
type ChatRequest struct {
	Model           string          `json:"model"`
	Messages        []Message       `json:"messages"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	MaxTokens       *int            `json:"max_tokens,omitempty"`
	Stop            []string        `json:"stop,omitempty"`
	Seed            *int64          `json:"seed,omitempty"`
	Tools           []Tool          `json:"tools,omitempty"`
	ToolChoice      *ToolChoice     `json:"tool_choice,omitempty"`
	ResponseFormat  *ResponseFormat `json:"response_format,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	ProviderOptions map[string]any  `json:"provider_options,omitempty"`
}

// Usage reports provider-supplied token counts. A nil Usage means the provider
// did not report usage. Within a non-nil Usage, a nil field means that specific
// count was absent; a zero pointer is a real zero count.
type Usage struct {
	PromptTokens       *int `json:"prompt_tokens,omitempty"`
	CompletionTokens   *int `json:"completion_tokens,omitempty"`
	TotalTokens        *int `json:"total_tokens,omitempty"`
	CachedPromptTokens *int `json:"cached_prompt_tokens,omitempty"`
	ReasoningTokens    *int `json:"reasoning_tokens,omitempty"`
}

// FinishReason is a normalized completion stop reason.
type FinishReason string

const (
	FinishUnknown       FinishReason = "unknown"
	FinishStop          FinishReason = "stop"
	FinishLength        FinishReason = "length"
	FinishToolCalls     FinishReason = "tool_calls"
	FinishContentFilter FinishReason = "content_filter"
	FinishError         FinishReason = "error"
)

// Timing records request timing. It is metadata, never a fabricated token count.
type Timing struct {
	StartedAt        time.Time     `json:"started_at"`
	FirstTokenAt     *time.Time    `json:"first_token_at,omitempty"`
	CompletedAt      time.Time     `json:"completed_at"`
	Duration         time.Duration `json:"-"`
	TimeToFirstToken time.Duration `json:"-"`
}

// ChatResponse is the normalized non-streaming response.
type ChatResponse struct {
	ProviderID        string            `json:"provider_id"`
	Model             string            `json:"model"`
	Message           Message           `json:"message"`
	FinishReason      FinishReason      `json:"finish_reason"`
	Usage             *Usage            `json:"usage,omitempty"`
	ProviderRequestID string            `json:"provider_request_id,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Timing            Timing            `json:"timing"`
}

// StreamEventType identifies a stream event.
type StreamEventType string

const (
	StreamTextDelta     StreamEventType = "text_delta"
	StreamToolCallDelta StreamEventType = "tool_call_delta"
	StreamDone          StreamEventType = "done"
)

// ToolCallDelta is an incremental tool-call fragment. Providers usually send
// the ID and name once, then argument fragments. Index identifies the call.
type ToolCallDelta struct {
	Index          int    `json:"index"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

// StreamEvent is one unified stream event.
//
// Contract: text and tool-call deltas may arrive in any order; exactly one
// StreamDone is delivered before Recv returns io.EOF. A provider failure is
// returned from Recv as a *Error, not as an event. Done carries provider usage
// and timing when available.
type StreamEvent struct {
	Type         StreamEventType `json:"type"`
	Text         string          `json:"text,omitempty"`
	ToolCall     *ToolCallDelta  `json:"tool_call,omitempty"`
	FinishReason FinishReason    `json:"finish_reason,omitempty"`
	Usage        *Usage          `json:"usage,omitempty"`
	Timing       *Timing         `json:"timing,omitempty"`
}

// Stream is a provider-independent streaming iterator. Consumers must call
// Close when finished, even after io.EOF, to release HTTP resources. The context
// passed to ChatStream is authoritative for cancellation; Recv also checks its
// own context between reads.
type Stream interface {
	Recv(ctx context.Context) (StreamEvent, error) // io.EOF after StreamDone
	Close() error
}

// ProviderCapabilities describes what an adapter can handle. It is not a claim
// that every configured model or every compatible endpoint supports it.
type ProviderCapabilities struct {
	Streaming        models.Support `json:"streaming"`
	ToolCalls        models.Support `json:"tool_calls"`
	StructuredOutput models.Support `json:"structured_output"`
	ModelListing     models.Support `json:"model_listing"`
	HealthCheck      models.Support `json:"health_check"`
}

// ProviderInfo is static provider metadata supplied by an adapter.
type ProviderInfo struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind"`
	DisplayName  string               `json:"display_name"`
	Description  string               `json:"description,omitempty"`
	Capabilities ProviderCapabilities `json:"capabilities"`
}

// Provider is the stable provider interface. Implementations must be safe for
// concurrent use: the registry may dispatch multiple requests at once.
//
// Lifecycle: the registry calls Init once when the provider is registered, and
// Close once when it is removed or the registry shuts down. Init must not start
// background polling.
type Provider interface {
	ID() string
	Kind() string
	Info() ProviderInfo
	Capabilities() ProviderCapabilities
	ListModels(ctx context.Context) ([]ModelInfo, error)
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req ChatRequest) (Stream, error)
	Health(ctx context.Context) error
	Init(ctx context.Context) error
	Close(ctx context.Context) error
}

// ModelInfo is model metadata reported by a provider adapter before it is
// assigned a LoveCode identity and placed in the model registry.
type ModelInfo struct {
	ProviderModelID  string             `json:"provider_model_id"`
	DisplayName      string             `json:"display_name"`
	Description      string             `json:"description,omitempty"`
	ContextWindow    *int64             `json:"context_window,omitempty"`
	MaxOutputTokens  *int64             `json:"max_output_tokens,omitempty"`
	InputModalities  []models.Modality  `json:"input_modalities"`
	OutputModalities []models.Modality  `json:"output_modalities"`
	Streaming        models.Support     `json:"streaming"`
	ToolCalls        models.Support     `json:"tool_calls"`
	StructuredOutput models.Support     `json:"structured_output"`
	Reasoning        models.Support     `json:"reasoning"`
	Pricing          *models.Pricing    `json:"pricing,omitempty"`
	RateLimits       *models.RateLimits `json:"rate_limits,omitempty"`
	Metadata         map[string]string  `json:"metadata,omitempty"`
}

const (
	maxMessages       = 2048
	maxTextPartBytes  = 1 << 20
	maxTotalTextBytes = 4 << 20
	maxStopSequences  = 16
	maxStopBytes      = 256
	maxTools          = 128
	maxSchemaBytes    = 1 << 20
	maxProviderOpts   = 64
	maxProviderOptVal = 64 << 10
)

var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// reservedProviderOptions may not be supplied through ProviderOptions. The
// adapter must not let provider-specific passthrough silently overwrite a
// normalized field.
var reservedProviderOptions = map[string]bool{
	"model": true, "messages": true, "stream": true, "temperature": true,
	"top_p": true, "max_tokens": true, "max_completion_tokens": true,
	"stop": true, "seed": true, "tools": true, "tool_choice": true,
	"response_format": true, "stream_options": true, "n": true,
}

// Validate checks normalized request invariants. Adapters call it before any
// network I/O. ProviderOptions are validated for shape and reserved keys, but
// their provider-specific meaning is validated by the adapter that understands
// them.
func (r ChatRequest) Validate() error {
	var errs []error
	if strings.TrimSpace(r.Model) == "" {
		errs = append(errs, errors.New("model must not be empty"))
	}
	if len(r.Model) > 512 {
		errs = append(errs, errors.New("model must be at most 512 bytes"))
	}
	if len(r.Messages) == 0 {
		errs = append(errs, errors.New("at least one message is required"))
	}
	if len(r.Messages) > maxMessages {
		errs = append(errs, fmt.Errorf("too many messages: %d (max %d)", len(r.Messages), maxMessages))
	}
	totalText := 0
	for i, m := range r.Messages {
		if !m.Role.Valid() {
			errs = append(errs, fmt.Errorf("messages[%d]: invalid role %q", i, m.Role))
		}
		if m.Role == RoleTool {
			if strings.TrimSpace(m.ToolCallID) == "" {
				errs = append(errs, fmt.Errorf("messages[%d]: tool message requires tool_call_id", i))
			}
			if len(m.ToolCalls) > 0 {
				errs = append(errs, fmt.Errorf("messages[%d]: tool message must not contain tool_calls", i))
			}
		}
		if len(m.ToolCalls) > 0 && m.Role != RoleAssistant {
			errs = append(errs, fmt.Errorf("messages[%d]: only assistant messages may contain tool_calls", i))
		}
		for j, part := range m.Content {
			if !part.Type.Valid() {
				errs = append(errs, fmt.Errorf("messages[%d].content[%d]: invalid type %q", i, j, part.Type))
				continue
			}
			switch part.Type {
			case PartText:
				if len(part.Text) > maxTextPartBytes {
					errs = append(errs, fmt.Errorf("messages[%d].content[%d]: text part too large", i, j))
				}
				totalText += len(part.Text)
			case PartImage:
				if part.Image == nil || strings.TrimSpace(part.Image.URL) == "" {
					errs = append(errs, fmt.Errorf("messages[%d].content[%d]: image URL required", i, j))
				} else if !validMediaURL(part.Image.URL) {
					errs = append(errs, fmt.Errorf("messages[%d].content[%d]: image URL must be http(s) or data", i, j))
				}
			case PartAudio:
				if part.Audio == nil || strings.TrimSpace(part.Audio.Data) == "" {
					errs = append(errs, fmt.Errorf("messages[%d].content[%d]: audio data required", i, j))
				}
			case PartVideo:
				if part.Video == nil || !validMediaURL(part.Video.URL) {
					errs = append(errs, fmt.Errorf("messages[%d].content[%d]: video URL required", i, j))
				}
			}
		}
		for j, tc := range m.ToolCalls {
			if strings.TrimSpace(tc.Name) == "" {
				errs = append(errs, fmt.Errorf("messages[%d].tool_calls[%d]: name required", i, j))
			}
			if len(tc.Arguments) > maxTextPartBytes {
				errs = append(errs, fmt.Errorf("messages[%d].tool_calls[%d]: arguments too large", i, j))
			} else if strings.TrimSpace(tc.Arguments) != "" && !json.Valid([]byte(tc.Arguments)) {
				errs = append(errs, fmt.Errorf("messages[%d].tool_calls[%d]: arguments must be valid JSON", i, j))
			}
		}
	}
	if totalText > maxTotalTextBytes {
		errs = append(errs, errors.New("total message text exceeds limit"))
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 2) {
		errs = append(errs, errors.New("temperature must be between 0 and 2"))
	}
	if r.TopP != nil && (*r.TopP < 0 || *r.TopP > 1) {
		errs = append(errs, errors.New("top_p must be between 0 and 1"))
	}
	if r.MaxTokens != nil && (*r.MaxTokens <= 0 || *r.MaxTokens > 1<<20) {
		errs = append(errs, errors.New("max_tokens must be between 1 and 1048576"))
	}
	if len(r.Stop) > maxStopSequences {
		errs = append(errs, errors.New("too many stop sequences"))
	}
	for _, s := range r.Stop {
		if s == "" || len(s) > maxStopBytes {
			errs = append(errs, errors.New("stop sequences must be non-empty and at most 256 bytes"))
		}
	}
	if len(r.Tools) > maxTools {
		errs = append(errs, fmt.Errorf("too many tools: %d (max %d)", len(r.Tools), maxTools))
	}
	for i, t := range r.Tools {
		if !toolNameRe.MatchString(t.Name) {
			errs = append(errs, fmt.Errorf("tools[%d]: invalid name %q", i, t.Name))
		}
		if len(t.Parameters) == 0 || len(t.Parameters) > maxSchemaBytes || !json.Valid(t.Parameters) {
			errs = append(errs, fmt.Errorf("tools[%d]: parameters must be valid JSON", i))
		}
	}
	if r.ToolChoice != nil {
		switch r.ToolChoice.Mode {
		case ToolChoiceAuto, ToolChoiceNone, ToolChoiceRequired:
		case ToolChoiceTool:
			if !toolNameRe.MatchString(r.ToolChoice.Name) {
				errs = append(errs, errors.New("tool_choice tool name is invalid"))
			}
		default:
			errs = append(errs, fmt.Errorf("tool_choice mode invalid: %q", r.ToolChoice.Mode))
		}
	}
	if r.ResponseFormat != nil {
		switch r.ResponseFormat.Type {
		case FormatText, FormatJSONObject:
		case FormatJSONSchema:
			if r.ResponseFormat.Schema == nil {
				errs = append(errs, errors.New("json_schema response format requires a schema"))
			} else {
				if strings.TrimSpace(r.ResponseFormat.Schema.Name) == "" {
					errs = append(errs, errors.New("json_schema name is required"))
				}
				if len(r.ResponseFormat.Schema.Schema) == 0 || len(r.ResponseFormat.Schema.Schema) > maxSchemaBytes || !json.Valid(r.ResponseFormat.Schema.Schema) {
					errs = append(errs, errors.New("json_schema schema must be valid JSON"))
				}
			}
		default:
			errs = append(errs, fmt.Errorf("response_format type invalid: %q", r.ResponseFormat.Type))
		}
	}
	if len(r.ProviderOptions) > maxProviderOpts {
		errs = append(errs, errors.New("too many provider options"))
	}
	for k, v := range r.ProviderOptions {
		if strings.TrimSpace(k) == "" {
			errs = append(errs, errors.New("provider option keys must not be empty"))
			continue
		}
		if reservedProviderOptions[k] {
			errs = append(errs, fmt.Errorf("provider option %q is reserved by the normalized request", k))
		}
		raw, err := json.Marshal(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("provider option %q is not JSON-serializable: %v", k, err))
			continue
		}
		if len(raw) > maxProviderOptVal {
			errs = append(errs, fmt.Errorf("provider option %q is too large", k))
		}
	}
	return errors.Join(errs...)
}

func validMediaURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "data":
		return strings.HasPrefix(s, "data:image/") || strings.HasPrefix(s, "data:video/")
	default:
		return false
	}
}
