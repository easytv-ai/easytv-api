<div align="center">

# EasyTV API

**Backend of [EasyTV](https://easytv.ai) — the open-source AI short-drama studio.**

[![CI](https://github.com/easytv-ai/easytv-api/actions/workflows/ci.yml/badge.svg)](https://github.com/easytv-ai/easytv-api/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/easytv-ai/easytv-api)](go.mod)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

[English](README.md) · [简体中文](README.zh-CN.md) · [API contract](API.md) · [Website](https://easytv.ai)

</div>

EasyTV turns a one-line idea into a finished short drama: script, storyboard,
image and video generation, voice, subtitles and final compose, all on a
node-based canvas. Think of it as the **New API of short-drama creation** — a
self-hostable platform that plugs into any New API-compatible model gateway.

This repository is the Go backend. The creator studio lives in
[`easytv-web`](https://github.com/easytv-ai/easytv-web) and the admin console in
[`easytv-manage`](https://github.com/easytv-ai/easytv-manage).

## Features

- **Creation domain** — projects, canvases with version history and read-only
  sharing, assets, generation tasks, published works, skills (prompt templates)
  and a prompt library.
- **AI agent** — screenplay planning, storyboard review, one-sentence-to-film
  pipeline with layered generation and automatic compose.
- **Any model, one gateway** — text, image, video and audio models are called
  through a New API-compatible gateway; pricing is managed per model in the
  admin console.
- **Media tools** — trim, split, extract frames, separate audio and compose
  with subtitles / BGM / watermark, powered by `ffmpeg`.
- **Community** — public feed, follows, likes, comments, rankings, reports and
  moderation queues.
- **Monetisation** — membership plans, point packs, orders, WeChat Pay and
  Alipay, invoices, growth mechanics (invites, check-ins, campaigns), teams with
  quotas.
- **Storage** — local disk, Aliyun OSS or Qiniu, switchable at runtime.
- **Operations** — hot-reloadable settings table, admin APIs for every entity,
  structured logs, worker tags for multi-node deployments.

## Architecture

```
easytv-web (Nuxt)  ─┐                      ┌─ MySQL 8  (GORM AutoMigrate)
                    ├─▶  easytv-api (Gin) ─┼─ Redis     (rate limit, codes; optional)
easytv-manage (Nuxt)┘        │             ├─ Object storage (local / OSS / Qiniu)
                             │             └─ ffmpeg / ffprobe
                             ▼
              New API-compatible model gateway (text · image · video · audio)
```

Clients authenticate with a bearer token plus an `EasyTV-User` header; CORS
reflects the request origin so the studio and console can be served from any
domain. Everything configurable at runtime lives in the `setting` table and is
edited from the admin console.

## Quick Start

### Docker Compose (recommended)

Requires Docker 24+ with Compose v2.

```bash
git clone https://github.com/easytv-ai/easytv-api.git
cd easytv-api
cp .env.example .env
# edit .env: set ROOT_PASSWORD and CRYPTO_SECRET (openssl rand -hex 32)
docker compose up -d
curl http://localhost:8383/healthz
```

Compose starts MySQL 8.4, Redis 7 and the API on port 8383. Uploads and logs
persist in named volumes. The first start runs the database migration and
creates the `admin` user with `ROOT_PASSWORD`.

### From source

Requires Go 1.27+, MySQL 8.x (5.7.8+ works), optional Redis, and
`ffmpeg`/`ffprobe` on `PATH`.

```bash
mysql -u root -p -e "CREATE DATABASE IF NOT EXISTS easytv DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
cp .env.example .env            # set SQL_DSN, ROOT_PASSWORD, CRYPTO_SECRET
go run . --port 8383
```

### Next steps

1. Point the studio at the API: `NUXT_PUBLIC_API_BASE_URL=http://localhost:8383`
   in `easytv-web`.
2. Sign in to `easytv-manage` as `admin`, open **System → Gateway**, enter your
   gateway URL and key, fetch the model list and import pricing.
3. Optional: seed demo content with `go run ./cmd/seed --env-file .env`.

## Configuration

Settings come from environment variables (loaded from `.env`, or the file
passed with `--env-file`) and from the `setting` table edited in the admin
console. Environment variables are read once at start; table settings reload
without restart.

### Required

| Variable | Description |
| --- | --- |
| `SQL_DSN` | MySQL DSN, e.g. `user:pass@tcp(127.0.0.1:3306)/easytv?charset=utf8mb4&parseTime=true&loc=Local` |
| `ROOT_PASSWORD` | Password for the first admin (`ROOT_USERNAME`, default `admin`). Startup refuses to run without it when no admin exists. |
| `CRYPTO_SECRET` | HMAC secret for signed codes and tokens. ≥ 32 random chars in production. |

### Common

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8383` | HTTP port (`--port` flag overrides) |
| `DEBUG` | `false` | Verbose logging and SQL echo |
| `REDIS_CONN_STRING` | – | Redis URL; falls back to in-memory rate limiting and codes when unset |
| `SQL_MAX_IDLE_CONNS` / `SQL_MAX_OPEN_CONNS` | `100` / `1000` | Connection pool |
| `UPLOAD_DIR` / `UPLOAD_MAX_MB` | `./upload` / – | Local storage directory and per-file limit |
| `FFMPEG_PATH` / `FFPROBE_PATH` | from `PATH` | Media tool binaries (settings `FFmpegPath` / `FFprobePath` take precedence) |
| `WORKER_TAG` | – | Only process jobs tagged with this value; useful for multi-node deployments |
| `VERSION` | `1.0.0` | Reported version string |

### Integrations (all optional)

| Area | Variables / settings |
| --- | --- |
| Email codes | `EMAIL_VERIFICATION_ENABLED`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM`, `VERIFICATION_VALID_MINUTES` |
| SMS codes (SubMail) | `SMS_ENABLED`, `SUBMAIL_URL`, `SUBMAIL_APP_ID`, `SUBMAIL_APP_KEY`, `SUBMAIL_SIGN_TYPE`, `SMS_SIGNATURE` |
| Model gateway | Settings `EasyAPIGatewayBaseUrl`, `EasyAPIGatewayKey`, `EasyAPIGatewayEnabled` (admin → System → Gateway). Model list and prices live in `model_price`. |
| Aliyun OSS | `OSS_BUCKET`, `OSS_ENDPOINT`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET`, `OSS_BASE_URL`, `OSS_PREFIX`, `OSS_PUBLIC_READ` |
| Qiniu | Settings `QiniuAccessKey`, `QiniuSecretKey`, `QiniuBucketName`, `QiniuDomain` |
| Payments | `WECHATPAY_*`, `ALIPAY_APPID` or the equivalent settings; `ServerAddress` setting builds callback URLs |
| Quotas | `FREE_CONCURRENT`, `FREE_STORAGE_GB` |

See [`.env.example`](.env.example) for the full annotated list.

## Deployment

### Binary + systemd

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o easytv-api .
```

```ini
# /etc/systemd/system/easytv-api.service
[Unit]
Description=EasyTV API
After=network.target mysql.service

[Service]
User=easytv
WorkingDirectory=/opt/easytv-api
ExecStart=/opt/easytv-api/easytv-api --port 8383 --env-file /opt/easytv-api/.env
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

`deploy.sh start|stop|restart|status [env-suffix]` wraps the same binary for
hosts without systemd; `make package` produces the tarball it expects.

### Reverse proxy

```nginx
location /api/ {
    proxy_pass http://127.0.0.1:8383/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 300s;
    client_max_body_size 512m;
}
```

CORS reflects any `Origin` and allows credentials. Authentication uses the
`Authorization` header rather than cookies, so this does not widen the CSRF
surface; restrict origins at the proxy if your policy requires it.

### Health check

`GET /healthz` returns `200 {"status":"ok"}` once the process is serving. The
Docker image and Compose file use it.

## API

- **Authentication**: `POST /user/login` (email) or `GET /sms/verification` +
  `POST /user/sms_login` (phone). Responses carry `access_token`.
- **Every authenticated request** sends `Authorization: Bearer <token>` and
  `EasyTV-User: <user id>`. Admin routes under `/admin/*` additionally require
  role ≥ 10.
- **Contract**: [`API.md`](API.md) documents every entity, route and state
  machine (Chinese). Change the contract first, then the code.

## Development

```bash
make hooks        # pre-push: gofmt + go vet + go test
make check        # go vet + go test
go test -race ./...
```

```
cmd/          seed, migrate-assets, backfill-*, purge-user utilities
common/       env, redis, responses, hashing, codes
constant/     shared enums
controller/   HTTP handlers (user / admin)
logger/       structured logging
middleware/   auth, rate limit, CORS, request log
model/        GORM entities and migrations
oauth/        OAuth provider registry
router/       route tables (api-router user side, admin-router admin side)
service/      business logic: agent, compose, billing, storage, providers
setting/      typed accessors for the settings table
tests/        HTTP-level smoke tests (sqlmock)
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow and commit
conventions, [SECURITY.md](SECURITY.md) for reporting vulnerabilities and
[CHANGELOG.md](CHANGELOG.md) for release notes.

## Related Projects

| Repository | Description |
| --- | --- |
| [easytv-web](https://github.com/easytv-ai/easytv-web) | Creator studio: canvas, director stage, one-sentence-to-film |
| [easytv-manage](https://github.com/easytv-ai/easytv-manage) | Admin console: users, works, moderation, billing, settings |
| [easytv-website](https://github.com/easytv-ai/easytv-website) | Project website |
| [New API](https://github.com/QuantumNous/new-api) | Model gateway this backend talks to; its architecture inspired ours |

## License

EasyTV API is released under the
[GNU Affero General Public License v3.0](LICENSE).

Parts of the user, payment and object-storage modules follow the structure of
[New API](https://github.com/QuantumNous/new-api) (itself derived from
[One API](https://github.com/songquanpeng/one-api)), both AGPL-3.0. Keep this
attribution when redistributing.
