# Contributing to EasyTV API

Thanks for your interest in EasyTV! This document explains how to propose
changes to `easytv-api`. It applies to code, documentation, translations and
design assets alike.

> 中文读者：欢迎用中文提 Issue 和 PR。本指南的要求同样适用。

## Table of Contents

- [Ground Rules](#ground-rules)
- [Ways to Contribute](#ways-to-contribute)
- [Development Setup](#development-setup)
- [Making Changes](#making-changes)
- [Commit Messages](#commit-messages)
- [Pull Requests](#pull-requests)
- [Reporting Bugs](#reporting-bugs)
- [Proposing Features](#proposing-features)
- [Translations](#translations)
- [License](#license)

## Ground Rules

- Be kind. We follow the [Code of Conduct](CODE_OF_CONDUCT.md).
- Open an issue or discussion before starting large changes so we can agree on
  the direction and avoid duplicate work.
- Keep pull requests focused. One logical change per PR.
- Never commit secrets, credentials or production configuration. Only
  `.env.example` belongs in git.
- Security issues go through [SECURITY.md](SECURITY.md), not the issue tracker.

## Ways to Contribute

- Report bugs and request features through the issue templates.
- Improve documentation and translations.
- Fix a `good first issue` or `help wanted` ticket.
- Review open pull requests.
- Share what you built with EasyTV in Discussions.

## Development Setup

Requirements: Go 1.27+ (the `go` directive in `go.mod` pins the toolchain; `go`
downloads it automatically), MySQL 8.x (5.7.8+ works), optional Redis, and
`ffmpeg`/`ffprobe` on `PATH` for media tools.

```bash
git clone https://github.com/easytv-ai/easytv-api.git
cd easytv-api
cp .env.example .env            # set SQL_DSN and ROOT_PASSWORD at minimum
make hooks                      # pre-push hook: gofmt + go vet + go test
go run . --port 8383
```

Local checks (CI runs exactly these):

```bash
gofmt -l .                      # must print nothing
go vet ./...
go test -race -count=1 ./...    # sqlmock based, no real MySQL / Redis needed
make check                      # vet + test shortcut
```

Tests live next to the code (`*_test.go`) and in `tests/` for HTTP-level
smoke tests. Tests prefixed `live_` need real credentials and skip otherwise.


## Making Changes

1. Fork the repository and create a branch from `main`:
   `git checkout -b feat/short-description`
2. Make your change, including tests and documentation.
3. Run the local checks listed above. CI runs the same commands.
4. Push to your fork and open a pull request against `main`.

Core maintainers may push directly to `main`; everyone else uses pull
requests.

## Commit Messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<optional scope>): <short summary>

<optional body: what and why, not how>
```

Common types: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`,
`ci`, `chore`. Both English and Chinese summaries are welcome. Reference
issues in the body or footer (`Closes #123`).

## Pull Requests

- Fill in the pull request template. Describe **what** changed and **why**.
- Make sure CI is green. Fix lint, type and test failures before requesting a
  review.
- Add or update tests for behavior changes.
- Update `CHANGELOG.md` under **Unreleased** for user-visible changes.
- Keep the PR rebased on the latest `main`; we squash-merge.
- A maintainer will review within a week. Please respond to review comments or
  mark the PR as draft if you need more time.

## Reporting Bugs

Use the **Bug report** issue template. Include:

- The commit / version you are running and how you deployed it
- Exact steps to reproduce, expected and actual behavior
- Logs, screenshots or network traces with secrets removed

## Proposing Features

Use the **Feature request** template. Explain the problem you are solving, who
benefits, and any alternatives you considered. Small, well-scoped proposals are
much more likely to land.

## Translations

The API returns Chinese error messages by default and honours the
`Accept-Language` header where translations exist. Documentation is written in
English (`README.md`) with a Chinese mirror (`README.zh-CN.md`); keep both in
sync when you change behavior. The API contract in `API.md` is Chinese.


## License

By contributing you agree that your contributions will be licensed under the
[GNU Affero General Public License v3.0](LICENSE), the same license that
covers the project. You confirm that you have the right to submit the work
under that license (see the
[Developer Certificate of Origin](https://developercertificate.org/)).
