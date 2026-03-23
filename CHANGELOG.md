# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Project scaffold: Go module, directory layout, Makefile, Dockerfile, CI config
- Config loading with validation for all required environment variables
- Content-Security-Policy header on all responses

### Fixed

- Remove DOMPurify `ADD_ATTR: ['onclick']` that allowed XSS via inline event handlers
- Attach copy-button click handlers via event delegation instead of inline `onclick`
- Run Docker container as non-root user (`appuser`)
