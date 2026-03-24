# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v1.5.14] - 2026-03-24

### Fixed
- Reduce max tool rounds from 20 to 8 to prevent runaway loops
- System prompt tells Claude to stop and analyze after 2-3 tool calls instead of endlessly fetching more data
- System prompt tells Claude not to repeatedly fetch logs for broken/pending servers

## [v1.5.13] - 2026-03-24

### Added
- Visible Stop button replaces Send button during streaming — click to cancel any in-progress chat, including rate limit retries

## [v1.5.12] - 2026-03-24

### Fixed
- Truncate tool results to 8KB before storing in conversation history — prevents 50KB log dumps from spiking token usage and causing rate limit loops

## [v1.5.11] - 2026-03-24

### Changed
- System prompt prohibits `watch_job_health` in interactive sessions — use instant `get_minecraft_server_status` instead to avoid blocking SSE connections for minutes
- System prompt includes server startup time guidance (1-2 min vanilla, 3-5 min modpacks) so Claude tells users when to check back

## [v1.5.10] - 2026-03-24

### Added
- System prompt identifies mc-router as infrastructure (itzg/mc-router reverse proxy), not a Minecraft server — prevents RCON calls, backup attempts, and inclusion in server listings

## [v1.5.9] - 2026-03-24

### Added
- System prompt HCL editing rules: treat existing specs as authoritative, make only requested changes, preserve datacenter/node_pool/volumes/env vars, always show HCL for review before submitting

## [v1.5.8] - 2026-03-24

### Added
- Debug log captures full user messages and assistant responses for troubleshooting context

### Fixed
- Server-side enforcement of one-tool-at-a-time: when Claude sends multiple parallel tool calls, only the first is executed — prevents connection timeouts from sequential gateway call storms

## [v1.5.7] - 2026-03-24

### Added
- Admin-only debug log panel: toggle in sidebar, floating panel with timestamped event log, copy-all button
- Server-side debug SSE events: API round metadata (token counts, stop reasons), tool execution timing, context trimming stats
- Debug events gated by both admin role AND debug checkbox — never sent to non-admin users

## [v1.5.6] - 2026-03-24

### Fixed
- Add paragraph break between pre-tool text and post-tool response to fix missing spaces when text resumes after tool execution

## [v1.5.5] - 2026-03-24

### Changed
- Tool calls now render as a compact activity bar instead of expandable blocks — eliminates layout jumping during tool execution
- Disable `breaks: true` in marked.js to fix missing spaces after periods in streamed text
- Visual divider separates tool activity from response text for clearer distinction

## [v1.5.4] - 2026-03-24

### Fixed
- `resolveConversation` now writes proper HTTP error responses (404/403) instead of silently returning 200 on failure
- Add missing `GET /api/auth/me` route to CLAUDE.md API routes table

## [v1.5.3] - 2026-03-24

### Added
- Admin panel: editable token limit per user (inline number input in users table)

### Changed
- Default per-conversation token limit bumped from 200k to 500k (schema, admin fallback, migration for existing users)

## [v1.5.2] - 2026-03-24

### Fixed
- Message input no longer loses focus after sending — removed disabled attribute from textarea during streaming

## [v1.5.1] - 2026-03-24

### Changed
- Context trimming now drops old messages entirely instead of truncating tool results — keeps conversations within API context limits indefinitely
- Removed `TOOL_RESULT_MAX_LEN` config (no longer needed)

## [v1.5.0] - 2026-03-24

### Added
- System prompt enforces one-tool-at-a-time step-by-step workflow: call one tool, report to user, wait for confirmation before next step — eliminates parallel tool bursts that caused rate limiting
- Rate limit retry with exponential backoff: on Anthropic API 429 responses, retries up to 3 times with `Retry-After` header support
- `rate_limit` SSE event type: frontend shows warning banner when rate limited, clears on retry success
- Context window trimming: sliding window keeps first message + last N messages, truncates old tool results to reduce token usage
- `CONTEXT_WINDOW_SIZE` config (default 20): number of recent messages to keep in full
- `TOOL_RESULT_MAX_LEN` config (default 500): max chars for old tool result content before truncation

### Changed
- Rate limit exhaustion returns distinct error message instead of generic "Failed to get response from Claude"
- Long rate limit waits (>30s) now close the SSE stream cleanly and send a `rate_limit_pause` event; frontend auto-retries after countdown — no user intervention needed
- Continuation requests after rate limit pause add a "Please continue where you left off." user message to satisfy API requirement that conversations end with a user message
- SSE connection deadline (90s): long tool-use conversations now auto-pause before the proxy kills the connection, frontend auto-continues with a new request

### Fixed
- Rate limit retry wait sends periodic countdown events every 10 seconds to keep SSE connection alive during short waits (≤30s)
- Long Retry-After values (60-90s) no longer kill the SSE connection — the stream closes cleanly and frontend handles the wait client-side
- Rate limit auto-retry no longer fails with "conversation must end with a user message" — continuation adds a user message instead of sending an empty request
- Message input retains focus after sending a message

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
