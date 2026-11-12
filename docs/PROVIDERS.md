# Providers and Models (Phase 2)

Phase 2 adds a provider-independent model layer, a provider registry, a separate
model registry and three adapters. It deliberately does **not** add smart
routing, failover or agent execution; those remain Phase 3 and later.

## Layering

```
config ──► providers.Manager ──► providers.Registry ──► Provider adapter
                   │                                      │
                   └──────────► models.Registry ◄─────────┘
                                      │
                                      └── API management endpoints
```

- `internal/providers` owns adapters, normalized request/response types, error
  normalization and provider lifecycle.
- `internal/models` owns the provider-independent model registry. It does not
  import `internal/providers`.
- `providers.Manager` connects configuration to both registries. The API talks
  to the manager, never to an adapter directly.
- Configuration is loaded once at startup. Runtime provider registration is not
  implemented; providers and model metadata are operator-configured.

## Provider interface

```go
type Provider interface {
    ID() string
    Kind() string
    Info() ProviderInfo
    Capabilities() ProviderCapabilities
    ListModels(ctx context.Context) ([]ModelInfo, error)
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    ChatStream(ctx context.Context, req ChatRequest) (Stream, error)
    Health(ctx context.Context) error
    Init(ctx context.Context) error
    Close(ctx context.Context) error
}

type Stream interface {
    Recv(ctx context.Context) (StreamEvent, error) // io.EOF after one StreamDone
    Close() error
}
```

Implementation rules:

- Adapters are safe for concurrent use and accept caller contexts on every
  network operation.
- `Init` runs once at registration and must not start background polling.
  `Close` runs once on removal or shutdown.
- Streaming is a pull iterator. Adapters do not start a background goroutine,
  so cancellation and back-pressure stay with the caller.
- A stream emits text/tool deltas, exactly one `done` event, then `io.EOF`.
  Provider failures are returned as `*providers.Error`, never disguised as
  events.
- Usage fields are pointers. `nil` usage means "not reported"; a zero pointer is
  a real zero. LoveCode never fabricates token counts.
- `ChatRequest.ProviderOptions` is a bounded passthrough for provider-specific
  fields. Reserved envelope keys (`model`, `messages`, `stream`, and normalized
  generation fields) cannot be overridden.

## Registry semantics

`providers.Registry`:

- IDs match `^[a-z0-9][a-z0-9._-]{0,63}$`; duplicate registration fails.
- Registration calls `Init` and exposes a status snapshot. `Get` works even for
  a disabled provider; generation uses `Acquire` and refuses disabled providers.
- Every `Chat`/`ChatStream` holds an in-flight lease. Remove and disable are
  refused with `ErrProviderBusy` while a lease is active. A stream releases its
  lease on EOF, error or `Close`.
- Shutdown closes providers in reverse registration order.
- Enabling is always allowed; disabling is subject to the busy check.

`models.Registry`:

- A model has a stable LoveCode `ID` (for example `openai:gpt-4.1`) and a
  separate `ProviderModelID` (the exact identifier sent upstream). Display names
  are not identities.
- Capabilities are tri-state: `unknown`, `supported`, `unsupported`. The zero
  value normalizes to `unknown`; unknown is never treated as supported. An empty
  modality list also means unknown.
- Availability is separate from the operator `enabled` flag. `Usable()` requires
  enabled and not known-unavailable.
- Records can carry context/output limits, tags, pricing and declared rate
  metadata. All are optional.
- Listing supports filters by provider, tag, enabled state and availability.
- `Add`, `Update`, `Upsert`, `Remove`, `SetEnabled`, `SetAvailability`,
  `RemoveByProvider`, `Available` and `Count` are concurrency-safe.

Configured models are registered from `[[providers.models]]`. Derived IDs use
`<provider-id>:<provider-model-id>` when the resulting ID is valid; unusual
upstream IDs are sanitized and receive a short hash suffix. Discovery never
deletes models and never overwrites operator tags, explicit capabilities or the
enabled flag.

## Adapters

### OpenAI-compatible (`type = "openai"`)

Works with OpenAI and compatible services. The endpoint is a base URL; paths
default to `/chat/completions`, `/models` and `/models` for health. Supported
mapping includes system/user/assistant/tool messages, text and image parts, tool
calls, tool choice, JSON object/schema output, `max_tokens` or
`max_completion_tokens`, SSE streaming, usage and finish reasons.

Model discovery reports capabilities as `unknown` rather than guessing what an
arbitrary compatible endpoint supports. Configure capabilities per model when
they matter.

Relevant `settings`:

| Key | Meaning |
|---|---|
| `chat_path`, `models_path`, `health_path` | Override API paths; each must start with `/`. |
| `skip_health_check` | Mark health checks unsupported and make `Health` return success. |
| `auth_header`, `auth_scheme` | Default `Authorization` / `Bearer`; use `auth_scheme = "none"` for no scheme. |
| `max_tokens_field` | `max_tokens` (default) or `max_completion_tokens`. |
| `stream_usage` | Send `stream_options.include_usage`; default true. |
| `organization`, `project` | Optional OpenAI headers. |

### Ollama (`type = "ollama"`)

Optional local adapter. It never downloads models and defaults to
`http://127.0.0.1:11434`. Uses `/api/chat`, `/api/tags` and `/api/version`;
streaming is NDJSON. Supports text, base64 data-URL images, tools, JSON output,
`num_predict`, stop sequences, seed, sampling options and `keep_alive`.
Model tags are reported with unknown capabilities; declare known capability
values in configuration when routing depends on them.

Relevant `settings`: `chat_path`, `tags_path`, `version_path`, `keep_alive`.

### Mock (`type = "mock"`)

Deterministic, in-process, no network. Mandatory contract-test provider and the
default choice for development without credentials. Default model is `mock-1`
with a deterministic reply. Settings include `reply`, `chunk_size`,
`chunk_delay`, `latency`, `finish_reason`, token counts, simulated failures
(`fail_kind`, `fail_message`, `fail_http_status`, `fail_provider_code`,
`fail_retry_after`), `unavailable`, `simulate_timeout`,
`stream_error_after`/`stream_fail_kind` and `stream_fail_message`.

## Configuration examples

All credentials are referenced by environment variable name. LoveCode validates
that the variable exists and is non-empty at startup; it never writes the value
to configuration, logs or API responses.

### One OpenAI-compatible provider

```toml
[[providers]]
id = "openai"
type = "openai"
display_name = "OpenAI"
endpoint = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
discover_models = true

[[providers.models]]
id = "openai:gpt-4.1"
provider_model_id = "gpt-4.1"
display_name = "GPT-4.1"
context_window = 1047576
input_modalities = ["text", "image"]
output_modalities = ["text"]
streaming = true
tool_calls = true
structured_output = true
tags = ["cloud", "general"]
```

### Two different OpenAI-compatible providers

```toml
[[providers]]
id = "openai"
type = "openai"
endpoint = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"

[[providers.models]]
id = "openai:gpt-4.1-mini"
provider_model_id = "gpt-4.1-mini"
streaming = true
tool_calls = true

[[providers]]
id = "together"
type = "openai"
display_name = "Together AI"
endpoint = "https://api.together.xyz/v1"
api_key_env = "TOGETHER_API_KEY"

[providers.default_headers]
X-Tenant = "personal"

[[providers.models]]
id = "together:llama-3.3-70b"
provider_model_id = "meta-llama/Llama-3.3-70B-Instruct-Turbo"
streaming = true
tool_calls = true
```

### Ollama

```toml
[[providers]]
id = "ollama"
type = "ollama"
display_name = "Local Ollama"
endpoint = "http://127.0.0.1:11434"
discover_models = true

[providers.settings]
keep_alive = "5m"

[[providers.models]]
provider_model_id = "llama3.2:latest"
display_name = "Llama 3.2"
streaming = true
tool_calls = true

[[providers.models]]
provider_model_id = "qwen2.5-coder:7b"
display_name = "Qwen 2.5 Coder"
streaming = true
```

### Mock

```toml
[[providers]]
id = "mock"
type = "mock"
display_name = "Development Mock"

[providers.settings]
reply = "Hello from the deterministic mock provider."
finish_reason = "stop"
prompt_tokens = 12
completion_tokens = 8

[[providers.models]]
id = "mock:chat"
provider_model_id = "mock-1"
display_name = "Mock Chat"
streaming = true
tool_calls = true
structured_output = true
tags = ["offline", "deterministic"]
```

## Secrets and failure handling

- Only `api_key_env` is stored. Adapters redact the resolved key from provider
  error messages, and endpoints in status responses have query strings,
  userinfo and fragments removed.
- A failed optional provider is recorded in `ProviderStatus.last_error` with a
  normalized kind. Other providers still start.
- A failed `required = true` provider, duplicate provider identity or duplicate
  model identity fails startup. Startup failures are written to the audit log.
- Provider errors have stable kinds: authentication, invalid configuration,
  invalid request, unsupported feature, model not found, provider unavailable,
  rate limited, quota exceeded, timeout, connection failure, canceled, internal
  provider error and malformed response. HTTP status, provider code, request ID
  and retry-after are preserved when available.

## Management API

Provider and model management is documented in [API.md](API.md). Read endpoints
require the `read` scope; mutations require `admin`. Mutations are audited as
`provider.enabled`, `provider.disabled`, `provider.test`,
`provider.refresh_models`, `model.enabled` and `model.disabled`.

## Tests

See [TESTING.md](TESTING.md). Adapter tests use `httptest` and the deterministic
mock; no external credentials or internet access are required.

## Limitations

- Providers are loaded only from configuration at startup. There is no runtime
  adapter creation, credential rotation or keyring integration yet.
- No automatic chat routing, failover, circuit breaker or usage ledger (Phase 3).
- No `/v1/chat/completions` endpoint yet; the normalized provider API is
  internal and exercised by tests.
- OpenAI-compatible discovery reports unknown model capabilities by design.
- Ollama supports only local base64 data URLs for images.
- Pricing/rate fields are metadata only; nothing enforces rate limits yet.
- Provider status is in-memory and rebuilt at startup; it does not show
  historical availability.
