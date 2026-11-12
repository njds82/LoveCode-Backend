# Routing (Phase 3)

LoveCode routes normalized `providers.ChatRequest` values through `internal/routing`; the HTTP API does not select adapter implementations.

## Profiles and groups

Routing profiles and model groups are persisted in SQLite (via `0002_routing.sql`) and automatically reloaded on startup. A profile contains ordered candidates (`provider_id`, `model_id`, `priority`, `weight`, `max_concurrent`), a policy (`priority`, `weighted`, or `ordered`), a bounded `max_attempts`, and an optional timeout.

- Candidate priority sorts descending in `priority` mode.
- Weighted selection dynamically calculates candidate weights in `weighted` mode.
- Ordered selection follows the candidate order specified in `ordered` mode.
- Model groups are named lists of LoveCode model IDs.

Configure with authenticated admin endpoints:
- `PUT /v1/routing/profiles/{id}` with `{"policy":"priority","max_attempts":3,"candidates":[{"provider_id":"mock","model_id":"mock:chat","priority":10,"weight":1,"max_concurrent":5}]}`
- `PUT /v1/routing/groups/{id}` with `{"models":["mock:chat"]}`
- `PUT /v1/routing/breakers/{id}` with `{"threshold":3,"recovery":30000000000,"max_probes":1}`

Read with:
- `GET /v1/routing/profiles`
- `GET /v1/routing/groups`
- `GET /v1/routing/status`
- `GET /v1/routing/usage` (supports query params: `provider`, `model`, `success`, `since`, `until`, `limit`)
- `GET /v1/routing/usage/summary`
- `GET /v1/routing/ratelimits`
- `GET /v1/routing/health`

## Generation

`POST /v1/chat/completions` accepts OpenAI-style messages, `model`, optional `provider`, `routing_profile`, `model_group`, `temperature`, `top_p`, `max_tokens`, `tools`, `stop`, `seed`, and `stream`.
- Use `"model":"auto"` with a profile or group for automatic selection.
- A specific LoveCode model ID can be requested directly.
- The response returns normalized message/usage fields.
- Requests require a bearer token with `write` scope. Management operations require `admin` scope.

## Retry, Backoff & Jitter

When candidate execution fails with a transient error, the router automatically calculates exponential backoff:
- `base_delay`: 50ms default
- `max_delay`: 2s default
- `jitter_fraction`: ±25% random jitter
- `Retry-After`: If the provider returns a `Retry-After` duration (or header), it is respected up to `max_delay`.
- Context cancellation terminates waiting immediately without unnecessary sleeping.
- Permanent errors (`KindAuthentication`, `KindInvalidRequest`, `KindUnsupported`, `KindQuotaExceeded`) fail immediately without retry.

## Circuit Breakers

Circuit breakers are provider-scoped with configurable thresholds:
- **Closed**: normal operation.
- **Open**: after `threshold` consecutive transient failures (default 3), the circuit opens and rejects requests immediately.
- **Half-Open**: after `recovery` window (default 30s), limited probes (`max_probes`, default 1) are admitted.
- A successful probe closes the circuit; a failed probe returns it to Open.
- Configurable per-provider via `PUT /v1/routing/breakers/{id}` and persisted in SQLite.

## Concurrency Limits

The router supports multi-level concurrency limits:
- Per-candidate/model limit (`max_concurrent`).
- Per-provider limit (`SetProviderConcurrencyLimit`).
- Safe resource release on success, failure, timeout, or streaming completion.

## Streaming Correctness

- **Before the first committed response event**: The router may transparently fail over to another candidate in the chain.
- **After the first committed response event**: Provider switching is strictly forbidden. Any subsequent failure is emitted as an SSE `event: error` chunk and the stream is cleanly closed.
- SSE output properly concludes with `data: [DONE]`.

## Rate Limit & Usage Tracking

- HTTP 429 events and `Retry-After` headers trigger an active cooldown window for the provider.
- Observed rate-limit headers (`x-ratelimit-remaining-requests`, `x-ratelimit-remaining-tokens`) are stored.
- Normalized token usage (`prompt_tokens`, `completion_tokens`, `total_tokens`) and request durations are logged to SQLite (`usage_records`).
- Missing tokens are recorded as NULL without fabricating zero values.
