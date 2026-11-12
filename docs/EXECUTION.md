# Execution Scheduling, Resource Limits & Persistence (Phase 4)

LoveCode is designed for resource-conscious execution on constrained hardware (e.g. 8 GB RAM, HDD storage). The Agent Engine enforces strict execution bounds and guarantees safe shutdown and restart recovery.

## Resource Limits

All concurrency limits are configurable in `[agents]` in `configs/lovecode.toml`:

```toml
[agents]
max_concurrent_runs = 8        # Global active run limit
max_concurrent_agents = 4      # Global active generation/agent execution limit
max_parallel_per_run = 4       # Max concurrency per individual team run
max_team_size = 16             # Max members allowed in a team
max_delegation_depth = 3       # Max subagent nesting depth
max_dynamic_agents = 4         # Max ephemeral agents allowed per run
max_tasks_per_run = 50         # Max total tasks per run
max_execution_duration = "10m" # Max timeout for a single run
max_requests_per_task = 5      # Max generation requests per task
queue_limit = 32               # Rejects submissions when queued + active >= limit
```

- **Zero goroutine leaks**: Context cancellation and semaphore acquisition defer releases guarantee clean resource recovery under errors, cancellations, and timeouts.
- **Queue shedding**: If the queue limit is reached, submissions immediately return HTTP 429 (`queue_full`) rather than accumulating boundless memory.

## Persistence & Restart Recovery

All definitions and execution state are persisted in SQLite:
- `agent_definitions`: Permanent registered agents.
- `team_definitions`: Permanent team configurations.
- `agent_runs`: Historical run records, status, aggregated outputs, and token counts.
- `agent_tasks`: Individual task execution records and dependencies.
- `agent_messages`: Communication between agents in a run.

### Crash & Restart Recovery
When LoveCode starts up:
1. `RecoverInterrupted()` queries any runs and tasks with non-terminal states (`pending`, `queued`, `running`, `waiting`).
2. Marks them as `failed` with error message `"interrupted by system restart"`.
3. In-flight generation is not blindly resumed, preventing corrupted or duplicate LLM requests.
4. Historical records remain intact; deleting an agent definition never deletes execution history.
