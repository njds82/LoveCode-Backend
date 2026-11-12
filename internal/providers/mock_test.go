package providers

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func mockProvider(t *testing.T, cfg MockConfig) *Mock {
	t.Helper()
	if cfg.ID == "" {
		cfg.ID = "mock"
	}
	p, err := NewMock(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMockDeterministic(t *testing.T) {
	p := mockProvider(t, MockConfig{
		Reply: "fixed reply",
		Usage: &Usage{PromptTokens: intPtr(0), CompletionTokens: intPtr(3), TotalTokens: intPtr(3)},
	})
	req := validRequest()
	a, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if a.Message.Text() != "fixed reply" || b.Message.Text() != "fixed reply" {
		t.Fatalf("reply: %q %q", a.Message.Text(), b.Message.Text())
	}
	if a.ProviderRequestID != b.ProviderRequestID || a.ProviderRequestID == "" {
		t.Fatalf("request id not deterministic: %q %q", a.ProviderRequestID, b.ProviderRequestID)
	}
	if a.Usage == nil || a.Usage.PromptTokens == nil || *a.Usage.PromptTokens != 0 {
		t.Fatalf("zero token usage must be preserved as a zero pointer: %+v", a.Usage)
	}
	if a.Usage.CompletionTokens == nil || *a.Usage.CompletionTokens != 3 {
		t.Fatalf("completion tokens: %+v", a.Usage)
	}
	if a.FinishReason != FinishStop || a.ProviderID != "mock" {
		t.Fatalf("metadata: %+v", a)
	}
	if a.Timing.Duration < 0 || a.Timing.StartedAt.IsZero() || a.Timing.CompletedAt.Before(a.Timing.StartedAt) {
		t.Fatalf("timing: %+v", a.Timing)
	}
}

func TestMockNoUsageIsNil(t *testing.T) {
	p := mockProvider(t, MockConfig{})
	resp, err := p.Chat(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage != nil {
		t.Fatalf("missing usage must remain nil: %+v", resp.Usage)
	}
}

func TestMockLatency(t *testing.T) {
	p := mockProvider(t, MockConfig{Latency: 30 * time.Millisecond})
	start := time.Now()
	if _, err := p.Chat(context.Background(), validRequest()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Fatalf("latency not applied: %v", elapsed)
	}
}

func TestMockFailureAndUnavailable(t *testing.T) {
	p := mockProvider(t, MockConfig{Failure: &Error{Kind: KindRateLimited, Message: "slow down", RetryAfter: 2 * time.Second}})
	_, err := p.Chat(context.Background(), validRequest())
	if !IsKind(err, KindRateLimited) {
		t.Fatalf("failure: %v", err)
	}
	var pe *Error
	if !errors.As(err, &pe) || pe.RetryAfter != 2*time.Second {
		t.Fatalf("retry after lost: %+v", pe)
	}
	u := mockProvider(t, MockConfig{Unavailable: true})
	if err := u.Health(context.Background()); !IsKind(err, KindUnavailable) {
		t.Fatalf("health: %v", err)
	}
	if _, err := u.ListModels(context.Background()); !IsKind(err, KindUnavailable) {
		t.Fatalf("list: %v", err)
	}
	if _, err := u.Chat(context.Background(), validRequest()); !IsKind(err, KindUnavailable) {
		t.Fatalf("chat: %v", err)
	}
}

func TestMockSimulatedTimeout(t *testing.T) {
	p := mockProvider(t, MockConfig{SimulateTimeout: 20 * time.Millisecond})
	start := time.Now()
	_, err := p.Chat(context.Background(), validRequest())
	if !IsKind(err, KindTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	if time.Since(start) < 15*time.Millisecond {
		t.Fatal("timeout returned too early")
	}
}

func TestMockContextCancellation(t *testing.T) {
	p := mockProvider(t, MockConfig{Latency: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Chat(ctx, validRequest()); !IsKind(err, KindCanceled) {
		t.Fatalf("chat cancellation: %v", err)
	}
}

func TestMockStreaming(t *testing.T) {
	p := mockProvider(t, MockConfig{
		Reply:      "hello streaming world",
		ChunkSize:  4,
		ChunkDelay: time.Millisecond,
		Usage:      &Usage{PromptTokens: intPtr(1), CompletionTokens: intPtr(4), TotalTokens: intPtr(5)},
	})
	stream, err := p.ChatStream(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var b strings.Builder
	var done *StreamEvent
	for {
		ev, err := stream.Recv(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case StreamTextDelta:
			b.WriteString(ev.Text)
		case StreamDone:
			e := ev
			done = &e
		}
	}
	if b.String() != "hello streaming world" {
		t.Fatalf("text: %q", b.String())
	}
	if done == nil || done.FinishReason != FinishStop || done.Usage == nil || *done.Usage.TotalTokens != 5 {
		t.Fatalf("done event: %+v", done)
	}
	if done.Timing == nil || done.Timing.FirstTokenAt == nil || done.Timing.TimeToFirstToken <= 0 {
		t.Fatalf("stream timing: %+v", done.Timing)
	}
}

func TestMockStreamCancellation(t *testing.T) {
	p := mockProvider(t, MockConfig{Reply: "abcdefghij", ChunkSize: 1, ChunkDelay: 500 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := p.ChatStream(ctx, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Recv(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := stream.Recv(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		if !IsKind(err, KindCanceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not cancel promptly")
	}
}

func TestMockStreamError(t *testing.T) {
	p := mockProvider(t, MockConfig{
		Reply: "abcdef", ChunkSize: 1, StreamErrorAfter: 1,
		StreamError: &Error{Kind: KindUnavailable, Message: "gone"},
	})
	stream, err := p.ChatStream(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Recv(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(context.Background()); !IsKind(err, KindUnavailable) {
		t.Fatalf("mid-stream error: %v", err)
	}
}

func TestMockModels(t *testing.T) {
	p := mockProvider(t, MockConfig{})
	models, err := p.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ProviderModelID != "mock-1" {
		t.Fatalf("models: %v %v", models, err)
	}
	if models[0].Streaming != "supported" {
		t.Fatalf("declared streaming: %+v", models[0])
	}
}
