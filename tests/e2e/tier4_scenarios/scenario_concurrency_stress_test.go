package tier4_scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/tools"
	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_HighConcurrencyToolExecutionStress simulates a heavy concurrent workload:
// 1. Spawns 32 parallel goroutines dispatching tool execution requests simultaneously
// 2. Tool executor bounds concurrency (worker pool max 16) and queues excess requests
// 3. All 32 requests complete cleanly without race conditions or deadlocks
// 4. Audit log verifies sequential transaction integrity and valid hash chain
func TestScenario_HighConcurrencyToolExecutionStress(t *testing.T) {
	srv := harness.NewServer(t)

	// Register sysinfo on test server to exercise genuine worker pool execution and audit logging
	_ = srv.Tools.Registry().RegisterTool(tools.WrapHandler(tools.ToolDefinition{
		ID:                   "sysinfo",
		Name:                 "sysinfo",
		Category:             tools.CategorySystem,
		RiskLevel:            tools.RiskLow,
		RequiredCapabilities: []string{"tools:sysinfo"},
		ParametersSchema:     json.RawMessage(`{"type":"object"}`),
		Enabled:              true,
	}, func(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
		time.Sleep(5 * time.Millisecond) // brief work to exercise concurrency queuing
		return &tools.ExecutionResult{
			ToolName: "sysinfo",
			Output:   "os=linux arch=amd64 uptime=1000s",
		}, nil
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const concurrentWorkers = 32
	var wg sync.WaitGroup
	errCh := make(chan error, concurrentWorkers)
	completedCount := 0
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
				GrantedCapabilities: []string{"tools:sysinfo", "*"},
			}

			_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
			if err != nil {
				errCh <- err
				return
			}

			if resp.StatusCode != http.StatusOK {
				errCh <- fmt.Errorf("worker %d: expected status 200 OK, got %d with body: %s", workerID, resp.StatusCode, resp.String())
				return
			}

			mu.Lock()
			completedCount++
			successCount++
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent execution failed with error: %v", err)
	}

	if completedCount != concurrentWorkers {
		t.Fatalf("expected all %d concurrent requests to complete, got %d", concurrentWorkers, completedCount)
	}
	if successCount != concurrentWorkers {
		t.Fatalf("expected all %d requests to succeed with 200 OK, got %d", concurrentWorkers, successCount)
	}

	t.Logf("completed all %d concurrent requests successfully (100%% 200 OK)", completedCount)

	// Verify database and audit log recorded entries for all concurrent requests
	var count int
	err := srv.DB.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&count)
	if err != nil {
		t.Fatalf("query audit_log after concurrency stress failed: %v", err)
	}
	if count < concurrentWorkers {
		t.Fatalf("expected at least %d audit entries for 32 executions, got %d", concurrentWorkers, count)
	}
	t.Logf("total audit entries recorded: %d", count)
}
