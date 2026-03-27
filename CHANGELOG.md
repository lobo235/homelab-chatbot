# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Rolling conversation summarization: when messages are trimmed from the context window, Haiku generates a running summary of dropped messages that is injected into the system prompt so important context is preserved across long conversations
- `context_summary` column in conversations table to persist rolling summaries

### Changed
- Bump default `CONTEXT_WINDOW_SIZE` from 20 to 40 to support longer conversations

## [v1.12.2] - 2026-03-26

### Changed
- Switch UI color scheme from GitHub Dark to Gruvbox Dark across index.html and help.html (CSS variables, hardcoded colors, inline SVG icons, favicon)

## [v1.12.1] - 2026-03-26

### Added
- Async tracking for `destroy_minecraft_server` — background poller checks destroy progress via `get_destroy_status`, sends toast notifications on completion
- Toast notifications show real-time destroy progress with auto-dismiss on completion
- Immediate toast notifications on async op start — `async_started` event sent via SSE hub as soon as the DB record is created, no longer waits for first poller tick

## [v1.12.0] - 2026-03-26

### Added
- Admin KB edit form: JVM args, GC strategy, startup method/script, level type, first launch notes, source platform, known client-only mods (comma-separated input)
- Admin KB edit form: per-version server pack notes and known issues fields
- Admin KB edit form: read-only display section for discovery-populated data (additional ports, config overrides, problematic mods, external services)
- Admin KB edit form: per-version mod count and discovery method display
- Toast notification elapsed time updates every second (client-side timer between 10s backend polls)
- CLAUDE.md UI-schema sync rule: schema changes must include corresponding frontend updates

## [v1.11.1] - 2026-03-26

### Fixed
- Pass `ANTHROPIC_API_KEY` to MCP subprocess — web search enrichment was silently skipped in production, producing sparse KB entries

## [v1.11.0] - 2026-03-26

### Added
- Floating toast-style notification bubbles (top-right) replace sidebar async ops panel — animated slide-in, auto-dismiss after 30s, manual close button
- Server health tracking: `provision_minecraft_server` auto-tracked as async op, poller checks health every 10s via `get_minecraft_server_status`, notifies when server is ready (10-min timeout)
- Logo SVG displayed next to "Homelab AI" title in header and login page

### Changed
- System prompt requires `get_modpack_knowledge` lookup before creating modpack servers — prevents blindly copying existing (possibly misconfigured) specs
- System prompt directs Claude to use `trigger_modpack_discovery` for KB population instead of manual CurseForge research

### Fixed
- Toast notifications now wrap long text (up to 3 lines) instead of truncating with ellipsis, with hover tooltip for full text

## [v1.10.0] - 2026-03-26

### Changed
- Increase max_tokens from 8192 to 16384 to use Sonnet's full output capacity (Haiku auto-caps at its own 8192 limit)

### Added
- Modpack discovery integration: `trigger_modpack_discovery` tracked as async operation with real-time status notifications
- Admin KB table: review toggle endpoint (`PATCH /admin/modpack-kb/{slug}/review`), source platform column, confidence flags display, sort needs-review packs first
- Admin KB edit form: needs_review checkbox and confidence flags display
- Async operation notification system: background poller checks download/backup/discovery status every 10s, sends real-time SSE events to the frontend when operations complete
- Persistent SSE endpoint (`GET /api/notifications`) for per-user async operation notifications with 30s keepalive
- Auto-continuation: when an async op completes, the system automatically sends a continuation to Claude so it can proceed with the workflow (max 3 per hour per conversation)
- Active Operations sidebar panel showing running/completed/failed async ops with elapsed time
- MC_PUBLIC_DOMAIN injection into system prompt so Claude always tells kids the correct server connection address
- System prompt guidance: recommend MC 1.20.1 or 1.21.1 for modded vanilla servers (best mod ecosystem)
- System prompt guidance: allow modpack deployment without a dedicated server pack file (use client pack instead)
- System prompt update: Claude is told it will be notified automatically when async ops complete (no more manual polling)
- Database indexes on `async_operations(status)` and `(user_id, status)` for efficient polling
- Database methods: `ListAllPendingOps`, `ListPendingOpsByUser`, `CountRecentAutoContinuations`
- Multi-tenancy Phase 1: operator mode checkbox hidden for non-admin users
- Multi-tenancy Phase 1: Max Servers column in admin users table is now editable (inline number input)
- Multi-tenancy Phase 1: database migration seeds existing Minecraft servers as owned by admin bootstrap account (user ID 1)
- Multi-tenancy Phase 1: system prompt includes user's owned server list so Claude enforces per-user server access; admins get unrestricted access
- Multi-tenancy Phase 3: ownership lifecycle tracking — server creation tools (`provision_minecraft_server`, `create_minecraft_server`) automatically record ownership in the database
- Multi-tenancy Phase 3: ownership removal on destroy — server destruction tools (`destroy_minecraft_server`, `destroy_minecraft_server_by_name`) automatically remove ownership records
- Multi-tenancy Phase 3: max_servers enforcement — non-admin users are blocked from creating servers when they reach their limit; tool returns an error instead of executing
- Multi-tenancy Phase 3: owned server list updated in-flight after creation so subsequent tool calls in the same request reflect the new server

## [v1.6.2] - 2026-03-24

### Added
- RCON safety rules in system prompt: blocked commands explained, confirmation required for destructive commands, safe commands listed

## [v1.6.1] - 2026-03-24

### Added
- Minecraft expertise in system prompt: server directory structure knowledge, modpack deployment workflow, config file locations, and guidance on using file management tools

## [v1.6.0] - 2026-03-24

### Added
- Prompt caching: system prompt and tool definitions cached via `cache_control` to reduce input tokens on every API call
- Haiku model routing: tool-execution rounds (round > 0) use Claude Haiku for faster, cheaper processing with separate rate limits; initial reasoning uses Sonnet
- `CLAUDE_HAIKU_MODEL` config var for configuring the Haiku model
- Debug log shows which model is used per round

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
