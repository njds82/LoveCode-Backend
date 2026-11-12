# HTTP API (Phase 1–2)

Base path: `/v1`. Requests and responses are JSON. Errors use:

```json
{"error":{"code":"invalid_request","message":"enabled is required","request_id":"…"}}
```

Authentication is `Authorization: Bearer lc_…`. The owner token is written to
`$XDG_DATA_HOME/lovecode/owner.token` on first start (mode 0600). `admin` implies
every scope; `read` allows only read endpoints. In local mode non-loopback peers
are rejected regardless of token.

The global request body limit is `server.max_body_bytes` (default 4 MiB).
Management writes reject malformed JSON, unknown fields, a missing `enabled`
field and trailing JSON values. Mutations are written to the hash-chained audit
log with the actor from the bearer token.

## Endpoints

| Method | Path | Scope | Purpose |
|---|---|---|---|
| GET | `/v1/health` | public | Minimal liveness response. |
| GET | `/v1/status` | `read` | Version, mode, uptime, module health. |
| GET | `/v1/audit` | `admin` | List audit entries (`limit`, `action` filters). |
| GET | `/v1/audit/verify` | `admin` | Verify the audit hash chain. |
| GET | `/v1/providers` | `read` | List configured provider statuses. |
| GET | `/v1/providers/{id}` | `read` | One provider status. |
| PATCH | `/v1/providers/{id}` | `admin` | Enable or disable a provider. |
| POST | `/v1/providers/{id}/test` | `admin` | Run a bounded health check. |
| POST | `/v1/providers/{id}/models/refresh` | `admin` | Discover and merge models. |
| GET | `/v1/models` | `read` | List/filter the model registry. |
| GET | `/v1/models/{id...}` | `read` | One model; the wildcard accepts `/` in IDs. |
| PATCH | `/v1/models/{id...}` | `admin` | Enable or disable a model. |

## Provider status

`GET /v1/providers` returns `{"providers":[…]}`. Each item is a status snapshot
without credentials or raw configuration:

```json
{
  "id": "openai",
  "kind": "openai",
  "display_name": "OpenAI",
  "description": "Primary cloud provider",
  "capabilities": {
    "streaming": "supported",
    "tool_calls": "supported",
    "structured_output": "supported",
    "model_listing": "supported",
    "health_check": "supported"
  },
  "enabled": true,
  "in_flight": 0,
  "registered_at": "2026-11-12T00:00:00Z",
  "endpoint": "https://api.openai.com/v1",
  "model_count": 3,
  "last_error": "",
  "last_error_kind": ""
}
```

- `endpoint` has userinfo, query and fragment removed.
- `last_error`/`last_error_kind` describe a provider that failed to initialize;
  such a provider is not registered but remains visible in the list.
- `enabled` is the operator switch. `in_flight` counts leased generations.

### Enable/disable a provider

```sh
curl -s -X PATCH http://127.0.0.1:7878/v1/providers/openai \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"enabled":false}'
```

Returns the updated status. Disabling is refused with `409 provider_busy` when
requests are in flight; enabling is always allowed. The audit actions are
`provider.disabled` and `provider.enabled`.

### Test a provider

```sh
curl -s -X POST http://127.0.0.1:7878/v1/providers/openai/test \
  -H "Authorization: Bearer $TOKEN"
```

Success: `{"provider_id":"openai","status":"ok"}`. A failed health check returns
a normalized provider error and is still audited as `provider.test` with
`"ok": false`. Testing works while a provider is disabled. A health check is
bounded by the provider's `health_timeout`.

### Refresh model discovery

```sh
curl -s -X POST http://127.0.0.1:7878/v1/providers/ollama/models/refresh \
  -H "Authorization: Bearer $TOKEN"
```

Response:

```json
{"provider_id":"ollama","added":2,"updated":1,"total":8}
```

Discovery adds new models, fills unknown metadata for existing models and marks
discovered models available. It never deletes models, never overwrites operator
tags or explicit capabilities, and never changes the enabled flag. Audit action:
`provider.refresh_models`.

## Models

`GET /v1/models` returns `{"models":[…]}` sorted by LoveCode model ID. Query
filters:

| Query | Values | Meaning |
|---|---|---|
| `provider_id` | provider ID | Only models from that provider. |
| `tag` | free text | Models carrying the exact tag. |
| `enabled` | `true`/`false` | Operator enabled state. |
| `availability` | `unknown`/`available`/`unavailable` | Last observed availability. |

Invalid boolean or availability values return `400 invalid_request`.

A model record includes its stable ID, provider identity, display metadata,
context/output limits, tri-state capabilities, availability, tags, optional
pricing and rate metadata, and the enabled flag. Example:

```json
{
  "id": "openai:gpt-4.1",
  "provider_id": "openai",
  "provider_model_id": "gpt-4.1",
  "display_name": "GPT-4.1",
  "context_window": 1047576,
  "input_modalities": ["text", "image"],
  "output_modalities": ["text"],
  "streaming": "supported",
  "tool_calls": "supported",
  "structured_output": "supported",
  "reasoning": "unknown",
  "availability": "unknown",
  "tags": ["cloud","general"],
  "pricing": {"input_per_million":2,"output_per_million":8,"currency":"USD"},
  "enabled": true
}
```

The three capability states are `unknown`, `supported` and `unsupported`.
Unknown is not a claim of support. An empty modality list means unknown.

### Enable/disable a model

```sh
curl -s -X PATCH 'http://127.0.0.1:7878/v1/models/openai:gpt-4.1' \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"enabled":false}'
```

Returns the updated model. This does not change its availability. Audit actions:
`model.disabled` and `model.enabled`.

For IDs containing `/`, use the wildcard path directly (for example
`/v1/models/local:library/model`) or a percent-encoded slash accepted by the
client/HTTP stack.

## Error mapping for management operations

| Condition | HTTP | `error.code` |
|---|---|---|
| Missing/invalid bearer token | 401 | `unauthorized` |
| Insufficient scope | 403 | `forbidden` |
| Invalid JSON/query/body field | 400 | `invalid_request` |
| Body over limit | 413 | `body_too_large` |
| Unknown provider/model | 404 | `not_found` |
| Provider busy or disabled | 409 | `provider_busy` / `provider_disabled` |
| Provider rate/quota limit | 429 | `rate_limited` / `quota_exceeded` |
| Provider timeout | 504 | `timeout` |
| Provider connection/unavailable | 503 | `connection_failure` / `provider_unavailable` |
| Upstream authentication/internal/malformed response | 502 | corresponding provider kind |
| Missing provider manager (misconfigured embedding) | 503 | `providers_unavailable` |

`Retry-After` is set when the provider supplied it.

## Audit actions added in Phase 2

| Action | Detail |
|---|---|
| `provider.enabled`, `provider.disabled` | `{"enabled":bool}` |
| `provider.test` | `{"ok":bool,"kind":…}` |
| `provider.refresh_models` | `{"ok":bool,"added":…,"updated":…,"total":…}` |
| `model.enabled`, `model.disabled` | `{"enabled":bool}` |
| `provider.init_failed` | Startup-only; provider ID, kind, sanitized error |
| `provider.load_failed` | Startup-only; sanitized aggregate error |

Audit detail is passed through `security.RedactValue` before storage. Provider
API keys are never returned by any endpoint.

## Phase 3 generation and routing

- `POST /v1/chat/completions` — requires `write`; OpenAI-style chat generation, optional SSE.
- `GET /v1/routing/profiles` — requires `read`.
- `PUT /v1/routing/profiles/{id}` — requires `admin`.
- `GET /v1/routing/groups` — requires `read`.
- `PUT /v1/routing/groups/{id}` — requires `admin`.
- `GET /v1/routing/status` — requires `read`; circuit breaker snapshots and configured profile/group counts.

Profiles, groups and breakers persist across restarts in SQLite (`0002_routing.sql`). See ROUTING.md for payloads and behavior.

## Phase 4 Agent Engine

### Agent Management

- `GET /v1/agents` (`read`): List registered agents. Query `?all=true` includes disabled agents.
- `POST /v1/agents` (`admin`): Register a new agent definition.
- `GET /v1/agents/{id}` (`read`): Retrieve an agent definition by ID.
- `PUT /v1/agents/{id}` (`admin`): Update an existing agent definition.
- `PATCH /v1/agents/{id}` (`admin`): Enable or disable an agent (`{"enabled": bool}`).
- `DELETE /v1/agents/{id}` (`admin`): Delete an agent definition. Historical execution records are preserved.

### Team Management

- `GET /v1/teams` (`read`): List registered agent teams. Query `?all=true` includes disabled teams.
- `POST /v1/teams` (`admin`): Register a new team definition with member roles and strategies (`sequential`, `parallel`, `dependency`).
- `GET /v1/teams/{id}` (`read`): Retrieve a team definition by ID.
- `PUT /v1/teams/{id}` (`admin`): Update an existing team definition.
- `PATCH /v1/teams/{id}` (`admin`): Enable or disable a team (`{"enabled": bool}`).
- `DELETE /v1/teams/{id}` (`admin`): Delete a team definition.

### Runs & Execution

- `POST /v1/agent-runs` (`write`): Start an agent or team execution run.
  - Body: `{"run_type":"agent"|"team", "agent_id":"...", "team_id":"...", "input":"...", "timeout_ms": 30000, "dynamic_agents": [...]}`.
- `GET /v1/agent-runs` (`read`): List historical and active execution runs (`?limit=50&offset=0`).
- `GET /v1/agent-runs/{id}` (`read`): Get execution status, aggregated result, token usage, and structured outputs.
- `POST /v1/agent-runs/{id}/cancel` (`write`): Cancel an active run and propagate cancellation to child tasks.
- `DELETE /v1/agent-runs/{id}` (`write`): Alias for run cancellation.

### Tasks

- `GET /v1/agent-runs/{id}/tasks` (`read`): List all tasks executed for a given run ID.
- `GET /v1/agent-runs/{id}/tasks/{taskId}` (`read`): Get detailed status, output, and dependency metadata for a specific task.
- `POST /v1/agent-runs/{id}/tasks/{taskId}/cancel` (`write`): Cancel an individual in-flight task.

