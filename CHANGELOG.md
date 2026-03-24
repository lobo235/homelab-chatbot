# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Rate limit retry with exponential backoff: on Anthropic API 429 responses, retries up to 3 times with `Retry-After` header support
- `rate_limit` SSE event type: frontend shows warning banner when rate limited, clears on retry success
- Context window trimming: sliding window keeps first message + last N messages, truncates old tool results to reduce token usage
- `CONTEXT_WINDOW_SIZE` config (default 20): number of recent messages to keep in full
- `TOOL_RESULT_MAX_LEN` config (default 500): max chars for old tool result content before truncation

### Changed
- Rate limit exhaustion returns distinct error message instead of generic "Failed to get response from Claude"

### Fixed
- Rate limit retry wait sends periodic countdown events every 10 seconds to keep SSE connection alive — prevents reverse proxy (Traefik) from killing idle connections during long Retry-After waits

## [v1.2.0] - 2026-03-23

### Added
- Comprehensive markdown content styling: tables, headings, lists, blockquotes, code blocks all render with proper formatting
- MCP subprocess env vars documented in CLAUDE.md configuration table

### Changed
- Redesigned UI with darker theme inspired by GitHub/Claude Code aesthetic
- Improved message bubble styling with better line-height, padding, and font sizing
- Enter key now reliably sends messages across all browsers (Shift+Enter for newlines)
- Docker build workflow resolves version from git tags for non-tag builds
- Docker build workflow accepts `workflow_dispatch` and `repository_dispatch` triggers for manual and cross-repo rebuilds

## [v1.1.1] - 2026-03-23

### Fixed
- Use HTTPS for gateway URLs in deploy spec to match Traefik TLS termination
- Harden system prompt to prevent Claude from exposing internal hostnames, IPs, and filesystem paths in error responses

## [v1.1.0] - 2026-03-23

### Added
- MCP server config env vars in deploy spec (datacenter, node pool, NFS path, DNS config)
- Agentic tool execution loop: Claude can now call MCP tools and receive results in a multi-turn conversation (up to 20 rounds per request)
- Admin bootstrap process documented in README

### Changed
- Removed `MC_PUBLIC_IP` from deploy spec (now optional upstream)

## [v1.0.1] - 2026-03-23

### Fixed
- Correct SRI integrity hashes for Alpine.js, marked.js, and DOMPurify CDN scripts — all three had invalid hashes, preventing the frontend from functioning
- Add `[x-cloak]` CSS rule to prevent flash of unstyled content before Alpine.js initializes
- Add `x-cloak` to admin overlay to prevent it from covering the page on load
- Load admin panel data (`loadAdminData`) when the panel is opened
- Prevent 1Password autofill on admin Create User form (`autocomplete="off"`, `data-1p-ignore`)

## [v1.0.0] - 2026-03-23

### Added

- Project scaffold: Go module, directory layout, Makefile, Dockerfile, CI config
- Config loading with validation for all required environment variables
- Content-Security-Policy header on all responses
- `POST /admin/servers/{name}/stop` endpoint to force-stop a Nomad job via nomad-gateway
- `GET /admin/gateways` endpoint to check health of all configured gateway services
- Gateway HTTP client (`internal/gateway/`) for admin operations against gateway services
- Gateway URL/key config loading from environment variables (same vars passed to MCP subprocess)

### Fixed

- Remove DOMPurify `ADD_ATTR: ['onclick']` that allowed XSS via inline event handlers
- Attach copy-button click handlers via event delegation instead of inline `onclick`
- Run Docker container as non-root user (`appuser`)
