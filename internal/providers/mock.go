package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lovecode/lovecode/internal/models"
)

// MockConfig configures the deterministic in-process provider. Every behaviour
// is explicit; there is no network access, randomness or external service.
type MockConfig struct {
	ID          string
	DisplayName string
	Description string
	Models      []ModelInfo

	// Reply is returned verbatim. Empty means a deterministic provider/model
	// description is used.
	Reply        string
	ToolCalls    []ToolCall
	FinishReason FinishReason

	// Usage nil means "provider did not report usage".
	Usage *Usage

	// Latency is applied once before the first response/event. ChunkDelay is
	// applied between streaming chunks.
	Latency    time.Duration
	ChunkDelay time.Duration
	ChunkSize  int

	// Failure is returned immediately by Chat and ChatStream.
	Failure *Error

	// SimulateTimeout waits for the duration and then reports a timeout. The
	// caller's context still wins if it expires first.
	SimulateTimeout time.Duration

	// Unavailable makes Health, ListModels, Chat and ChatStream report a
	// provider-unavailable error.
	Unavailable bool

	// StreamErrorAfter emits this many events, then fails the stream.
	// StreamErrorAfter == 0 disables the mid-stream failure.
	StreamErrorAfter int
	StreamError      *Error
}

// Mock is a fully functional provider used by tests and by routing/agent
// development without credentials or internet access.
type Mock struct {
	cfg MockConfig
}

// NewMock validates cfg and returns a Mock provider.
func NewMock(cfg MockConfig) (*Mock, error) {
	if cfg.ID == "" {
		cfg.ID = "mock"
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = "Mock Provider"
	}
	if cfg.ChunkSize == 0 {
		cfg.ChunkSize = 16
	}
	if cfg.FinishReason == "" {
		cfg.FinishReason = FinishStop
	}
	if cfg.Latency < 0 || cfg.ChunkDelay < 0 || cfg.SimulateTimeout < 0 {
		return nil, NewError(KindInvalidConfig, cfg.ID, "mock latencies must not be negative")
	}
	if cfg.ChunkSize < 1 {
		return nil, NewError(KindInvalidConfig, cfg.ID, "mock chunk_size must be positive")
	}
	if cfg.StreamErrorAfter < 0 {
		return nil, NewError(KindInvalidConfig, cfg.ID, "mock stream_error_after must not be negative")
	}
	if cfg.Failure != nil && !cfg.Failure.Kind.Valid() {
		return nil, NewError(KindInvalidConfig, cfg.ID, "mock failure kind is invalid")
	}
	if cfg.StreamError != nil && !cfg.StreamError.Kind.Valid() {
		return nil, NewError(KindInvalidConfig, cfg.ID, "mock stream error kind is invalid")
	}
	if len(cfg.Models) == 0 {
		cfg.Models = []ModelInfo{{
			ProviderModelID:  "mock-1",
			DisplayName:      "Mock Model 1",
			Description:      "Deterministic in-process model",
			InputModalities:  []models.Modality{models.ModalityText},
			OutputModalities: []models.Modality{models.ModalityText},
			Streaming:        models.SupportSupported,
			ToolCalls:        models.SupportSupported,
			StructuredOutput: models.SupportSupported,
			Reasoning:        models.SupportUnknown,
		}}
	}
	return &Mock{cfg: cfg}, nil
}

func (m *Mock) ID() string   { return m.cfg.ID }
func (m *Mock) Kind() string { return "mock" }

func (m *Mock) Info() ProviderInfo {
	return ProviderInfo{
		ID:          m.cfg.ID,
		Kind:        "mock",
		DisplayName: m.cfg.DisplayName,
		Description: m.cfg.Description,
		Capabilities: ProviderCapabilities{
			Streaming:        models.SupportSupported,
			ToolCalls:        models.SupportSupported,
			StructuredOutput: models.SupportSupported,
			ModelListing:     models.SupportSupported,
			HealthCheck:      models.SupportSupported,
		},
	}
}

func (m *Mock) Capabilities() ProviderCapabilities { return m.Info().Capabilities }
func (m *Mock) Init(context.Context) error         { return nil }
func (m *Mock) Close(context.Context) error        { return nil }

func (m *Mock) unavailableError() error {
	return NewError(KindUnavailable, m.cfg.ID, "mock provider is configured as unavailable")
}

func (m *Mock) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxError(m.cfg.ID, err)
	}
	if m.cfg.Unavailable {
		return nil, m.unavailableError()
	}
	out := make([]ModelInfo, len(m.cfg.Models))
	copy(out, m.cfg.Models)
	return out, nil
}

func (m *Mock) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return ctxError(m.cfg.ID, err)
	}
	if m.cfg.Unavailable {
		return m.unavailableError()
	}
	return nil
}

func (m *Mock) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, wrapInvalidRequest(m.cfg.ID, err)
	}
	if m.cfg.Unavailable {
		return nil, m.unavailableError()
	}
	if m.cfg.Failure != nil {
		return nil, cloneProviderError(m.cfg.Failure)
	}
	started := time.Now()
	if m.cfg.SimulateTimeout > 0 {
		if err := waitContext(ctx, m.cfg.SimulateTimeout); err != nil {
			return nil, ctxError(m.cfg.ID, err)
		}
		return nil, NewError(KindTimeout, m.cfg.ID, "simulated provider timeout")
	}
	if m.cfg.Latency > 0 {
		if err := waitContext(ctx, m.cfg.Latency); err != nil {
			return nil, ctxError(m.cfg.ID, err)
		}
	}
	completed := time.Now()
	resp := &ChatResponse{
		ProviderID:        m.cfg.ID,
		Model:             req.Model,
		Message:           Message{Role: RoleAssistant, Content: []ContentPart{TextPart(m.reply(req))}, ToolCalls: cloneToolCalls(m.cfg.ToolCalls)},
		FinishReason:      m.cfg.FinishReason,
		Usage:             cloneUsage(m.cfg.Usage),
		ProviderRequestID: m.requestID(req),
		Metadata:          map[string]string{"mock": "true"},
		Timing: Timing{
			StartedAt:   started,
			CompletedAt: completed,
			Duration:    completed.Sub(started),
		},
	}
	return resp, nil
}

func (m *Mock) ChatStream(ctx context.Context, req ChatRequest) (Stream, error) {
	if err := req.Validate(); err != nil {
		return nil, wrapInvalidRequest(m.cfg.ID, err)
	}
	if m.cfg.Unavailable {
		return nil, m.unavailableError()
	}
	if m.cfg.Failure != nil {
		return nil, cloneProviderError(m.cfg.Failure)
	}
	if m.cfg.SimulateTimeout > 0 {
		if err := waitContext(ctx, m.cfg.SimulateTimeout); err != nil {
			return nil, ctxError(m.cfg.ID, err)
		}
		return nil, NewError(KindTimeout, m.cfg.ID, "simulated provider timeout")
	}
	return &mockStream{
		provider: m,
		ctx:      ctx,
		chunks:   splitRunes(m.reply(req), m.cfg.ChunkSize),
		started:  time.Now(),
	}, nil
}

func (m *Mock) reply(req ChatRequest) string {
	if m.cfg.Reply != "" {
		return m.cfg.Reply
	}
	return fmt.Sprintf("mock response from %s for model %s", m.cfg.ID, req.Model)
}

func (m *Mock) requestID(req ChatRequest) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", m.cfg.ID, req.Model)
	for _, msg := range req.Messages {
		fmt.Fprintf(h, "%s\x00%s\x00", msg.Role, msg.Text())
	}
	return "mock-" + hex.EncodeToString(h.Sum(nil)[:8])
}

type mockStream struct {
	provider    *Mock
	ctx         context.Context
	chunks      []string
	index       int
	events      int
	started     time.Time
	firstAt     *time.Time
	emittedDone bool
	closed      bool
}

func (s *mockStream) Recv(ctx context.Context) (StreamEvent, error) {
	if s.closed {
		return StreamEvent{}, io.EOF
	}
	if err := s.checkContext(ctx); err != nil {
		s.closed = true
		return StreamEvent{}, err
	}
	if s.emittedDone {
		return StreamEvent{}, io.EOF
	}
	if s.provider.cfg.StreamErrorAfter > 0 && s.events >= s.provider.cfg.StreamErrorAfter {
		s.closed = true
		if s.provider.cfg.StreamError != nil {
			return StreamEvent{}, cloneProviderError(s.provider.cfg.StreamError)
		}
		return StreamEvent{}, NewError(KindInternal, s.provider.cfg.ID, "simulated mid-stream failure")
	}
	// First event includes configured latency. Subsequent text chunks use
	// chunk delay to simulate token pacing.
	if s.events == 0 && s.provider.cfg.Latency > 0 {
		if err := s.wait(ctx, s.provider.cfg.Latency); err != nil {
			s.closed = true
			return StreamEvent{}, err
		}
	} else if s.events > 0 && s.provider.cfg.ChunkDelay > 0 {
		if err := s.wait(ctx, s.provider.cfg.ChunkDelay); err != nil {
			s.closed = true
			return StreamEvent{}, err
		}
	}

	if len(s.provider.cfg.ToolCalls) > 0 && s.index < len(s.provider.cfg.ToolCalls) {
		tc := s.provider.cfg.ToolCalls[s.index]
		s.index++
		s.noteFirst()
		s.events++
		return StreamEvent{
			Type: StreamToolCallDelta,
			ToolCall: &ToolCallDelta{
				Index: s.index - 1, ID: tc.ID, Name: tc.Name, ArgumentsDelta: tc.Arguments,
			},
		}, nil
	}

	textIndex := s.index - len(s.provider.cfg.ToolCalls)
	if textIndex < len(s.chunks) {
		text := s.chunks[textIndex]
		s.index++
		s.noteFirst()
		s.events++
		return StreamEvent{Type: StreamTextDelta, Text: text}, nil
	}

	s.emittedDone = true
	s.events++
	completed := time.Now()
	timing := &Timing{StartedAt: s.started, CompletedAt: completed, Duration: completed.Sub(s.started)}
	if s.firstAt != nil {
		timing.FirstTokenAt = s.firstAt
		timing.TimeToFirstToken = s.firstAt.Sub(s.started)
	}
	return StreamEvent{
		Type:         StreamDone,
		FinishReason: s.provider.cfg.FinishReason,
		Usage:        cloneUsage(s.provider.cfg.Usage),
		Timing:       timing,
	}, nil
}

func (s *mockStream) noteFirst() {
	if s.firstAt == nil {
		now := time.Now()
		s.firstAt = &now
	}
}

func (s *mockStream) checkContext(ctx context.Context) error {
	if err := s.ctx.Err(); err != nil {
		return ctxError(s.provider.cfg.ID, err)
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return ctxError(s.provider.cfg.ID, err)
		}
	}
	return nil
}

func (s *mockStream) wait(ctx context.Context, d time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.ctx.Done():
		return ctxError(s.provider.cfg.ID, s.ctx.Err())
	case <-ctx.Done():
		return ctxError(s.provider.cfg.ID, ctx.Err())
	case <-t.C:
		return nil
	}
}

func (s *mockStream) Close() error {
	s.closed = true
	return nil
}

func splitRunes(s string, size int) []string {
	if s == "" {
		return nil
	}
	r := []rune(s)
	out := make([]string, 0, (len(r)+size-1)/size)
	for len(r) > 0 {
		n := size
		if n > len(r) {
			n = len(r)
		}
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return out
}

func waitContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func ctxError(providerID string, err error) *Error {
	switch {
	case errors.Is(err, context.Canceled):
		e := NewError(KindCanceled, providerID, "request canceled")
		e.cause = context.Canceled
		return e
	case errors.Is(err, context.DeadlineExceeded):
		e := NewError(KindTimeout, providerID, "request timed out")
		e.cause = context.DeadlineExceeded
		return e
	default:
		return NewError(KindCanceled, providerID, "request canceled")
	}
}

func wrapInvalidRequest(providerID string, err error) *Error {
	e := NewError(KindInvalidRequest, providerID, err.Error())
	e.cause = err
	return e
}

func cloneProviderError(e *Error) *Error {
	if e == nil {
		return nil
	}
	cp := *e
	return &cp
}

func cloneUsage(u *Usage) *Usage {
	if u == nil {
		return nil
	}
	cp := *u
	return &cp
}

func cloneToolCalls(tcs []ToolCall) []ToolCall {
	if len(tcs) == 0 {
		return nil
	}
	return append([]ToolCall(nil), tcs...)
}

var _ Provider = (*Mock)(nil)
