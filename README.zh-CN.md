<div align="center">

# EasyTV API

**[EasyTV](https://easytv.ai) 后端 —— 开源的 AI 短剧创作平台。**

[![CI](https://github.com/easytv-ai/easytv-api/actions/workflows/ci.yml/badge.svg)](https://github.com/easytv-ai/easytv-api/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/easytv-ai/easytv-api)](go.mod)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

[English](README.md) · [简体中文](README.zh-CN.md) · [接口契约](API.md) · [官网](https://easytv.ai)

</div>

EasyTV 把一句话变成一部成片短剧：剧本、分镜、图像与视频生成、配音、字幕、合成，全部在节点画布上完成。可以把它理解为**短剧创作领域的 New API**：一套可自托管的平台，接任何兼容 New API 协议的模型网关。

本仓库是 Go 后端。创作台在 [`easytv-web`](https://github.com/easytv-ai/easytv-web)，运营后台在 [`easytv-manage`](https://github.com/easytv-ai/easytv-manage)。

## 功能

- **创作域** —— 项目、带版本历史与只读分享的画布、资产、生成任务、发布作品、技能（提示词模板）与提示词库。
- **AI 智能体** —— 剧本策划、分镜审片、一句话成片（分层生成 + 自动合成）。
- **任意模型，一个网关** —— 文本、图像、视频、音频模型统一经兼容 New API 的网关调用；每个模型的积分单价在后台维护。
- **媒体工具** —— 裁切、分割、抽帧、音画分离，以及带字幕 / BGM / 水印的合成，基于 `ffmpeg`。
- **社区** —— 公开信息流、关注、点赞、评论、榜单、举报与审核队列。
- **商业化** —— 会员计划、积分包、订单、微信支付与支付宝、发票、增长机制（邀请、签到、活动）、团队额度。
- **存储** —— 本地磁盘、阿里云 OSS、七牛，运行期切换。
- **运维** —— 可热更新的 setting 表、覆盖全部实体的管理接口、结构化日志、多节点的 worker 标签。

## 架构

```
easytv-web (Nuxt)  ─┐                      ┌─ MySQL 8  (GORM AutoMigrate)
                    ├─▶  easytv-api (Gin) ─┼─ Redis     (限流、验证码；可选)
easytv-manage (Nuxt)┘        │             ├─ 对象存储 (本地 / OSS / 七牛)
                             │             └─ ffmpeg / ffprobe
                             ▼
              兼容 New API 的模型网关（文本 · 图像 · 视频 · 音频）
```

客户端以 Bearer Token 加 `EasyTV-User` 头鉴权；CORS 回显请求来源，创作台与后台可以部署在任意域名。所有运行期可变的配置都在 `setting` 表，由后台编辑。

## 快速开始

### Docker Compose（推荐）

需要 Docker 24+ 与 Compose v2。

```bash
git clone https://github.com/easytv-ai/easytv-api.git
cd easytv-api
cp .env.example .env
# 编辑 .env：设置 ROOT_PASSWORD 与 CRYPTO_SECRET（openssl rand -hex 32）
docker compose up -d
curl http://localhost:8383/healthz
```

Compose 会启动 MySQL 8.4、Redis 7 和监听 8383 的 API，上传文件与日志放在命名卷里。首次启动自动迁移数据库，并用 `ROOT_PASSWORD` 创建 `admin` 账号。

### 源码运行

需要 Go 1.27+、MySQL 8.x（5.7.8+ 亦可）、可选的 Redis，以及 `PATH` 里的 `ffmpeg` / `ffprobe`。

```bash
mysql -u root -p -e "CREATE DATABASE IF NOT EXISTS easytv DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
cp .env.example .env            # 设置 SQL_DSN、ROOT_PASSWORD、CRYPTO_SECRET
go run . --port 8383
```

### 接下来

1. 把创作台指向后端：在 `easytv-web` 设置 `NUXT_PUBLIC_API_BASE_URL=http://localhost:8383`。
2. 用 `admin` 登录 `easytv-manage`，打开 **系统 → 网关**，填入网关地址与 Key，拉取模型清单并导入定价。
3. 可选：`go run ./cmd/seed --env-file .env` 灌入演示内容。

## 配置

配置来自环境变量（从 `.env` 或 `--env-file` 指定的文件加载）和后台编辑的 `setting` 表。环境变量只在启动时读取一次；setting 表的改动无需重启。

### 必填

| 变量 | 说明 |
| --- | --- |
| `SQL_DSN` | MySQL DSN，如 `user:pass@tcp(127.0.0.1:3306)/easytv?charset=utf8mb4&parseTime=true&loc=Local` |
| `ROOT_PASSWORD` | 首个管理员（`ROOT_USERNAME`，默认 `admin`）的密码。系统无管理员且未设置时拒绝启动。 |
| `CRYPTO_SECRET` | 签名验证码与令牌的 HMAC 密钥，生产环境 ≥ 32 位随机字符。 |

### 常用

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `PORT` | `8383` | HTTP 端口（`--port` 参数优先） |
| `DEBUG` | `false` | 详细日志与 SQL 回显 |
| `REDIS_CONN_STRING` | – | Redis 地址；未设置时限流与验证码降级为内存模式 |
| `SQL_MAX_IDLE_CONNS` / `SQL_MAX_OPEN_CONNS` | `100` / `1000` | 连接池 |
| `UPLOAD_DIR` / `UPLOAD_MAX_MB` | `./upload` / – | 本地存储目录与单文件上限 |
| `FFMPEG_PATH` / `FFPROBE_PATH` | 读 `PATH` | 媒体工具二进制（setting `FFmpegPath` / `FFprobePath` 优先） |
| `WORKER_TAG` | – | 只处理带该标签的作业，多节点部署用 |
| `VERSION` | `1.0.0` | 对外报告的版本号 |

### 集成（全部可选）

| 领域 | 变量 / setting |
| --- | --- |
| 邮箱验证码 | `EMAIL_VERIFICATION_ENABLED`、`SMTP_HOST`、`SMTP_PORT`、`SMTP_USER`、`SMTP_PASS`、`SMTP_FROM`、`VERIFICATION_VALID_MINUTES` |
| 短信验证码（SubMail） | `SMS_ENABLED`、`SUBMAIL_URL`、`SUBMAIL_APP_ID`、`SUBMAIL_APP_KEY`、`SUBMAIL_SIGN_TYPE`、`SMS_SIGNATURE` |
| 模型网关 | setting `EasyAPIGatewayBaseUrl`、`EasyAPIGatewayKey`、`EasyAPIGatewayEnabled`（后台 → 系统 → 网关）。模型清单与单价在 `model_price` 表。 |
| 阿里云 OSS | `OSS_BUCKET`、`OSS_ENDPOINT`、`OSS_ACCESS_KEY_ID`、`OSS_ACCESS_KEY_SECRET`、`OSS_BASE_URL`、`OSS_PREFIX`、`OSS_PUBLIC_READ` |
| 七牛 | setting `QiniuAccessKey`、`QiniuSecretKey`、`QiniuBucketName`、`QiniuDomain` |
| 支付 | `WECHATPAY_*`、`ALIPAY_APPID` 或对应 setting；`ServerAddress` setting 用于拼接回调地址 |
| 额度 | `FREE_CONCURRENT`、`FREE_STORAGE_GB` |

完整带注释的清单见 [`.env.example`](.env.example)。

## 部署

### 二进制 + systemd

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

没有 systemd 的主机可用 `deploy.sh start|stop|restart|status [env 后缀]` 管理同一个二进制；`make package` 产出它需要的压缩包。

### 反向代理

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

CORS 回显任意 `Origin` 并允许携带凭据。鉴权走 `Authorization` 头而非 Cookie，因此不会扩大 CSRF 面；如有合规要求可在代理层限制来源。

### 健康检查

进程开始服务后 `GET /healthz` 返回 `200 {"status":"ok"}`，Docker 镜像与 Compose 文件都用它做探针。

## 接口

- **鉴权**：`POST /user/login`（邮箱）或 `GET /sms/verification` + `POST /user/sms_login`（手机号），响应带 `access_token`。
- **所有需登录的请求**携带 `Authorization: Bearer <token>` 与 `EasyTV-User: <用户 id>`。`/admin/*` 还要求 role ≥ 10。
- **契约**：[`API.md`](API.md) 记录了全部实体、路由与状态机。先改契约，再改代码。

## 开发

```bash
make hooks        # 推送前钩子：gofmt + go vet + go test
make check        # go vet + go test
go test -race ./...
```

```
cmd/          seed、migrate-assets、backfill-*、purge-user 等工具
common/       env、redis、响应、哈希、验证码
constant/     共享枚举
controller/   HTTP 处理（用户侧 / 管理侧）
logger/       结构化日志
middleware/   鉴权、限流、CORS、请求日志
model/        GORM 实体与迁移
oauth/        OAuth Provider 注册表
router/       路由表（api-router 用户侧，admin-router 管理侧）
service/      业务逻辑：智能体、合成、计费、存储、模型供应商
setting/      setting 表的类型化访问
tests/        HTTP 级冒烟测试（sqlmock）
```

工作流程与提交规范见 [CONTRIBUTING.md](CONTRIBUTING.md)，漏洞报告见 [SECURITY.md](SECURITY.md)，变更记录见 [CHANGELOG.md](CHANGELOG.md)。

## 相关项目

| 仓库 | 说明 |
| --- | --- |
| [easytv-web](https://github.com/easytv-ai/easytv-web) | 创作台：画布、导演台、一句话成片 |
| [easytv-manage](https://github.com/easytv-ai/easytv-manage) | 运营后台：用户、作品、审核、计费、设置 |
| [easytv-website](https://github.com/easytv-ai/easytv-website) | 项目官网 |
| [New API](https://github.com/QuantumNous/new-api) | 本后端对接的模型网关，其架构也是我们的参考 |

## 开源协议

EasyTV API 以 [GNU Affero General Public License v3.0](LICENSE) 发布。

用户、支付与对象存储模块的结构参考了 [New API](https://github.com/QuantumNous/new-api)（源自 [One API](https://github.com/songquanpeng/one-api)），二者同为 AGPL-3.0。再分发时请保留此署名。
