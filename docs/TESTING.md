# Testing

All Phase 2 tests run offline. Adapters are tested against local `httptest`
servers and the deterministic mock provider; no API keys, hosted models or
internet access are required.

## Required verification

Run from the repository root (`love-code/`):

```sh
gofmt -l .                         # must print nothing
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go build ./...
```

Useful targeted runs:

```sh
go test -count=1 -v ./internal/providers
go test -count=1 -v ./internal/models
go test -count=1 -v ./internal/config
go test -count=1 -v ./internal/api
go test -race -count=1 ./internal/providers ./internal/api
```

## Coverage map

| Area | Tests | What is covered |
|---|---|---|
| Model registry | `internal/models/models_test.go` | Add/update/upsert/remove, duplicate IDs, filters, tags, availability, enable/disable, deep copies, concurrent access, capability normalization |
| Provider registry | `internal/providers/registry_test.go` | ID/metadata validation, Init errors, duplicate registration, enable/disable, in-flight leases, busy removal, reverse-order close, leased streams, disabled dispatch |
| Normalized errors | `internal/providers/errors_test.go` | Status mapping, retry-after, context/transport classification, redaction of keys and URL queries, serialization without causes |
| Request/response types | `internal/providers/types_test.go` | Request validation, limits, tool/schema checks, modality and message rules, usage pointer semantics |
| Mock adapter | `internal/providers/mock_test.go` | Determinism, usage absence, latency, failure injection, unavailable state, simulated timeout, cancellation, streaming order, mid-stream errors, model listing |
| OpenAI-compatible adapter | `internal/providers/openai_test.go` | Request envelope, headers/auth, response normalization, usage, tool calls, structured output, model listing, health, error mapping, secret redaction, malformed payloads, timeout, cancellation, SSE streaming and stream errors |
| Ollama adapter | `internal/providers/ollama_test.go` | Config validation/defaults, tags listing, health/404, chat envelope/options/format, tool calls, usage, model map, streaming NDJSON, stream failures, cancellation, connection failure |
| Manager / config integration | `internal/providers/manager_test.go` | Config-to-adapter loading, env resolution, optional failure isolation, required failures, duplicate identities, discovery merge rules, disabled providers, statuses without secrets |
| Config | `internal/config/provider_config_test.go` | TOML arrays/nesting, timeouts, headers, models, pricing/rate metadata, strict unknown fields, ID/modality/header/endpoint validation |
| Management API | `internal/api/providers_test.go`, `internal/api/api_test.go` | Auth and read/admin scopes, list/get/patch, provider test/refresh, model filters and get/patch, normalized errors, body limit, audit entries, sanitized status output, missing-manager behavior |
| Security/audit/storage (Phase 1) | existing package tests | Token auth, scopes, redaction, audit hash chain, migrations/backup |

## Streaming and cancellation tests

Streaming tests assert the contract directly: deltas arrive in order, exactly
one `done` event is emitted, the next `Recv` returns `io.EOF`, provider failures
are returned as normalized errors, and cancellation is observed promptly.
Cancellation tests release blocking test handlers during cleanup so the test
suite cannot hang on `httptest.Server.Close`.

## Concurrency

Provider and model registries are exercised concurrently under `-race`.
Registry lease tests cover removal/disable refusal while a request is in flight
and automatic release when a stream reaches EOF, errors or is closed.

## What is not tested

- Live credentialed calls to OpenAI, Together, Ollama or other hosted services.
  Those are intentionally excluded from the required offline suite.
- Real TLS certificates and cross-host networking (Phase 1 limitation remains).
- Smart routing, failover, circuit breakers and usage ledgers; those are Phase 3.
- Resource usage/benchmarks. `docs/STATUS.md` records only measurements that
  were actually taken.
