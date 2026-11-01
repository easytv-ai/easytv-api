# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Release pipeline: GoReleaser binaries (Linux / macOS / Windows, amd64 / arm64) on `v*` tags and multi-arch images on GHCR (`ghcr.io/easytv-ai/easytv-api`).
- `govulncheck` in CI; `docker compose --profile full` starts studio and admin console alongside the API.
- `SUPPORT.md`, release-notes categories (`.github/release.yml`), label sync, stale bot, Conventional Commits PR-title check and OpenSSF Scorecard workflow.
- Open-source governance: AGPL-3.0 license, contributing guide, code of
  conduct, security policy, issue and pull request templates, Dependabot and
  GitHub Actions CI.
- Added `GET /healthz` liveness endpoint for container orchestrators.
- Added `Dockerfile` and `docker-compose.yml` (API + MySQL + Redis) for one-command self-hosting.

### Changed

- Go toolchain 1.27.3 and `golang.org/x/net` 0.60 / `quic-go` 0.59.1: fixes the 12 reachable vulnerabilities govulncheck reported against 1.27.1.
- Invite links fall back to the `ServerAddress` setting instead of the hosted easytv.ai domain; Qiniu CDN domain no longer has a hosted default.
- README rewritten for self-hosters; team assignment tables removed from `API.md`.
- Build artifacts (`golang-report/`) removed from git; `docs/`, `scripts/`, `tools/` are no longer ignored.

[Unreleased]: https://github.com/easytv-ai/easytv-api/commits/main
