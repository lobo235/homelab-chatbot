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

## Build

```bash
make build
make test
make lint
```

## Docker

```bash
docker build -t homelab-chatbot .
docker build --build-arg VERSION=v1.0.0 -t homelab-chatbot .
```

## License

Private — part of the Homelab AI Platform.
