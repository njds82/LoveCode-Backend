package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"time"
)

var (
	ErrDuplicateProvider = errors.New("provider already registered")
	ErrProviderNotFound  = errors.New("provider not found")
	ErrProviderDisabled  = errors.New("provider disabled")
	ErrProviderBusy      = errors.New("provider has requests in flight")
	ErrInvalidProvider   = errors.New("invalid provider")
)

var providerIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidProviderID reports whether an ID is stable and URL-safe.
func ValidProviderID(id string) bool { return providerIDRe.MatchString(id) }

// Status is a registry snapshot for one provider. Manager augments this with
// configuration-derived endpoint metadata and initialization errors.
type Status struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind"`
	DisplayName  string               `json:"display_name"`
	Description  string               `json:"description,omitempty"`
	Capabilities ProviderCapabilities `json:"capabilities"`
	Enabled      bool                 `json:"enabled"`
	InFlight     int                  `json:"in_flight"`
	RegisteredAt time.Time            `json:"registered_at"`
}

type entry struct {
	provider     Provider
	info         ProviderInfo
	enabled      bool
	inflight     int
	registeredAt time.Time
}

// Registry is the concurrency-safe provider registry. Registration is
// independent of model registration.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*entry
	order   []string
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*entry)}
}

// Register initializes and stores a provider. IDs must be unique and stable.
func (r *Registry) Register(ctx context.Context, p Provider) error {
	if p == nil {
		return fmt.Errorf("%w: nil", ErrInvalidProvider)
	}
	id := p.ID()
	if !ValidProviderID(id) {
		return fmt.Errorf("%w: id %q must match %s", ErrInvalidProvider, id, providerIDRe)
	}
	info := p.Info()
	if info.ID != "" && info.ID != id {
		return fmt.Errorf("%w: Info().ID %q does not match ID() %q", ErrInvalidProvider, info.ID, id)
	}
	if info.Kind != "" && info.Kind != p.Kind() {
		return fmt.Errorf("%w: Info().Kind %q does not match Kind() %q", ErrInvalidProvider, info.Kind, p.Kind())
	}

	r.mu.RLock()
	_, exists := r.entries[id]
	r.mu.RUnlock()
	if exists {
		return fmt.Errorf("%w: %s", ErrDuplicateProvider, id)
	}

	if err := p.Init(ctx); err != nil {
		// Init may have acquired resources before failing. Release them; the
		// provider is not registered and the caller no longer owns it.
		_ = p.Close(context.Background())
		return fmt.Errorf("initialize provider %q: %w", id, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[id]; exists {
		// Lost a registration race after Init. Release the losing instance.
		_ = p.Close(context.Background())
		return fmt.Errorf("%w: %s", ErrDuplicateProvider, id)
	}
	info.ID = id
	if info.Kind == "" {
		info.Kind = p.Kind()
	}
	r.entries[id] = &entry{provider: p, info: info, enabled: true, registeredAt: time.Now().UTC()}
	r.order = append(r.order, id)
	return nil
}

// Remove unregisters and closes a provider. It refuses while requests are in
// flight so a generation cannot observe a closed adapter.
func (r *Registry) Remove(ctx context.Context, id string) error {
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}
	if e.inflight > 0 {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s (%d in flight)", ErrProviderBusy, id, e.inflight)
	}
	delete(r.entries, id)
	for i, v := range r.order {
		if v == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
	return e.provider.Close(ctx)
}

// Get returns a provider even when it is disabled. Use Acquire or the Chat
// wrappers for generation; they enforce the enabled flag and track in-flight
// requests.
func (r *Registry) Get(id string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}
	return e.provider, nil
}

// SetEnabled enables or disables a provider. Disabling is refused while
// requests are in flight; enabling is always safe.
func (r *Registry) SetEnabled(id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}
	if !enabled && e.inflight > 0 {
		return fmt.Errorf("%w: %s (%d in flight)", ErrProviderBusy, id, e.inflight)
	}
	e.enabled = enabled
	return nil
}

func (r *Registry) isEnabled(id string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}
	return e.enabled, nil
}

// List returns provider status snapshots in registration order.
func (r *Registry) List() []Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Status, 0, len(r.order))
	for _, id := range r.order {
		e := r.entries[id]
		out = append(out, Status{
			ID:           id,
			Kind:         e.info.Kind,
			DisplayName:  e.info.DisplayName,
			Description:  e.info.Description,
			Capabilities: e.info.Capabilities,
			Enabled:      e.enabled,
			InFlight:     e.inflight,
			RegisteredAt: e.registeredAt,
		})
	}
	return out
}

// Count returns the number of registered providers.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// Lease pins a provider so it cannot be removed or disabled while a request is
// in flight. Release must be called exactly once; it is idempotent.
type Lease struct {
	provider Provider
	release  func()
	once     sync.Once
}

func (l *Lease) Provider() Provider { return l.provider }

func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(l.release)
}

func (r *Registry) acquire(id string, requireEnabled bool) (*Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, id)
	}
	if requireEnabled && !e.enabled {
		return nil, fmt.Errorf("%w: %s", ErrProviderDisabled, id)
	}
	e.inflight++
	return &Lease{
		provider: e.provider,
		release: func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if e.inflight > 0 {
				e.inflight--
			}
		},
	}, nil
}

// Acquire pins an enabled provider for a generation request.
func (r *Registry) Acquire(id string) (*Lease, error) { return r.acquire(id, true) }

// AcquireAny pins any registered provider, including disabled ones. It is for
// management operations such as connectivity tests and model refresh.
func (r *Registry) AcquireAny(id string) (*Lease, error) { return r.acquire(id, false) }

// Chat dispatches a non-streaming request through the registry.
func (r *Registry) Chat(ctx context.Context, id string, req ChatRequest) (*ChatResponse, error) {
	lease, err := r.Acquire(id)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	return lease.Provider().Chat(ctx, req)
}

// ChatStream dispatches a streaming request. The returned stream releases the
// in-flight lease automatically on terminal read or Close.
func (r *Registry) ChatStream(ctx context.Context, id string, req ChatRequest) (Stream, error) {
	lease, err := r.Acquire(id)
	if err != nil {
		return nil, err
	}
	s, err := lease.Provider().ChatStream(ctx, req)
	if err != nil {
		lease.Release()
		return nil, err
	}
	return &leasedStream{stream: s, lease: lease}, nil
}

// Close closes every provider in reverse registration order.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	ids := append([]string(nil), r.order...)
	entries := make([]*entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, r.entries[id])
	}
	r.entries = make(map[string]*entry)
	r.order = nil
	r.mu.Unlock()

	var errs []error
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i] == nil {
			continue
		}
		if err := entries[i].provider.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("close provider %q: %w", ids[i], err))
		}
	}
	return errors.Join(errs...)
}

// leasedStream releases the registry lease on EOF, error or explicit Close.
type leasedStream struct {
	mu     sync.Mutex
	stream Stream
	lease  *Lease
	done   bool
}

func (s *leasedStream) Recv(ctx context.Context) (StreamEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return StreamEvent{}, io.EOF
	}
	ev, err := s.stream.Recv(ctx)
	if err != nil {
		s.done = true
		_ = s.stream.Close()
		s.lease.Release()
	}
	return ev, err
}

func (s *leasedStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	s.done = true
	err := s.stream.Close()
	s.lease.Release()
	return err
}
