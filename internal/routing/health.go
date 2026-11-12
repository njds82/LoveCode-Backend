package routing

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lovecode/lovecode/internal/providers"
)

// HealthMonitor periodically tests enabled providers in the background.
type HealthMonitor struct {
	pm       *providers.Manager
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup

	mu      sync.RWMutex
	status  map[string]ProviderHealthInfo
	running bool
}

// ProviderHealthInfo holds the latest background health check results.
type ProviderHealthInfo struct {
	ProviderID string        `json:"provider_id"`
	Healthy    bool          `json:"healthy"`
	Latency    time.Duration `json:"latency,omitempty"`
	LastError  string        `json:"last_error,omitempty"`
	CheckedAt  time.Time     `json:"checked_at"`
}

// NewHealthMonitor creates a health monitor with a configurable check interval.
func NewHealthMonitor(pm *providers.Manager, interval time.Duration) *HealthMonitor {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &HealthMonitor{
		pm:       pm,
		interval: interval,
		status:   make(map[string]ProviderHealthInfo),
	}
}

// Start begins the background monitoring goroutine. If disabled or already running, does nothing.
func (hm *HealthMonitor) Start() {
	if hm == nil || hm.pm == nil {
		return
	}
	hm.mu.Lock()
	if hm.running {
		hm.mu.Unlock()
		return
	}
	hm.running = true
	hm.stopCh = make(chan struct{})
	hm.mu.Unlock()

	hm.wg.Add(1)
	go hm.loop()
}

// Stop gracefully shuts down the background health monitor.
func (hm *HealthMonitor) Stop() {
	if hm == nil {
		return
	}
	hm.mu.Lock()
	if !hm.running {
		hm.mu.Unlock()
		return
	}
	hm.running = false
	close(hm.stopCh)
	hm.mu.Unlock()

	hm.wg.Wait()
}

func (hm *HealthMonitor) loop() {
	defer hm.wg.Done()

	// Initial check
	hm.checkAll()

	ticker := time.NewTicker(hm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-hm.stopCh:
			return
		case <-ticker.C:
			hm.checkAll()
		}
	}
}

func (hm *HealthMonitor) checkAll() {
	if hm.pm == nil {
		return
	}
	statuses := hm.pm.ProviderStatuses()
	for _, st := range statuses {
		if !st.Enabled {
			continue
		}
		// Test provider health with bounded timeout (5s)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		err := hm.pm.TestProvider(ctx, st.ID)
		dur := time.Since(start)
		cancel()

		hm.mu.Lock()
		info := ProviderHealthInfo{
			ProviderID: st.ID,
			Healthy:    err == nil,
			Latency:    dur,
			CheckedAt:  time.Now(),
		}
		if err != nil {
			info.LastError = fmt.Sprintf("%v", err)
		}
		hm.status[st.ID] = info
		hm.mu.Unlock()
	}
}

// Status returns a list of health info for all monitored providers.
func (hm *HealthMonitor) Status() []ProviderHealthInfo {
	if hm == nil {
		return nil
	}
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	out := make([]ProviderHealthInfo, 0, len(hm.status))
	for _, info := range hm.status {
		out = append(out, info)
	}
	return out
}

// RecordObservedResult updates health state from real generation results.
func (hm *HealthMonitor) RecordObservedResult(providerID string, err error) {
	if hm == nil || providerID == "" {
		return
	}
	hm.mu.Lock()
	defer hm.mu.Unlock()

	info := hm.status[providerID]
	info.ProviderID = providerID
	info.CheckedAt = time.Now()
	if err == nil {
		info.Healthy = true
		info.LastError = ""
	} else {
		info.Healthy = false
		info.LastError = err.Error()
	}
	hm.status[providerID] = info
}
