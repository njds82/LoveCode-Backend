package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/models"
)

type fakeProvider struct {
	id, kind string
	initErr  error
	closeErr error

	initCalls  atomic.Int32
	closeCalls atomic.Int32
	healthErr  error
	models     []ModelInfo

	mu         sync.Mutex
	closeOrder *[]string
	chatDelay  time.Duration
}

func (f *fakeProvider) ID() string   { return f.id }
func (f *fakeProvider) Kind() string { return f.kind }
func (f *fakeProvider) Info() ProviderInfo {
	return ProviderInfo{ID: f.id, Kind: f.kind, DisplayName: f.id, Capabilities: ProviderCapabilities{
		Streaming: models.SupportSupported, ModelListing: models.SupportSupported,
	}}
}
func (f *fakeProvider) Capabilities() ProviderCapabilities { return f.Info().Capabilities }
func (f *fakeProvider) Init(context.Context) error {
	f.initCalls.Add(1)
	return f.initErr
}
func (f *fakeProvider) Close(context.Context) error {
	f.closeCalls.Add(1)
	if f.closeOrder != nil {
		f.mu.Lock()
		*f.closeOrder = append(*f.closeOrder, f.id)
		f.mu.Unlock()
	}
	return f.closeErr
}
func (f *fakeProvider) ListModels(context.Context) ([]ModelInfo, error) {
	return append([]ModelInfo(nil), f.models...), nil
}
func (f *fakeProvider) Health(context.Context) error { return f.healthErr }
func (f *fakeProvider) Chat(ctx context.Context, _ ChatRequest) (*ChatResponse, error) {
	if f.chatDelay > 0 {
		select {
		case <-time.After(f.chatDelay):
		case <-ctx.Done():
			return nil, ctxError(f.id, ctx.Err())
		}
	}
	return &ChatResponse{ProviderID: f.id, Message: TextMessage(RoleAssistant, "ok"), FinishReason: FinishStop}, nil
}
func (f *fakeProvider) ChatStream(context.Context, ChatRequest) (Stream, error) {
	return &fakeStream{events: []StreamEvent{
		{Type: StreamTextDelta, Text: "ok"},
		{Type: StreamDone, FinishReason: FinishStop},
	}}, nil
}

type fakeStream struct {
	events []StreamEvent
	index  int
	err    error
	closed atomic.Bool
}

func (s *fakeStream) Recv(context.Context) (StreamEvent, error) {
	if s.index >= len(s.events) {
		return StreamEvent{}, io.EOF
	}
	ev := s.events[s.index]
	s.index++
	if s.err != nil && s.index == len(s.events) {
		return StreamEvent{}, s.err
	}
	return ev, nil
}
func (s *fakeStream) Close() error { s.closed.Store(true); return nil }

func TestRegistryRegistrationAndDuplicates(t *testing.T) {
	r := NewRegistry()
	p := &fakeProvider{id: "p1", kind: "fake"}
	if err := r.Register(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.initCalls.Load() != 1 {
		t.Fatal("Init not called")
	}
	if err := r.Register(context.Background(), &fakeProvider{id: "p1", kind: "fake"}); !errors.Is(err, ErrDuplicateProvider) {
		t.Fatalf("duplicate: %v", err)
	}
	if r.Count() != 1 {
		t.Fatalf("count=%d", r.Count())
	}
	got, err := r.Get("p1")
	if err != nil || got.ID() != "p1" {
		t.Fatalf("get: %v", err)
	}
	if _, err := r.Get("missing"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestRegistryInitFailureNotRegistered(t *testing.T) {
	r := NewRegistry()
	p := &fakeProvider{id: "bad", kind: "fake", initErr: errors.New("boom")}
	if err := r.Register(context.Background(), p); err == nil {
		t.Fatal("expected init error")
	}
	if r.Count() != 0 {
		t.Fatal("failed provider was registered")
	}
	if _, err := r.Get("bad"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if p.closeCalls.Load() != 1 {
		t.Fatalf("failed provider was not closed: %d", p.closeCalls.Load())
	}
}

func TestRegistryEnableDisableAcquire(t *testing.T) {
	r := NewRegistry()
	p := &fakeProvider{id: "p1", kind: "fake"}
	if err := r.Register(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	lease, err := r.Acquire("p1")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnabled("p1", false); !errors.Is(err, ErrProviderBusy) {
		t.Fatalf("disable should be refused while in flight: %v", err)
	}
	lease.Release()
	if err := r.SetEnabled("p1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Acquire("p1"); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("disabled acquire: %v", err)
	}
	if _, err := r.Chat(context.Background(), "p1", ChatRequest{}); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("disabled chat: %v", err)
	}
	management, err := r.AcquireAny("p1")
	if err != nil {
		t.Fatalf("management acquire should work while disabled: %v", err)
	}
	management.Release()
	if err := r.SetEnabled("p1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Acquire("p1"); err != nil {
		t.Fatalf("re-enabled acquire: %v", err)
	}
}

func TestRegistryRemoveRefusesInFlight(t *testing.T) {
	r := NewRegistry()
	p := &fakeProvider{id: "p1", kind: "fake"}
	if err := r.Register(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	stream, err := r.ChatStream(context.Background(), "p1", ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(context.Background(), "p1"); !errors.Is(err, ErrProviderBusy) {
		t.Fatalf("remove while streaming: %v", err)
	}
	// Drain to EOF; the leased stream releases the pin.
	for {
		_, err := stream.Recv(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(context.Background(), "p1"); err != nil {
		t.Fatalf("remove after stream: %v", err)
	}
	if p.closeCalls.Load() != 1 {
		t.Fatal("provider Close not called")
	}
	if r.Count() != 0 {
		t.Fatal("provider not removed")
	}
}

func TestRegistryCloseReverseOrder(t *testing.T) {
	r := NewRegistry()
	var order []string
	a := &fakeProvider{id: "a", kind: "fake", closeOrder: &order}
	b := &fakeProvider{id: "b", kind: "fake", closeOrder: &order}
	_ = r.Register(context.Background(), a)
	_ = r.Register(context.Background(), b)
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "b" || order[1] != "a" {
		t.Fatalf("close order: %v", order)
	}
}

func TestRegistryChatAndListStatus(t *testing.T) {
	r := NewRegistry()
	p := &fakeProvider{id: "p1", kind: "fake"}
	if err := r.Register(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	resp, err := r.Chat(context.Background(), "p1", ChatRequest{Model: "m", Messages: []Message{TextMessage(RoleUser, "hi")}})
	if err != nil || resp.ProviderID != "p1" {
		t.Fatalf("chat: %v %+v", err, resp)
	}
	list := r.List()
	if len(list) != 1 || list[0].ID != "p1" || !list[0].Enabled || list[0].InFlight != 0 {
		t.Fatalf("status: %+v", list)
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("p%d", i)
			if err := r.Register(context.Background(), &fakeProvider{id: id, kind: "fake"}); err != nil {
				t.Error(err)
				return
			}
			lease, err := r.Acquire(id)
			if err != nil {
				t.Error(err)
				return
			}
			_ = r.List()
			_ = r.Count()
			lease.Release()
			if err := r.Remove(context.Background(), id); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if r.Count() != 0 {
		t.Fatalf("leftover providers: %d", r.Count())
	}
}

func TestValidProviderID(t *testing.T) {
	for _, id := range []string{"a", "openai-main", "my.provider_1"} {
		if !ValidProviderID(id) {
			t.Fatalf("%q should be valid", id)
		}
	}
	for _, id := range []string{"", "A", "with space", "-leading", "a/b", "a:b"} {
		if ValidProviderID(id) {
			t.Fatalf("%q should be invalid", id)
		}
	}
}
