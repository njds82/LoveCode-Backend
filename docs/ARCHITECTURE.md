# LoveCode Architecture

Status of every statement below is tracked in `STATUS.md`. This document describes the
target design; Phase 1 and the Phase 2 provider/model infrastructure exist in code.
Routing, agents, tools and later phases do not.

## 1. Spec analysis: conflicts and missing decisions

| # | Issue in the spec | Decision |
|---|---|---|
| 1 | "No CGO / lightweight" vs. SQLite needs a driver | `modernc.org/sqlite` (pure Go). Slower on heavy writes than the CGO driver; benchmark in Phase 8 and keep the driver behind `internal/storage` so it can be swapped. |
| 2 | "Failover" vs. streaming | Failover is only possible **before the first byte** is sent to the client. After that, a provider failure is reported as a stream error. Mid-stream resume is not attempted. |
| 3 | "Remote without exposing a public port" vs. a backend that listens | Node Agents and remote frontends may connect **outbound** to a relay/tunnel; the transport is an interface so WireGuard/Tailscale/Cloudflare-style tunnels plug in later. Phase 1 supports direct TLS only. |
| 4 | Audit log "protected against unauthorized modification" | SQLite file owned by the same OS user cannot be made tamper-proof. We provide **tamper-evidence** (hash chain + append-only triggers). Stronger options (separate OS user, external anchoring of the head hash) are Phase 8. |
| 5 | "Decision / Action / Observation" trace vs. no chain-of-thought | `Decision` is a short, structured, agent-declared summary requested as an explicit output field. Provider "reasoning" content is never stored. |
| 6 | Quota tracking | Two separate ledgers: `provider_reported_quota` (only when a provider API exposes it, or the user enters it) and `observed_usage` (our own counters). The router never presents observed usage as remaining quota. |
| 7 | Media detection, process inspection on the backend vs. on remote devices | These live in tools that run on a **Node** (the backend is just the local node). On Linux, media uses MPRIS over D-Bus; this adds a D-Bus dependency only to the node binary. |
| 8 | Semantic cache without a vector DB | Embeddings stored as float32 BLOBs; brute-force cosine over a bounded, recency-limited candidate set. Optional, off by default, requires a user-configured embedding model. Hit only above a configurable threshold **and** matching permission/tool/prompt-version fingerprint. |
| 9 | Resource limits on 8 GB RAM | Bounded pools everywhere. Process-level limits via `GOMEMLIMIT`, per-subprocess timeouts and rlimits. cgroup limits are optional (systemd) and not assumed. |
| 10 | Windows/macOS portability vs. Linux-only tools | Platform code behind build tags and interfaces; unsupported capabilities are *not advertised* by a node rather than stubbed. |
| 11 | "Multiple databases" vs. migrations | One migration runner, one directory per logical domain; default maps all domains to one file. Cross-domain foreign keys are forbidden (so domains can be split). |
| 12 | Local-only default vs. OpenAI-compatible third-party apps | Same listener, same token auth. LAN access requires explicit `mode="lan"`. |
| 13 | Exact cache vs. permissions | Cache key includes a permission-context fingerprint; entries are never shared across fingerprints. |
| 14 | "No user accounts" vs. per-device permissions | One owner; each device/frontend has a scoped, revocable token. No user table. |
| 15 | Phase order: audit/policy are Phase 8, but tools arrive in Phase 5 | Audit and a minimal scope check ship in **Phase 1**; the richer policy engine (approvals, temporary grants) must land before any tool executes (start of Phase 5). |

## 2. Module map

Single binary, modular monolith. Dependencies point downward only.

```
cmd/lovecode, cmd/lovecode-node
        │
   api (HTTP/WS/SSE, OpenAI-compat)
        │
   agents ─ orchestration ─ router ─ providers/models
        │         │            │
      tools ─ mcp ─ devices   cache ─ memory ─ conversations ─ skills/prompts ─ automation
        │
   policies ─ security ─ audit
        │
   storage ─ config ─ core ─ monitoring ─ events
```

Rules: a package may import only packages below it; cross-cutting needs go through
`events` (in-process bus with bounded buffers) or small interfaces defined by the consumer.
Empty packages are not created in advance; each appears with its phase.

| Module | Responsibility | Exists |
|---|---|---|
| core | module lifecycle, supervised goroutines, health | Phase 1 ✔ |
| config | TOML load/validate, feature flags | Phase 1 ✔ |
| storage | SQLite open, pragmas, migrations, backup | Phase 1 ✔ |
| security | tokens, scopes, redaction | Phase 1 ✔ (pairing/rotation later) |
| audit | hash-chained audit log | Phase 1 ✔ |
| api | gateway, middleware, error model | Phase 1 ✔ (health/status/audit); Phase 2 ✔ provider/model management |
| providers, models | adapters, registry | Phase 2 ✔ (OpenAI-compatible, Ollama, mock) |
| router | rules, failover, breakers, usage | Phase 3 ✔ |
| agents, orchestration | profiles, pools, teams, traces | Phase 4 ✔ |
| tools, devices, policies | registry, nodes, approvals | Phase 5 |
| conversations, memory, cache, prompts, skills | persistence/intelligence | Phase 6 |
| automation, mcp | scheduler, MCP client | Phase 7 |
| monitoring | resource sampling, limits | Phase 1 (health) → Phase 8 |

## 3. Key interfaces (target)

```go
// providers (implemented)
type Provider interface {
    ID() string
    Kind() string
    Info() ProviderInfo
    Capabilities() ProviderCapabilities    // declared, never guessed
    ListModels(ctx context.Context) ([]ModelInfo, error)
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    ChatStream(ctx context.Context, req ChatRequest) (Stream, error)
    Health(ctx context.Context) error
    Init(ctx context.Context) error
    Close(ctx context.Context) error
}
type Stream interface {
    Recv(ctx context.Context) (StreamEvent, error) // io.EOF after one done event
    Close() error
}
type QuotaReporter interface { Quota(ctx context.Context) (*Quota, error) } // optional, later

// router
type Router interface {
    Select(ctx context.Context, need Requirements) ([]Candidate, error) // ordered fallback chain
    Report(res Outcome)                                                  // feeds breakers/usage
}

// agents
type Agent interface { Run(ctx context.Context, t Task) (Result, error) }
type Orchestrator interface {
    Submit(ctx context.Context, spec RunSpec) (RunID, error) // bounded queue; ErrQueueFull = load shedding
    Cancel(id RunID) error
}
type TraceSink interface { Record(ctx context.Context, s TraceStep) } // Decision/Action/Observation

// tools
type Tool interface {
    Spec() ToolSpec // id, description, JSON schemas, permissions, timeout, limits, risk
    Execute(ctx context.Context, call Call) (Result, error)
}
type Policy interface { Authorize(ctx context.Context, subj Subject, act Action) (Decision, error) } // Allow | Deny | NeedsApproval
```

Concurrency: default `max_concurrent_agents = 2`, queue length bounded (default 32),
per-run token/step/time budgets, `GOMEMLIMIT` default ≈ 60% of RAM.

### Phase 2 provider layer (implemented)

```
config ──► providers.Manager ──► providers.Registry ──► Provider adapter
                   │                                      │
                   └──────────► models.Registry ◄─────────┘
```

- The provider registry owns lifecycle, unique IDs, enable/disable and in-flight
  leases. Remove/disable is refused while a generation lease is active.
- The independent model registry owns LoveCode model IDs, separate provider
  model IDs, tri-state capabilities, availability, tags and optional
  pricing/rate metadata.
- Streaming is a pull iterator with a strict delta → one done → `io.EOF`
  contract. Adapters start no background goroutine; caller cancellation wins.
- Errors are normalized into stable kinds with redacted messages. Usage is
  pointer-based so absent counts are never fabricated.
- Configuration supplies `api_key_env` names, never secret values. Optional
  provider failures are isolated; required providers and duplicate identities
  fail startup.
- Details, examples and adapter-specific behavior: `docs/PROVIDERS.md`.

## 4. Database schema

Phase 1 (implemented, `migrations/0001_init.sql`): `schema_migrations`, `api_tokens`, `audit_log`.

Planned (domain → tables); IDs are text (random hex/ULID), timestamps RFC 3339 UTC:

- **providers**: `providers`, `provider_keys` (encrypted), `models`, `model_aliases`, `model_groups`, `model_group_members`, `routing_rules`, `usage_observed`, `provider_quota_reported`
- **conversations**: `conversations`, `messages`
- **memory**: `memories`, `memory_versions`, `memory_embeddings` (optional)
- **execution**: `agent_profiles`, `agent_templates`, `runs`, `tasks`, `task_deps`, `trace_steps`
- **security**: `devices`, `pairing_codes`, `policies`, `grants` (with `expires_at`), `approvals`
- **skills/prompts**: `skills`, `prompts`, `prompt_versions`
- **automation**: `triggers`, `jobs`, `job_runs`
- **cache**: `cache_entries` (key, fingerprint, expires_at), `provider_metadata_cache`
- **mcp**: `mcp_servers`, `mcp_tools` (with per-tool trust flag, default untrusted)

Phase 2 keeps providers and models in TOML configuration and in-memory registries;
the provider tables above remain planned for a later persistence phase. Phase 2
credentials are environment-variable references (`api_key_env`), so no provider
secret is written to the database or config file. When persisted credentials are
added, they must be encrypted at rest with a key from the OS keyring or a
root-only key file (candidate: XChaCha20-Poly1305 via `x/crypto`); the database
alone must not reveal keys.

## 5. Security model (target)

- **Subjects**: owner token(s), device tokens, node identities, agents (act *on behalf of* a subject; they never exceed it), MCP servers (always untrusted).
- **Scopes** are strings (`read`, `tools:fs:read`, `tools:terminal:exec`, `admin`, …); `admin` implies all. Policy = (subject, action, resource constraints) → Allow / Deny / NeedsApproval. Grants can expire.
- **Defaults**: deny. Loopback bind. No tool receives access without an explicit grant.
- **Untrusted content** (web pages, files, tool/MCP output) is tagged as data and never promoted to instructions; high-risk tool calls triggered after ingesting untrusted content require approval. This reduces, but cannot eliminate, prompt-injection risk.
- **Filesystem tools** resolve paths with `filepath.EvalSymlinks` and verify containment in allow-listed roots. **Terminal tools** use an allow-listed argv (no shell interpolation).
- **Secrets**: redaction on logs/audit/traces (`security.Redact*`), pattern-based and key-name-based; best-effort by nature.

## 6. API

Base path `/v1`. JSON errors: `{"error":{"code","message","request_id"}}`. Auth: `Authorization: Bearer lc_…`.

Phase 1: `GET /v1/health` (public, minimal), `GET /v1/status` (scope `read`), `GET /v1/audit`, `GET /v1/audit/verify` (admin).

Phase 2: `GET /v1/providers`, `GET /v1/providers/{id}`, `PATCH /v1/providers/{id}`, `POST /v1/providers/{id}/test`, `POST /v1/providers/{id}/models/refresh`, `GET /v1/models`, `GET /v1/models/{id...}`, `PATCH /v1/models/{id...}`. Reads need `read`; mutations need `admin` and are audited. Details and response shapes: `docs/API.md`.

Planned: `/v1/chat/completions` (SSE), `/v1/agents`, `/v1/runs`, `/v1/runs/{id}/trace`, `/v1/tools`, `/v1/devices` (+pairing), `/v1/conversations`, `/v1/memory`, `/v1/cache`, `/v1/automation`, `/v1/mcp`, `/v1/config`, `/v1/monitoring`, `/v1/ws`. OpenAI-compatible surface (`/v1/chat/completions`, `/v1/models`, `/v1/embeddings`) is a thin translation layer over the richer internal types. Breaking changes go to `/v2`.

## 7. Dependency report

| Dependency | Why | Alternatives rejected |
|---|---|---|
| `modernc.org/sqlite` | pure-Go SQLite driver, no CGO | `mattn/go-sqlite3` (CGO, faster; kept as swap option) |
| `pelletier/go-toml/v2` | TOML with strict unknown-key detection | stdlib has no TOML; YAML libs are heavier |
| stdlib `net/http` (1.22 mux) | method+path routing is sufficient | chi/gin (not needed yet) |
| stdlib `log/slog` | structured logging | zap/zerolog |
| stdlib `net/http`, `httptest`, `encoding/json` | provider adapters and offline contract tests | provider SDKs (unnecessary for HTTP APIs; harder to audit) |

Phase 2 added no dependencies. Planned, to be justified when needed: a WebSocket library (Phase 5/9), `golang.org/x/crypto` (secret encryption), a cron-expression parser (Phase 7; may be hand-written), a D-Bus library (node only).

## 8. Open decisions

CGO vs. pure-Go SQLite performance; secret-encryption key custody; relay/tunnel choice;
embedding source for semantic cache; MCP SDK vs. hand-rolled client.

## 9. Phase plan and acceptance criteria

Phases follow the specification (§24). Each phase is done only when its criteria pass on the target hardware class and `STATUS.md` is updated.

1. **Foundation** — `go test -race ./...` green; server starts with defaults on loopback; `/v1/health` 200; `/v1/status` 401 without token; migrations idempotent; SIGTERM shuts down cleanly; audit chain verifies; no secret appears in logs/audit (tested); idle RSS and startup time *measured and recorded*.
2. **Providers** — implemented: mock contract tests and offline OpenAI-compatible/Ollama adapter tests pass; streaming cancellation and key redaction are tested. Live credentialed integration tests against hosted services have **not** been run (see `STATUS.md`).
3. **Routing** — not started; failover, breakers, rate-limit compliance and usage accounting remain to be implemented and tested.
4. **Agents** — N+1 submissions with limit N never run more than N concurrently (race test); cancellation propagates to children; trace reconstructs a run chronologically; memory stays bounded under a 1000-task stress test.
5. **Tools & nodes** — path-traversal and symlink-escape tests; command-injection tests; node pairing, revocation and reconnect tests; every tool call is audited and policy-checked.
6. **Storage/intelligence** — cache never crosses permission fingerprints (test); memory versioning/expiry tests; semantic cache threshold tests.
7. **Automation/MCP** — jobs survive restart; MCP tools untrusted until approved; server crash recovery.
8. **Hardening** — benchmark report (idle RAM/CPU, startup, p50/p99 latency); backup/restore drill; audit anchoring.
9. **API stabilization** — OpenAPI document; OpenAI-compat conformance tests against a reference client; WebSocket event spec; example clients.

## Phase 3 routing and generation

The `internal/routing` package sits between the API and the provider/model registries. It resolves profile/group candidates, applies capability and enabled-state filtering, uses bounded retries and provider-scoped circuit breakers, and dispatches normalized requests through the provider registry. `POST /v1/chat/completions` is the provider-independent generation boundary. Persistent routing profiles and groups are stored in SQLite (`0002_routing.sql`); see [ROUTING.md](ROUTING.md) for operational behavior and limitations.

## Phase 4 Agent Engine & Multi-Agent Orchestration

The `internal/agents` package provides an in-process, frontend-independent agent runtime.
- **Agent Registry**: Persistent definitions in SQLite (`0003_agents.sql`) with in-memory caching and thread-safe CRUD.
- **Agent Runtime**: Loads agent definitions, builds bounded context, and invokes `routing.Router` directly via an internal `Generator` interface.
- **Teams**: Sequential pipelines, parallel execution groups, and dependency DAGs with cycle detection and configurable failure policies (`fail_fast`, `continue_independent`, `best_effort`).
- **Subagents & Dynamic Agents**: Ephemeral dynamic agents created for a run with strict privilege inheritance rules preventing privilege escalation.
- **Result Aggregation**: Strategies (`collect_all`, `ordered`, `primary`, `synthesizer`, `merge`) with automatic conflict detection.
- **Scheduler**: Global run concurrency semaphores, agent semaphores, per-run parallel limits, and queue limit rejection.
- **Persistence & Recovery**: Interrupted executions are marked failed upon restart without re-triggering partial LLM requests.
- **Unified API**: Endpoints under `/v1/agents`, `/v1/teams`, `/v1/agent-runs`, `/v1/agent-runs/{id}/tasks`.
