# homelab-chatbot

Task-focused web UI chatbot for Minecraft server management via natural-language chat.
Part of the [homelab-ai](https://github.com/lobo235/homelab-ai) platform.

## Module

`github.com/lobo235/homelab-chatbot`

## Quick Start

```bash
cp .env.example .env
# Fill in required values
go run ./cmd/server
```

## Build, Test, Run

> Go is installed at `~/bin/go/bin/go` (also on `$PATH` via `.bashrc`).

```bash
# Build (requires CGO for SQLite)
make build

# Run tests
make test

# Run tests with verbose output
CGO_ENABLED=1 go test -v ./...

# Run linter
make lint

# Coverage report (opens in browser)
make cover

# Run the server (requires .env or env vars)
make run

# Build binary
CGO_ENABLED=1 go build -o homelab-chatbot ./cmd/server
```

## Project Layout

```
homelab-chatbot/
├── Dockerfile
├── Makefile
├── go.mod / go.sum
├── .env.example              # dev template — never commit real values
├── .gitignore
├── .golangci.yml             # strict linter config
├── .githooks/pre-commit      # runs lint + tests; activate with `make hooks`
├── CLAUDE.md                 # this file
├── README.md
├── CHANGELOG.md
├── cmd/
│   └── server/
│       └── main.go           # entry point — HTTP server with SSE streaming
├── deploy/
│   └── homelab-chatbot.hcl   # Nomad job spec (placeholders only)
└── internal/
    ├── config/
    │   ├── config.go          # ENV var loading & validation
    │   └── config_test.go
    ├── database/
    │   ├── database.go        # SQLite connection, migrations, queries
    │   └── database_test.go
    ├── auth/
    │   ├── auth.go            # Session management, bcrypt, login/logout
    │   └── auth_test.go
    ├── gateway/
    │   └── client.go          # HTTP client for gateway services (health, job stop)
    ├── mcp/
    │   └── mcp.go             # MCP subprocess launcher (stdio)
    ├── chat/
    │   └── chat.go            # Anthropic API client, SSE streaming, tool integration
    ├── api/
    │   ├── server.go          # HTTP server, route registration, middleware
    │   ├── handlers.go        # User-facing route handlers
    │   └── errors.go          # writeError / writeJSON helpers
    ├── notify/
    │   ├── hub.go             # Per-user SSE notification hub
    │   ├── hub_test.go
    │   ├── poller.go          # Background poller for async op status
    │   └── poller_test.go
    ├── admin/
    │   └── handlers.go        # Admin route handlers
    └── frontend/
        └── frontend.go        # Embedded frontend assets (HTML/CSS/JS)
```

## Configuration

All config via ENV vars. Loaded from `.env` in development (via `godotenv`; missing file silently ignored). In production, secrets are injected by Nomad Vault Workload Identity.

| Var | Required | Default | Purpose |
|-----|----------|---------|---------|
| `ANTHROPIC_API_KEY` | yes | — | Anthropic API key |
| `CLAUDE_MODEL` | no | `claude-sonnet-4-6` | Claude model for initial reasoning |
| `CLAUDE_HAIKU_MODEL` | no | `claude-haiku-4-5-20251001` | Claude model for tool-execution rounds (faster, cheaper) |
| `MCP_SERVER_CMD` | yes | — | Command to launch homelab-mcp-server subprocess |
| `ADMIN_PASSWORD` | yes | — | Bootstrap password for admin account (first run) |
| `SESSION_SECRET` | yes | — | Secret for signing session tokens |
| `DATA_DIR` | no | `/data` | Directory for SQLite DB |
| `PORT` | no | `8080` | Listen port |
| `LOG_LEVEL` | no | `info` | Verbosity: `debug`, `info`, `warn`, `error` |
| `CONTEXT_WINDOW_SIZE` | no | `20` | Recent messages to keep in sliding window (older messages dropped) |
| `NOMAD_GATEWAY_URL` | no | — | Nomad gateway base URL (for admin server stop) |
| `NOMAD_GATEWAY_KEY` | no | — | Nomad gateway API key |
| `ADGUARD_GATEWAY_URL` | no | — | AdGuard gateway base URL |
| `ADGUARD_GATEWAY_KEY` | no | — | AdGuard gateway API key |
| `CF_GATEWAY_URL` | no | — | Cloudflare gateway base URL |
| `CF_GATEWAY_KEY` | no | — | Cloudflare gateway API key |
| `MINECRAFT_GATEWAY_URL` | no | — | Minecraft gateway base URL |
| `MINECRAFT_GATEWAY_KEY` | no | — | Minecraft gateway API key |
| `CURSEFORGE_GATEWAY_URL` | no | — | CurseForge gateway base URL |
| `CURSEFORGE_GATEWAY_KEY` | no | — | CurseForge gateway API key |
| `VAULT_GATEWAY_URL` | no | — | Vault gateway base URL |
| `VAULT_GATEWAY_KEY` | no | — | Vault gateway API key |
| `NOMAD_DEFAULT_DATACENTER` | no | — | Forwarded to MCP subprocess for job placement |
| `NOMAD_DEFAULT_NODE_POOL` | no | `default` | Forwarded to MCP subprocess for node pool selection |
| `NFS_BASE_PATH` | no | — | Forwarded to MCP subprocess for Minecraft server storage |
| `MC_PUBLIC_DOMAIN` | no | — | Forwarded to MCP subprocess for server connection addresses |
| `CF_ZONE_NAME` | no | — | Forwarded to MCP subprocess for Cloudflare DNS |
| `ARTIFACT_ALLOWLIST` | no | — | Forwarded to MCP subprocess; comma-separated allowed artifact domains |
| `ITZG_DOCS_REFRESH_INTERVAL` | no | `24h` | Forwarded to MCP subprocess; itzg docs cache refresh interval |

## Architecture

```
cmd/server/main.go               — entry point, wires deps, starts HTTP server
internal/config/config.go         — ENV-based config with validation
internal/database/database.go     — SQLite connection, schema, queries
internal/auth/auth.go             — Session/user management, bcrypt, middleware
internal/gateway/client.go         — HTTP client for gateway health checks & admin operations
internal/mcp/mcp.go               — MCP subprocess launcher (stdio transport)
internal/chat/chat.go             — Anthropic API client with MCP tool support
internal/api/server.go            — HTTP server, route registration
internal/api/handlers.go          — User-facing handlers (chat, sessions, servers)
internal/api/errors.go            — JSON error/response helpers
internal/notify/hub.go            — Per-user SSE notification hub for async ops
internal/notify/poller.go         — Background poller checking async op status via MCP
internal/admin/handlers.go        — Admin-only handlers (users, servers, usage)
internal/frontend/frontend.go     — Embedded HTML/CSS/JS assets
```

## API Routes

### User Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/` | None | Serve frontend (SPA handles auth client-side) |
| POST | `/api/chat` | Session | Send message, stream SSE response |
| GET | `/api/sessions` | Session | List user's sessions |
| GET | `/api/sessions/{id}` | Session | Get session with messages |
| DELETE | `/api/sessions/{id}` | Session | Delete session |
| GET | `/api/servers` | Session | List user's servers |
| GET | `/api/notifications` | Session | Persistent SSE stream for async op notifications |
| POST | `/api/auth/login` | None | Login, set session cookie |
| POST | `/api/auth/logout` | Session | Logout, clear cookie |
| GET | `/api/auth/me` | Session | Get current user info |
| GET | `/health` | None | Health check |
| GET | `/help` | None | Static help page |

### Admin Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/admin/users` | Admin | List users |
| POST | `/admin/users` | Admin | Create user |
| PUT | `/admin/users/{id}` | Admin | Update/deactivate user |
| DELETE | `/admin/users/{id}` | Admin | Delete user |
| GET | `/admin/servers` | Admin | All running jobs with owner info |
| POST | `/admin/servers/{name}/stop` | Admin | Force-stop a server |
| GET | `/admin/gateways` | Admin | Gateway health status |
| GET | `/admin/usage` | Admin | Token usage summary |
| PUT | `/admin/users/{id}/limits` | Admin | Set per-user limits |
| GET | `/admin/logs` | Admin | Recent error log |
| PUT | `/admin/servers/{name}/ownership` | Admin | Reassign server ownership |
| DELETE | `/admin/servers/{name}/ownership` | Admin | Remove server ownership |
| GET | `/admin/modpack-kb` | Admin | List all modpack KB entries |
| GET | `/admin/modpack-kb/{slug}` | Admin | Get modpack KB entry |
| PUT | `/admin/modpack-kb/{slug}` | Admin | Save/update modpack KB entry |
| DELETE | `/admin/modpack-kb/{slug}` | Admin | Delete modpack KB entry |
| PATCH | `/admin/modpack-kb/{slug}/review` | Admin | Toggle needs_review flag |

### SSE Event Types

```json
{"type":"token","content":"..."}
{"type":"tool_start","name":"...","message":"..."}
{"type":"tool_done","name":"...","status":"done|failed"}
{"type":"rate_limit","message":"Rate limited by API. Retrying in N seconds..."}
{"type":"rate_limit_pause","message":"...","retry_after":88}
{"type":"debug","data":{...}}
{"type":"done"}
{"type":"error","message":"..."}
```

`rate_limit` is sent during short waits (≤30s) while the server retries. `rate_limit_pause` is sent for long waits — the server closes the stream and the frontend auto-retries by sending a continuation request (empty message with `conversation_id`) after the countdown.

### Notification SSE Event Types (`GET /api/notifications`)

Persistent SSE connection for async operation status updates. Events:

```json
{"type":"async_started","op_id":"...","op_type":"download","server_name":"mc-test","description":"..."}
{"type":"async_progress","op_id":"...","elapsed_seconds":45,"status":"pending"}
{"type":"async_complete","op_id":"...","status":"done","message":"...","conversation_id":123}
{"type":"async_failed","op_id":"...","status":"failed","message":"...","conversation_id":123}
{"type":"auto_continue","op_id":"...","conversation_id":123,"message":"[System] ..."}
```

A background poller checks pending async operations (downloads, backups) every 10 seconds. When an operation completes, the hub sends `async_complete`/`async_failed` to the user's notification stream and triggers auto-continuation (sends `auto_continue` so the frontend auto-sends a continuation to Claude). Max 3 auto-continuations per conversation per hour to prevent token runaway.

## Testing Approach

Tests live alongside their packages in `*_test.go` files.

Key patterns:
- Config tests cover all required fields, defaults, and validation
- Database tests use in-memory SQLite (`:memory:`)
- Auth tests cover bcrypt hashing, session lifecycle, middleware
- Handler tests use `httptest.NewServer` with mocked dependencies
- Table-driven tests for input validation

## Coding Conventions

- stdlib `net/http` for routing (Go 1.22+ pattern matching)
- SQLite via `modernc.org/sqlite` (pure Go, no CGO required at runtime)
- Alpine.js + marked.js frontend loaded from CDN with integrity hashes
- SSE streaming via `http.Flusher`
- All errors use `writeError(w, status, code, message)` with machine-readable `code`
- Structured JSON logging via `log/slog`
- Version logged on startup
- Never log secret values (API keys, passwords, session tokens)
- Session tokens: 32-byte `crypto/rand`, hex-encoded, stored hashed (SHA-256)
- bcrypt work factor: 12
- **UI-schema sync:** When a data schema changes (e.g. `ModpackKnowledge`, `VersionKnowledge`, SSE event types, API response shapes), the admin panel and frontend must be updated in the same commit to reflect the new fields. This includes: form inputs for editable fields, read-only displays for machine-generated fields, JS defaults in `newModpack()`/`addVersion()` or equivalent initializers, and table columns in list views. Never add a backend field without checking whether it should be visible in the UI.

## Security Rules

> **Claude must enforce all rules below on every commit and push without exception.**

1. **Never commit secrets:** No `.env`, tokens, API keys, passwords, or credentials of any kind.
2. **Never commit infrastructure identifiers:** No real hostnames, IP addresses, datacenter names, node pool names, Consul service names, Vault paths with real values, Traefik routing rules with real domains, or any value that reveals homelab architecture. Use generic placeholders (`dc1`, `default`, `example.com`, `your-node-pool`, `your-service`).
3. **Unknown files:** If `git status` shows a file Claude didn't create, ask the operator before staging it.
4. **Pre-commit checks (must all pass before committing):**
   - `go test ./...` — all tests must pass
   - `golangci-lint run` — no lint errors
5. **Docs accuracy:** Review all changed `.md` files before committing — documentation must reflect the current state of the code in the same commit.
6. **Version bump:** Before any `git commit`, review the changes and determine the appropriate SemVer bump (MAJOR/MINOR/PATCH). Present the rationale and proposed new version to the operator and wait for confirmation before tagging or referencing the new version.
7. **Push confirmation:** Before any `git push`, show the operator a summary of what will be pushed (commits, branch, remote) and wait for explicit confirmation.
8. **Commit messages:** Must not contain real hostnames, IPs, or infrastructure identifiers.

## Versioning & Releases

SemVer (`MAJOR.MINOR.PATCH`). Git tags are the source of truth.

```bash
git tag v1.2.3 && git push origin v1.2.3
```

This triggers the Docker workflow which publishes:
- `ghcr.io/lobo235/homelab-chatbot:v1.2.3`
- `ghcr.io/lobo235/homelab-chatbot:v1.2`
- `ghcr.io/lobo235/homelab-chatbot:latest`
- `ghcr.io/lobo235/homelab-chatbot:<short-sha>`

Version is embedded at build time: `-ldflags "-X main.version=v1.2.3"` — defaults to `"dev"` for local builds. Logged on startup.

## Docker

```bash
# Build (version defaults to "dev")
docker build -t homelab-chatbot .

# Build with explicit version
docker build --build-arg VERSION=v1.2.3 -t homelab-chatbot .
```

Multi-stage build: `golang:1.26-alpine` -> `alpine:3.21`. Includes MCP server binary via `COPY --from=ghcr.io/lobo235/homelab-mcp-server:latest`.

## Known Limitations

- **CGO required for SQLite:** Build requires `CGO_ENABLED=1` and a C compiler. The Docker multi-stage build handles this.
- **MCP subprocess:** The MCP server runs as a child process — if the chatbot crashes, the MCP server is also terminated.
- **Single-instance:** SQLite does not support concurrent writes from multiple processes. Run one instance only.
