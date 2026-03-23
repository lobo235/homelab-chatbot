# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
