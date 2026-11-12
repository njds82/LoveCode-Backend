# Agent Teams (Phase 4)

LoveCode supports collaborative agent teams coordinating multiple agent definitions under explicit execution strategies and policies.

## Team Execution Strategies

### 1. Sequential Teams (`sequential`)
- Executes member agents in ordered succession.
- Results from one member are passed to subsequent members according to their context policy (`handoff`, `shared`, or `summarized`).
- Useful for multi-stage pipelines: e.g. Researcher -> Analyst -> Reviewer -> Synthesizer.

### 2. Parallel Teams (`parallel`)
- Executes member agents concurrently against the task input.
- Concurrency is bounded by `max_parallel` per team and global scheduler concurrency limits.
- Results are collected and passed to the team's result aggregator.

### 3. Dependency-Based Teams (`dependency`)
- Configures task prerequisite DAGs (`dependencies`).
- Detects circular dependencies before execution starts using depth-first search cycle detection.
- Tasks execute as soon as their required dependencies complete successfully.
- If a dependency fails or is cancelled, dependent tasks fail cleanly without deadlock.

## Result Aggregation

Teams specify an `aggregation_policy`:
- **Collect All (`collect_all`)**: Returns all agent outputs attributed by agent ID.
- **Ordered (`ordered`)**: Formats results in member execution sequence.
- **Primary (`primary`)**: Returns the designated primary agent's result, with fallback to successful members.
- **Dedicated Synthesizer (`synthesizer`)**: Executes a designated synthesizer agent that summarizes and reconciles the outputs of all contributing members through the routing engine.
- **Structured Merge (`merge`)**: Combines sectioned outputs into a unified multi-agent report.

### Conflict Handling
- The aggregator analyzes divergence across member outputs (e.g. conflicting recommendations or sentiment).
- Conflicts are recorded in `conflict_details` and flagged with `has_conflicts: true` without discarding individual outputs.

## Failure Policies

Configured via `failure_policy`:
- **Fail Fast (`fail_fast`)**: Halts team execution and cancels in-flight tasks immediately if any member fails.
- **Continue Independent (`continue_independent`)**: Allows independent tasks to proceed even if an unrelated branch fails.
- **Best Effort (`best_effort`)**: Runs all runnable members and returns whatever succeeds.
- **Required Members (`required: true`)**: Any member flagged as required causes the entire run to fail if its task fails.
