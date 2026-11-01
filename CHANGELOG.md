# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Open-source governance: AGPL-3.0 license, contributing guide, code of
  conduct, security policy, issue and pull request templates, Dependabot and
  GitHub Actions CI.
- Added `GET /healthz` liveness endpoint for container orchestrators.
- Added `Dockerfile` and `docker-compose.yml` (API + MySQL + Redis) for one-command self-hosting.

### Changed

- Invite links fall back to the `ServerAddress` setting instead of the hosted easytv.ai domain; Qiniu CDN domain no longer has a hosted default.
- README rewritten for self-hosters; team assignment tables removed from `API.md`.
- Build artifacts (`golang-report/`) removed from git; `docs/`, `scripts/`, `tools/` are no longer ignored.

[Unreleased]: https://github.com/easytv-ai/easytv-api/commits/main
