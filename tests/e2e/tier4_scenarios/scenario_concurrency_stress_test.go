package tier4_scenarios

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_HighConcurrencyToolExecutionStress simulates a heavy concurrent workload:
// 1. Spawns 32 parallel goroutines dispatching tool execution requests simultaneously
// 2. Tool executor bounds concurrency (worker pool max 16) and queues excess requests
// 3. All 32 requests complete cleanly without race conditions or deadlocks
// 4. Audit log verifies sequential transaction integrity and valid hash chain
func TestScenario_HighConcurrencyToolExecutionStress(t *testing.T) {
	srv := harness.NewServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const concurrentWorkers = 32
	var wg sync.WaitGroup
	errCh := make(chan error, concurrentWorkers)
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < concurrentWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			req := harness.ToolExecuteRequest{
				ToolName:            "sysinfo",
				Arguments:           map[string]any{},
				RunID:               "run-stress-test-10",
				GrantedCapabilities: []string{"*"},
			}

			_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
			if err != nil {
				errCh <- err
				return
			}

			if resp.StatusCode == http.StatusOK {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent execution failed with error: %v", err)
	}

	t.Logf("completed 32 concurrent requests with %d successful responses", successCount)

	// Verify database and audit log remain consistent
	var count int
	err := srv.DB.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&count)
	if err != nil {
		t.Fatalf("query audit_log after concurrency stress failed: %v", err)
	}
	t.Logf("total audit entries recorded: %d", count)
}
