# homelab-chatbot

Task-focused web UI chatbot for the Homelab AI Platform. Lets kids and operators manage Minecraft servers via natural-language chat.

Part of the [homelab-ai](https://github.com/lobo235/homelab-ai) platform.

## Quick Start

```bash
cp .env.example .env
# Fill in required values
go run ./cmd/server
```

## Architecture

```
Browser → SSE → homelab-chatbot (Go HTTP server)
                  ├─ Anthropic API (Claude)
                  └─ homelab-mcp-server (stdio subprocess)
```

See [CLAUDE.md](CLAUDE.md) for full project documentation.

## First Run — Admin Bootstrap

On first startup, the app creates an `admin` user with the password from the `ADMIN_PASSWORD` environment variable. This only happens once — if the `admin` user already exists in the SQLite database, the password is **not** updated.

If you need to reset the admin password, delete the SQLite database file (`$DATA_DIR/homelab-chatbot.db`) and restart the app. This will recreate all tables and bootstrap the admin user with the current `ADMIN_PASSWORD` value.

## Build

```bash
make build
make test
make lint
```

## Docker

```bash
docker build -t homelab-chatbot .
docker build --build-arg VERSION=v1.2.3 -t homelab-chatbot .
```

## License

Private — part of the Homelab AI Platform.
