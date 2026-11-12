# LoveCode

Self-hosted, frontend-independent AI backend (Go). **Status: Phase 4 Agent Engine, Multi-Agent Orchestration & Team Execution implemented and verified.**

- [Architecture](docs/ARCHITECTURE.md)
- [Provider and model guide](docs/PROVIDERS.md)
- [Routing guide](docs/ROUTING.md)
- [Agents guide](docs/AGENTS.md)
- [Teams guide](docs/TEAMS.md)
- [Execution and scheduling guide](docs/EXECUTION.md)
- [HTTP API](docs/API.md)
- [Testing](docs/TESTING.md)
- [Implementation status](docs/STATUS.md)

## Build and verify (Arch Linux / CachyOS)

```sh
sudo pacman -S --needed go git
cd love-code
go mod tidy                 # needs network once: resolves go.sum
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go build ./...
make build                  # -> bin/lovecode (CGO_ENABLED=0)
```

The provider test suite is offline: it uses `httptest` servers and the
deterministic mock adapter, so no API keys or hosted services are needed.

## Run

```sh
./bin/lovecode -config configs/lovecode.example.toml
# first start writes an owner token (mode 0600) to $XDG_DATA_HOME/lovecode/owner.token
TOKEN=$(cat ~/.local/share/lovecode/owner.token)
curl -s localhost:7878/v1/health
curl -s -H "Authorization: Bearer $TOKEN" localhost:7878/v1/status
curl -s -H "Authorization: Bearer $TOKEN" localhost:7878/v1/providers
curl -s -H "Authorization: Bearer $TOKEN" localhost:7878/v1/models
curl -s -H "Authorization: Bearer $TOKEN" localhost:7878/v1/audit/verify
./bin/lovecode -backup /tmp/lovecode-backup.db     # consistent DB backup
```

Default bind is `127.0.0.1:7878` (local-only mode). LAN/remote require explicit
config. Provider credentials are supplied through environment variables named
by `api_key_env`; the example config contains commented OpenAI-compatible,
Ollama and mock provider blocks. See [docs/PROVIDERS.md](docs/PROVIDERS.md).

Provider and model management endpoints require `read` or `admin` scope:

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  localhost:7878/v1/providers/ollama/models/refresh
curl -s -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"enabled":false}' localhost:7878/v1/models/ollama:llama3.2:latest
```

Phase 2 exposes management endpoints only; the normalized chat interface is
currently internal and is used by tests. The OpenAI-compatible chat endpoint is
a later phase.

## Measure

```sh
/usr/bin/time -v ./bin/lovecode ... & sleep 30; ps -o rss,pcpu -p $(pgrep lovecode)
```

Resource measurements are not claimed until recorded in `docs/STATUS.md`.

## Unified generation and routing

Phase 3 adds `POST /v1/chat/completions` plus persistent routing profiles, model groups, circuit breakers, usage tracking, and status endpoints. See [docs/ROUTING.md](docs/ROUTING.md) for request examples, policies, failover behavior and operational details.

## Agent Engine & Teams

Phase 4 adds single-agent execution, multi-agent teams (sequential, parallel, dependency-based DAGs), subagent delegation, ephemeral dynamic agents with privilege inheritance rules, context policies, result aggregation and conflict handling, bounded concurrency scheduling, and execution persistence (`/v1/agents`, `/v1/teams`, `/v1/agent-runs`, `/v1/agent-runs/{id}/tasks`). See [docs/AGENTS.md](docs/AGENTS.md), [docs/TEAMS.md](docs/TEAMS.md), and [docs/EXECUTION.md](docs/EXECUTION.md).
