# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
