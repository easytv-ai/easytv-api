# EasyTV API

EasyTV 的 **AI 图片/视频创作平台后端**，参考 [easyapi-ai-api](https://github.com/QuantumNous/new-api) 的 Go 架构搭建。用户界面由独立前端项目（`easytv-web`）提供，本仓库仅包含 Go 后端服务。

## 技术栈

- Go 1.22+、Gin、GORM
- MySQL ≥ 5.7.8
- Redis（推荐，验证码/限流使用；不可用时自动降级内存）

## 关联项目

| 项目 | 说明 |
|------|------|
| `easyapi-ai-api` | 架构参考项目（AI 网关后端，New API 二次开发） |
| `easytv-api`（本仓库） | EasyTV 后端：用户/登录/支付/财务基础模块 |
| `easytv-web` | EasyTV 创作平台前端（Nuxt 4） |

## 本地开发

```bash
cd easytv-api

# 在项目根目录创建 .env，至少配置 SQL_DSN、REDIS_CONN_STRING
# 可按下方「环境变量」表手写，或用 --env-file 指定其它环境文件（如 .env.prod）
go run main.go
```

默认监听 **8383** 端口，可通过环境变量 `PORT` 或启动参数 `--port` 修改。

克隆后执行一次 `make hooks` 启用仓库内的 git hooks：推送前自动跑 `go vet` 与 `go test`，失败则拒绝推送（临时跳过用 `git push --no-verify`）；`make check` 可手动执行同样的检查。

首次启动自动执行 GORM 迁移（AutoMigrate）建表。若系统中尚无管理员，会使用 `ROOT_USERNAME`（默认 `admin`）与 `ROOT_PASSWORD` 创建 root 账号；**`ROOT_PASSWORD` 为必填项，未设置时拒绝启动**（不再存在默认口令）。

## 生产部署

### 构建与打包

```bash
go build -o easytv-api main.go   # 生成 easytv-api 二进制（Linux 上交叉编译用 GOOS=linux go build）
```

### 服务器启动

```bash
# 将二进制与 .env 放在同一目录
./easytv-api --port 8585            # --port 覆盖 PORT 环境变量；--env-file 指定环境文件
```

### 数据库初始化

```bash
mysql -u user -p -e "CREATE DATABASE IF NOT EXISTS easytv DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
```

主节点启动时自动执行 GORM 迁移建表（`SQL_DSN` 指向 `easytv` 库）。

### 展示数据（seed）与存量素材迁移

```bash
# 幂等灌入演示作品/技能/定价等展示数据（deploy.sh 不含该步骤，部署后手动执行）
go run ./cmd/seed --env-file .env

# 存量本地素材迁移 OSS（url=/upload/... 的行上传 OSS 并回填，幂等）
go run ./cmd/migrate-assets --env-file .env
```

联调/部署手册见 `docs/ops/`（EasyAPI 网关、支付走账、部署与 seed）；凭据轮换清单见 `docs/security/credential-rotation.md`。

### systemd（推荐）

参考 easyapi-ai-api 的 `easyapi-ai-api.service` 单元文件，配置 `Restart=always`，并确保 `--port` 与 Nginx `proxy_pass` 一致。

## 环境变量

在项目根目录 `.env` 中配置，启动时自动加载。

| 变量 | 说明 |
|------|------|
| `SQL_DSN` | 主库连接串（必填，指向 `easytv` 库） |
| `SQL_MAX_IDLE_CONNS` | 连接池空闲连接数，默认 100 |
| `SQL_MAX_OPEN_CONNS` | 连接池最大连接数，默认 1000 |
| `REDIS_CONN_STRING` | Redis 连接串；未配置或不可达时降级内存模式（验证码/限流） |
| `CRYPTO_SECRET` | HMAC 签名密钥（生产必设） |
| `PORT` | 监听端口，默认 8383 |
| `DEBUG` | 调试模式（true 输出更多日志与 SQL） |
| `VERIFICATION_VALID_MINUTES` | 邮箱验证码有效分钟数，默认 10 |
| `EMAIL_VERIFICATION_ENABLED` | 是否走 SMTP 发送验证码；false 时验证码仅打印日志（联调） |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` / `SMTP_FROM` | SMTP 邮件服务配置 |
| `SMS_ENABLED` | 是否走 SubMail 发送短信验证码；false 时验证码仅打印日志（联调） |
| `SUBMAIL_URL` / `SUBMAIL_APP_ID` / `SUBMAIL_APP_KEY` / `SUBMAIL_SIGN_TYPE` | SubMail 短信服务参数（签名模式默认 md5） |
| `SMS_SIGNATURE` | 短信签名（需与 SubMail 报备一致，含【】） |
| `ROOT_USERNAME` / `ROOT_PASSWORD` | 首次启动创建 root 管理员账号；`ROOT_PASSWORD` 必填（≥8 位），未设置拒绝启动 |
| `WECHATPAY_APPID` / `WECHATPAY_MCHID` / `WECHATPAY_MCH_SERIALNO` / `WECHATPAY_APIv3KEY` | 微信支付商户参数（也可写入 DB setting 表热更新） |
| `ALIPAY_APPID` | 支付宝应用 ID（也可写入 DB setting 表热更新） |
| `EASYAPI_KEY` / `EASYAPI_BASE_URL` | EasyAPI 网关令牌与地址，画布生成任务全部经网关（见 API.md 1.4）。**已迁移为 DB setting `EasyAPIGatewayKey`/`EasyAPIGatewayBaseUrl`**，管理端 `/admin/gateway/config` 热更新（env 仅首次迁移回退） |
| `EASYAPI_TEXT_MODELS` / `EASYAPI_IMAGE_MODELS` / `EASYAPI_VIDEO_MODELS` / `EASYAPI_AUDIO_MODELS` | **已废弃**，模型清单统一由 `model_price` 表管理：`/admin/gateway/fetch-models` 从网关拉取 + `/admin/gateway/models/import` 导入并定价 |
| `UPLOAD_DIR` / `UPLOAD_MAX_MB` | 本地上传目录、单文件上限（对外前缀见 setting `UploadBaseUrl`，管理端可热更新） |
| `OSS_BUCKET` / `OSS_ENDPOINT` / `OSS_ACCESS_KEY_ID` / `OSS_ACCESS_KEY_SECRET` / `OSS_BASE_URL` / `OSS_PREFIX` | 阿里云 OSS，配置桶后上传与生成产物直接写 OSS（见 API.md 4） |

## 前后端分离与鉴权

生产环境前端（`easytv-web`）与后端通常分域部署，控制台登录使用 **Token 鉴权**，不依赖 Cookie 跨域。

1. 登录：`POST /user/login`，响应 `data.access_token`
   - 手机号验证码登录：`GET /sms/verification?mobile=13812341234` 发送验证码 → `POST /user/sms_login`（body `{"mobile", "code"}`），未注册手机号自动注册
2. 后续请求需携带：
   - `Authorization: Bearer <access_token>`
   - `EasyTV-User: <用户 id>`（未登录传 `-1`）

> ⚠️ **前端注意**：`easytv-web` 的 `app/utils/auth-user.ts` 当前发送的是 `EasyAPI-User` 请求头，接入本后端前需改为 **`EasyTV-User`**。

用户资料字段：响应中使用 **`nickname`**（数据库列 `nickname`）。登录响应同时返回 `access_token`。

### 前端配置

```bash
# easytv-web
NUXT_PUBLIC_API_BASE_URL=https://api.easytv.example.com
```

### Nginx 反代示例

```nginx
location /api/ {
    proxy_pass http://127.0.0.1:8585;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 300s;
}
```

CORS 无条件放行所有跨域来源（参考 easyapi 母版中继接口的 allow-all 行为）：任意请求 `Origin` 均回显原值并允许携带凭据（`Access-Control-Allow-Credentials: true`），预检 `OPTIONS` 返回 204。

## 数据表（AutoMigrate 自动创建）

| 表 | 说明 |
|------|------|
| `user` | 用户主表（username=邮箱；第三方登录列、推广字段、额度，字段与 easyapi 对齐） |
| `user_oauth` | 第三方 OAuth 绑定关系（预留其它登录方式） |
| `recharge` | 充值/支付订单（微信 Native / 支付宝） |
| `withdraw` | 提现单（待审核 → 打款中 → 已打款 / 驳回） |
| `promoter_bill` | 资金流水（充值/提现/退回/调整，Balance 余额快照） |
| `setting` | key-value 配置（支付开关、充值面额、站点地址等，运营热更新） |
| `project` | 项目（创作容器，软删进回收站 30 天） |
| `canvas` | 画布（工作流草稿，graph JSON + 乐观锁 version） |
| `asset` | 资产（上传素材 / 生成产物，本地 `upload/` 存储） |
| `generation_task` | 生成任务（pending → running → succeeded / failed / cancelled） |
| `work` | 作品（发布后的成片，公开信息流数据源；featured/sort_order 精选，review_status 发布审核） |
| `work_report` | 作品举报（社区审核，成立即下架作品） |
| `category` | 内容分类（成片分类 / Skill 分类，前台筛选标签） |

> 创作域五张表的字段、路由与三端页面映射见 **[API.md](API.md)**，改接口先改契约。

## 常见问题

| 现象 | 处理 |
|------|------|
| **502** | 后端未运行或端口不一致；检查 Nginx `proxy_pass` 与 `PORT` |
| **启动失败/秒退** | 查看日志，常见原因：数据库/Redis 连不上、`SQL_DSN` 库不存在（先执行建库语句） |
| **登录后接口 401** | 确认请求带 `Authorization` 与 `EasyTV-User` 头；重新登录获取新 `access_token` |
| **充值下单提示"未开启"** | 微信/支付宝渠道开关（DB `setting` 表 `WechatPayEnabled` / `AlipayEnabled`）未开启或商户参数未配置 |
| **验证码收不到邮件** | `EMAIL_VERIFICATION_ENABLED=false` 时验证码打印在服务日志；开启 SMTP 后检查 SMTP 配置 |
| **前端登录 401/鉴权失败** | 确认前端已把请求头 `EasyAPI-User` 改为 `EasyTV-User` |

## 目录说明

```
router/          HTTP 路由（api-router 用户侧 / admin-router 管理侧）
controller/      接口处理（user/wechat_oauth/oauth/recharge/withdraw/admin_*）
service/         业务逻辑（充值入账等）
model/           数据模型（GORM 实体 + 迁移注册）
middleware/      鉴权、限流、CORS、请求日志
oauth/           OAuth Provider 注册表（新增登录方式 = 实现接口 + 一行注册）
setting/         DB 配置表类型化封装（支付配置）
common/          通用工具（env/redis/响应/密码哈希/验证码）
docs/openapi/    OpenAPI 接口文档
tests/           冒烟测试（sqlmock，无需真实 MySQL/Redis）
```

## 开源协议

本项目为内部项目，架构参考 [easyapi-ai-api](https://github.com/QuantumNous/new-api)（[New API](https://github.com/QuantumNous/new-api) / [One API](https://github.com/songquanpeng/one-api) 系，AGPLv3）与 [EasyAPI](https://github.com/QuantumNous/new-api) 体系；如对外分发，请遵守上游许可（AGPLv3）并保留署名要求。
