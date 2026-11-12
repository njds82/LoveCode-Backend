# Agent Engine (Phase 4)

LoveCode's Agent Engine provides single-agent and multi-agent coordination, subagents, and bounded execution directly over the Phase 3 Routing Engine.

## Agent Definition

Agents are logical execution units configured in SQLite (`agent_definitions`) and memory-cached by `internal/agents.Registry`. Agents select models via Phase 3 routing profiles or explicit model IDs, preventing vendor lock-in.

### Fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique agent identifier (e.g., `researcher`, `summarizer`). |
| `name` | string | Display name. |
| `description` | string | Detailed description of the agent's responsibilities. |
| `role` | string | Role persona injected into the prompt. |
| `system_prompt` | string | Core instructions for the agent. |
| `model` | string | Model ID, group, or empty for automatic routing selection. |
| `routing_profile`| string | Routing profile ID (e.g. `default`, `fast`). |
| `params` | object | Generation parameters (e.g. `temperature`, `top_p`). |
| `max_tokens` | integer| Maximum output tokens for generation requests. |
| `timeout_ms` | integer| Per-agent execution timeout in milliseconds. |
| `max_requests` | integer| Bounded generation turns per task (default 1). |
| `autonomy_level` | string | `readonly`, `guided`, or `autonomous`. |
| `context_policy` | string | Context boundary: `isolated`, `shared`, `handoff`, `summarized`. |
| `capabilities` | string[] | Declared capabilities for future tools/phases. |
| `permissions` | string[] | Permission scopes granted to the agent. |
| `enabled` | boolean | Enabled or disabled flag. |

## Agent Lifecycle

States:
- **Pending**: Created, awaiting queue slot.
- **Queued**: Awaiting worker run concurrency slot.
- **Running**: Active generation through router.
- **Waiting**: Awaiting subagent, tool, or predecessor task.
- **Completed**: Successfully finished with structured output.
- **Failed**: Execution or validation error encountered.
- **Cancelled**: Explicitly cancelled by operator or parent run.
- **TimedOut**: Execution exceeded configured timeout limit.

Transitions are strictly validated by `ValidateTransition(from, to)`. Terminal states cannot transition to any other state.

## Context Policies

1. **Isolated (`isolated`)**: The agent receives only its own instructions and explicit task input.
2. **Shared (`shared`)**: The agent receives the shared run context and broadcast messages from other agents in the same run.
3. **Handoff (`handoff`)**: The agent receives explicit structured output and handoff data from predecessor agents.
4. **Summarized (`summarized`)**: The agent receives a bounded summary of previous task results.

Hidden chain-of-thought is never stored, extracted, or transmitted. All communication occurs through explicit messages and observable results.

## Subagents and Dynamic Agent Creation

Agents may delegate tasks to child subagents or create temporary dynamic agents during execution:
- **Privilege Inheritance**: Child agents strictly inherit parent permissions; any attempt to grant permissions the parent lacks is blocked as privilege escalation.
- **Resource Bounds**: Enforces `max_dynamic_agents` and `max_delegation_depth`.
- **Ephemeral Lifecycle**: Dynamic agents are cleared when the run completes and do not pollute the permanent registry.
