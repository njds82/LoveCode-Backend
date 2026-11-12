package routing

import (
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/providers"
)

func TestProfileValidationAndSnapshot(t *testing.T) {
	r := New(nil)
	if err := r.SetProfile(Profile{ID: "bad", Policy: "mystery", Candidates: []Candidate{{ProviderID: "p", ModelID: "m"}}}); err == nil {
		t.Fatal("expected invalid policy")
	}
	p := Profile{ID: "default", Policy: Priority, Candidates: []Candidate{{ProviderID: "mock", ModelID: "mock:chat", Priority: 9, Weight: 1}}}
	if err := r.SetProfile(p); err != nil {
		t.Fatal(err)
	}
	got := r.Profiles()
	if len(got) != 1 || got[0].ID != "default" || got[0].MaxAttempts != 3 {
		t.Fatalf("unexpected profile snapshot: %#v", got)
	}
	got[0].Candidates[0].ModelID = "mutated"
	if r.Profiles()[0].Candidates[0].ModelID != "mock:chat" {
		t.Fatal("profile snapshot aliases internal candidate slice")
	}
}
func TestGroupValidationAndSnapshot(t *testing.T) {
	r := New(nil)
	if err := r.SetGroup(Group{ID: "g", Models: []string{"m", "m"}}); err == nil {
		t.Fatal("expected duplicate rejection")
	}
	if err := r.SetGroup(Group{ID: "g", Models: []string{"m1", "m2"}}); err != nil {
		t.Fatal(err)
	}
	g := r.Groups()
	g[0].Models[0] = "changed"
	if r.Groups()[0].Models[0] != "m1" {
		t.Fatal("group snapshot aliases internal model slice")
	}
}
func TestCircuitBreakerTripsOnlyRetryableFailures(t *testing.T) {
	r := New(nil)
	permanent := providers.NewError(providers.KindAuthentication, "p", "denied")
	r.failure("p", permanent)
	if got := r.Breakers(); len(got) != 1 || got[0].State != Closed || got[0].Failures != 0 {
		t.Fatalf("permanent error changed breaker: %#v", got)
	}
	transient := providers.NewError(providers.KindUnavailable, "p", "temporary")
	for i := 0; i < 3; i++ {
		r.failure("p", transient)
	}
	got := r.Breakers()
	if got[0].State != Open || got[0].Failures != 3 {
		t.Fatalf("breaker did not open: %#v", got)
	}
	if r.allow("p") {
		t.Fatal("open breaker admitted request before recovery")
	}
}
func TestHalfOpenProbeLimit(t *testing.T) {
	r := New(nil)
	b := r.breakerFor("p")
	b.mu.Lock()
	b.state = Open
	b.opened = time.Now().Add(-time.Minute)
	b.mu.Unlock()
	if !r.allow("p") {
		t.Fatal("expected recovery probe")
	}
	if r.allow("p") {
		t.Fatal("expected only one half-open probe")
	}
}
