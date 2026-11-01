# EasyTV 三端接口契约（创作域）

> 本文是 `easytv-api`（Go）、`easytv-web`（创作台）、`easytv-manage`（运营后台）三端对齐的唯一依据。
> 改接口先改这里，再改代码。用户/登录/充值/提现等已有接口见 `README.md`。

## 0. 通用约定

- **Base URL**：`NUXT_PUBLIC_API_BASE_URL`（生产 `https://tv-api.easyapi.com`），本地 `http://127.0.0.1:8383`。
- **鉴权头**：`Authorization: Bearer <access_token>` + `EasyTV-User: <user_id>`。`/user/**` 需登录，`/admin/**` 需 role ≥ 10。
- **空间头**：`EasyTV-Team: <team_id>` 选择团队空间，缺省 / 0 为个人空间（2026-09-14 起，见 20）。
- **响应包**：HTTP 恒 200。
  ```json
  { "success": true,  "message": "", "data": {} }
  { "success": false, "message": "错误原因" }
  ```
- **分页**：请求 `?page=1&page_size=20`（上限 100）；响应 `data = { "items": [], "total": 123 }`。
- **时间**：全部 Unix 秒（int64）。`deleted_at` 为空表示未删除。
- **归属**：`/user/**` 下所有资源只能操作 `user_id = 当前用户` 的记录，越权返回 `success:false, message:"资源不存在"`（不区分不存在与无权，避免枚举）。
- **软删/回收站**：`DELETE` 进回收站（`deleted_at` 置值），30 天后由后台任务物理清理；`/restore` 恢复。

## 1. 实体

### 1.1 project 项目（作品容器）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| user_id | int | 所有者 |
| name | string(100) | |
| description | string(500) | |
| cover_url | string(500) | 封面。列表为空时附带最近一张画面（图片、视频抽帧，或画布缩略图），不写回数据库 |
| status | int | 1 正常 |
| canvas_id | int | **只读、列表附带**：该项目最近更新的画布 id，前端点卡片直接打开 |
| created_at / updated_at / deleted_at | int64 | |

### 1.2 canvas 画布（工作流草稿）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| user_id | int | |
| project_id | int | 所属项目。**创建时不传则后端自动新建同名项目并关联** |
| name | string(100) | 与页面顶部草稿名同步；改名同时更新项目名 |
| graph | JSON | `{ "nodes": [...], "edges": [...], "viewport": {"x":0,"y":0,"zoom":1} }`，原样存取，后端不解析节点语义，上限 2 MB |
| version | int | 乐观锁。PUT 必须带当前 version，不一致返回 `画布已在其他地方修改，请刷新` |
| thumbnail_url | string(500) | |
| created_at / updated_at / deleted_at | int64 | |

`graph.nodes[]` 由前端 `CanvasWorkspace` 定义（vue-flow 节点 + `data:{kind,label,content,prompt,mode}`），后端透传。

`kind=director`（3D 导演台）的节点在 `data.scene` 里带一份完整场景（角色、机位、运镜节点、场景设置），**后端同样不解析**。前端存之前已经摘掉机位截图（base64 PNG，单张可达 1 MB 以上）和浏览器本地 blob 素材，并按 256 KB 软上限提示用户——2 MB 是**整张画布**共用的额度，一个导演台节点撑爆它，整张画布都保存不了。相关逻辑在 `easytv-web/app/utils/director/persist.ts`。

#### 1.2.1 canvas_version 画布版本历史

| 字段 | 类型 | 说明 |
|---|---|---|
| id / canvas_id / user_id | int | |
| version | int | 对应保存后的 `canvas.version` |
| name / node_count | | 当时的画布名与节点数 |
| graph | longtext | 快照（列表接口不返回，单个版本接口返回） |
| source | string | `auto` 自动保存（10 分钟内合并为一条）/ `manual` 主动保存（`PUT /user/canvas/:id` 带 `snapshot: true`）/ `restore` 恢复前自动备份 |
| note | string(100) | 备注 |

每个画布最多保留 50 个版本。`GET /user/canvas/:id/versions` 列表、`GET /user/canvas/:id/versions/:vid` 取单个（含 graph）、`POST /user/canvas/:id/versions/:vid/restore` 回滚（先备份当前状态，再覆盖 graph，返回新的画布详情含 graph 与 version）。

#### 1.2.2 画布只读分享

画布增加 `share_enabled`、`share_token`（首次开启时生成，关闭后保留但链接失效）。分享页路径 `/share/<token>`（2026-09-18 起；旧的 `/s/<token>` 由前端 301 过去），匿名可看。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / PUT | `/user/canvas/:id/share` | 分享状态；PUT `{ enabled }`，返回 `{ enabled, token, path }` |
| GET | `/share/canvas/:token` | 匿名：`{ name, node_count, updated_at, owner:{id,nickname}, graph, mine }`；graph 去掉任务状态、所有资产 ID 引用（含 refItems / 导演台场景里的）与 subjectIds，保留节点内容、提示词、媒体地址（见 11.3） |
| POST | `/user/share/canvas/:token/copy` | 复制为当前用户的新项目 + 画布（名称前缀「副本 · 」），返回画布详情 |

### 1.3 asset 资产（素材与生成产物）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| user_id | int | |
| project_id | int | 可 0 |
| type | string | `image` / `video` / `audio` / `text` / `other`，上传时按 MIME 自动判断 |
| name | string(200) | 原始文件名 |
| url | string(1000) | 可直接访问的地址 |
| cover_url | string(1000) | 视频资产自动抽帧的封面（OSS 截帧或 ffmpeg，异步写入，可能为空） |
| storage_key | string(500) | 存储路径（本地 `upload/…`，后续换 OSS 不改契约） |
| mime | string(100) | |
| size | int64 | 字节 |
| width / height | int | 图片、视频 |
| duration | float | 秒，音视频 |
| source | string | `upload` 用户上传 / `generated` 任务产物 |
| task_id | int | 产物来源任务 |
| created_at / deleted_at | int64 | |

### 1.4 generation_task 生成任务

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| task_no | string(32) | 对外单号 `GT…` |
| user_id / project_id / canvas_id | int | |
| node_id | string(64) | 画布节点 id，回填结果用 |
| type | string | `text` / `image` / `video` / `audio` |
| provider | string(50) | 后端根据 model 路由到的厂商（minimax / kling / …） |
| model | string(100) | 前端 Inspector 选择的模型标识 |
| prompt | text | |
| params | JSON | 模型参数：`{ "ratio":"16:9", "duration":5, "resolution":"1080p", ... }` 透传给 provider |
| inputs | JSON | 输入素材：`[{ "asset_id": 1, "role": "first_frame" }, ...]` |
| status | string | `pending` → `running` → `succeeded` / `failed` / `cancelled` |
| progress | int | 0-100 |
| result_asset_id | int | 成功后写入的 asset |
| result_url | string(1000) | 冗余，前端直接展示 |
| result_text | text | 文本任务的生成结果（text 类型没有 asset） |
| error_msg | string(500) | |
| cost_points | int | 扣减积分（provider 回填） |
| created_at / started_at / finished_at | int64 | |

**生成只走 EasyAPI 网关**（`service/providers/easyapi.go`），不直连任何模型厂商：文本 `POST /v1/chat/completions`、图片 `POST /v1/images/generations`、语音 `POST /v1/audio/speech`、视频 `POST /v1/videos` + `GET /v1/videos/{job_id}` 轮询（素材用 `input_references[{type,url,role}]` 数组，支持 `first_frame`/`last_frame`/`reference` 图、`reference_video`、`reference_audio`，任务完成后从响应 `download_url` 下载成片；网关任务 ID 记入 `generation_task.provider_task_id`，进程重启后自动恢复轮询）。可用模型清单 = **`model_price` 表（status=1）单一数据源**，通过 `GET /generation/models` 下发给前端；管理端 `GET /admin/gateway/fetch-models` 从网关拉取模型与定价（含建议积分：网关美元成本 × 汇率 × 毛利系数 × 积分/元，按量模型按假设用量折成次 / 张 / 秒，见 docs/ops/easyapi-gateway.md），`POST /admin/gateway/models/import` 导入 `model_price` 并定价。`POST /user/task` 的 `model` 不在清单里时返回 `模型暂未接入`，不落库。`params` 透传约定：`ratio` / `aspect_ratio`（比例，视频走顶层字段，`ratio` 为旧参数名兼容）、`resolution`（480P/720P/1080P）、`duration`（秒）、`size`（直接指定尺寸）、`generate_audio`（是否生成音频）、`seed`（随机种子）、`voice`（语音音色）、`negative_prompt`（进 metadata）。`inputs` 素材角色：`first_frame`（首帧图）、`last_frame`（尾帧图）、`reference`（外观参考图，不作首帧）、`reference_video`（参考视频）、`reference_audio`（参考音频）。**文本任务（type=text）也可带图片**：`inputs` 里 `image` / `reference` 等图片角色的素材（最多 4 张）按 OpenAI 视觉格式随消息发给模型（`content` = `text` + `image_url` 数组；公网地址直接给 URL，本地存储转 data URL），用于画布「反推提示词」与智能编辑「智能识别」，需选能看图的文本模型（如 gpt-4o-mini）；不带图片时与原来一样只发文字。网关地址/令牌经 setting（`EasyAPIGatewayBaseUrl`/`EasyAPIGatewayKey`/`EasyAPIGatewayEnabled`）在 `/admin/gateway/config` 热更新；定价接口不在同域名时另填 `EasyAPIGatewayPricingBaseUrl`（`pricing_base_url`），`fetch-models` 会从网关分辨率倍率表推出视频模型的 `resolutions` 一并导入（已有值不覆盖）。

### 1.5 work 作品（发布后的成片）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| user_id / project_id / asset_id | int | |
| title | string(200) | |
| description | string(1000) | |
| cover_url / video_url | string(1000) | 发布时 cover_url 留空则用成片资产的抽帧封面；编辑时传空串会重新抽帧 |
| duration | float | 秒 |
| canvas_id / show_process | | 发布时所在画布与「公开制作过程」开关（默认 true）；`GET /work/:id/process` 返回该画布的节点（kind/label/prompt/content/model/ref_url/x/y）与连线，供播放页「查看制作过程」只读展示 |
| status | int | 0 草稿 / 1 已发布 / 2 已下架 / -1 审核驳回 |
| review_status | int | 发布审核：0 待审核 / 1 已通过 / 2 已驳回（驳回时 status 同步置 -1；发布不阻塞，审核只做事后处理） |
| featured / sort_order | int | 精选成片：featured=1 在信息流置顶，按 sort_order 升序（越小越靠前） |
| view_count / like_count | int | |
| published_at / created_at / updated_at / deleted_at | int64 | |

公开信息流（`GET /work`）只返回 `status = 1` 的作品，排序为 `featured desc, sort_order asc, published_at desc`，并附带 `author: { id, nickname }`；带登录态请求时附带 `liked`（当前用户是否已点赞，表 `work_like`）。作品另有 `category`（取值来自 1.7 分类表，scope=work），`GET /work?category=` 可过滤，`GET /work?featured=1` 只取精选。

### 1.6 skill 技能模板（运营维护，Agent 页 / 画布对话面板展示）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| title / slug / description | string | 名称、路径标识、一句话描述 |
| author_name | string(100) | 作者展示名 |
| category | string(50) | 分类（专业影视 / 商业广告 / 短剧漫剧 / 动漫游戏 / 音乐MV / 自媒体创作 / 通用技能 / 发现） |
| cover_url / video_url | string | 封面、示例视频 |
| scene / howto / output | string | 适用场景、输入说明、产出说明（技能弹窗三段文案） |
| prompt | text | 提示词模板，`{{prompt}}` 占位 |
| guide | text | 执行规则：这个技能要求 AI 按什么步骤做、必须问清什么、有哪些约束。上限 4000 字符 |
| output_format | text | 输出格式说明：产出应该长什么样。上限 4000 字符 |
| uses / stars | int | 使用次数、收藏数（收藏表 `skill_favorite`，用户 × 技能唯一） |
| graph / graph_nodes | | 画布模板：从画布「存为 Skill」写入（去掉任务状态与资产 ID），列表只返回 `graph_nodes`，详情返回 `graph`；进入画布时有模板则整块载入并把节点里的 `{{prompt}}` 替换为灵感 |
| user_id | int | 0 官方；>0 用户私有 Skill（只有本人可见，`GET /skill` 只返回官方） |
| favorited | bool | 带登录态时附带：当前用户是否已收藏 |
| sort_order | int | 越大越靠前 |
| status | int | 1 已上架 / 2 已下架 |

`guide` / `output_format` 合称**执行契约**：`POST /user/task` 带 `skill_id` 且 `type=text` 时，服务端把两段按
`【执行规则】…【输出格式】…【用户需求】…` 的顺序拼在提示词最前面（主体描述等用户内容拼完之后再包，保证规则稳定排在最前）。
图片 / 视频任务不注入——那些提示词直接进模型，混入执行规则只会污染画面。两个字段都留空时提示词原样透传。

公开：`GET /skill?category=&keyword=`（仅已上架）、`GET /skill/:id`（使用次数 +1）。

后台管理：`GET /admin/skill` 可选 `source`（缺省全部，`official` 官方 / `user` 用户私有）；私有 Skill（user_id > 0）后台只允许下架（status 2），不允许编辑内容、不允许重新上架、不允许删除（`DELETE /admin/skill/:id` 返回错误）。

### 1.11 prompt 提示词库（运营维护 + 远程源同步）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| title | string(200) | 标题 |
| content | text | 提示词正文，上限 8000 字符。**列表不返回，详情才返回** |
| cover_url / result_url | string(1000) | 封面图、效果图 |
| tags | string(255) | 存储为逗号分隔，响应里是 `string[]` |
| category | string(50) | 分类 |
| source_id / source_name / source_url | | 远程源 ID（0 为后台手工录入）、来源展示名、原文链接 |
| uses | int | 复制次数（`GET /prompt/:id` +1） |
| sort_order | int | 越大越靠前 |
| status | int | 1 已上架 / 2 已下架 |

`prompt_source` 远程源：`name`、`url`（http(s) 抓取地址）、`format`（`markdown` / `json`）、
`tag`（同步进来的条目统一打这个标签）、`enabled`、`last_synced_at`、`last_error`、`count`。

同步规则：按 `external_key`（来源 ID + 标题指纹）upsert——新条目插入，老条目**只更新正文与原文链接**，
运营在后台改过的标题、封面、标签不会被下一轮同步冲掉。`markdown` 按二级/三级标题切段，每段取第一个代码块当正文
（没有代码块就退回段落纯文本），段内第一张图当封面；`json` 期望一个对象数组，字段名兼容
`title|name|topic`、`content|prompt|text`、`cover|cover_url|image`、`tags[]`。
单个源抓取超时 20 秒、最多读 4MB；定时任务每 12 小时跑一轮（启动 5 分钟后先跑一次），
单个源失败只记日志并写进 `last_error`，不影响其他源。删除来源时已同步的提示词保留，只清空它们的 `source_id`。

公开：`GET /prompt?keyword=&tag=&category=&source_id=&page=&page_size=`（仅已上架，不含正文）、
`GET /prompt/tags`（标签云）、`GET /prompt/sources`（来源筛选项）、`GET /prompt/:id`（含正文，复制次数 +1）。

后台管理：`GET /admin/prompt`（含正文，可按 `status` 过滤）、`POST /admin/prompt`、`PUT /admin/prompt/:id`、
`DELETE /admin/prompt/:id`；`GET|POST /admin/prompt-source`、`PUT|DELETE /admin/prompt-source/:id`、
`POST /admin/prompt-source/:id/sync`（手动同步，返回 `{ total, created }`）。

### 1.9 notification 站内通知（发给单个用户，与面向全员的公告 notice 区分）

| 字段 | 类型 | 说明 |
|---|---|---|
| id / user_id | int | |
| type | string | `work` 作品审核 / 下架、`comment` 评论 / 回复、`like` 喜欢、`task` 生成任务、`system` |
| title / content | string | |
| link | string(300) | 站内跳转路径，如 `/settings?tab=works`、`/canvas?id=` |
| read_at | int64 | 0 未读 |

触发点：作品被管理端下架 / 驳回、举报成立、发布审核驳回、生成任务超时退积分；有人评论作品或回复评论（`comment`，自己操作不通知）；作品收到喜欢（`like`，每部作品 24 小时内只发一条）。`GET /user/notification?unread=1` 返回 `{ items, total, unread_count }`；`POST /user/notification/read` `{ ids?: [] }`（不传 ids 全部已读）。

#### 1.9.1 用户通知偏好

表 `user_notification_pref`（user_id 唯一）：`disabled_types` 逗号分隔的已关闭类型，`email_enabled` 邮件通知开关。写通知前先看全局类型开关，再看用户偏好，关闭的类型不写入。邮件同步：`work` 类型（作品审核 / 下架 / 举报成立）且用户开启 `email_enabled` 时，额外异步发一封 SMTP 邮件（未配置 SMTP 或 `EMAIL_VERIFICATION_ENABLED=false` 时仅打印日志）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/notification/prefs` | `{ types: [{ type, label, desc, enabled, globally_off }], email_enabled }`；`globally_off` 表示运营已在后台停用该类型，用户侧不可开启 |
| PUT | `/user/notification/prefs` | `{ disabled_types: [], email_enabled }`，返回同上 |

### 1.7 category 内容分类（运营维护，前台筛选标签）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| name | string(50) | 名称，即作品 / 技能 `category` 字段的取值（按名称关联） |
| scope | string | `work` 成片分类 / `skill` Skill 分类 |
| sort_order | int | 越小越靠前 |
| status | int | 1 启用 / 2 停用 |

同一 scope 内名称唯一。删除分类不影响已使用该名称的作品 / 技能。公开：`GET /category?scope=work|skill`（仅启用，按 sort_order 升序，不分页）。

### 1.8 work_report 作品举报（社区审核）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| work_id / user_id | int | 被举报作品、举报人 |
| reason | string(50) | 违规内容 / 侵权 / 垃圾信息 / 其他 |
| content | string(500) | 补充说明 |
| status | int | 0 待审核 / 1 举报成立 / 2 驳回 |
| remark | string(255) | 处理备注 |

管理端列表附带 `work_title`、`reporter_name`。举报成立时作品自动下架（status=2，remark 记录原因），同一作品其它待审核举报一并结案。同一用户对同一作品只保留一条待审核举报。

### 1.10 work_comment 作品评论（社区）

| 字段 | 类型 | 说明 |
|---|---|---|
| id / work_id / user_id | int | |
| parent_id | int | 一级评论为 0；回复挂在一级评论下（只有一层，回复的回复仍挂同一父级） |
| reply_to_id | int | 回复对象用户 ID，列表附带 `reply_to: { id, nickname }` |
| content | string(500) | |
| status | int | 1 正常 / 2 已隐藏（管理端处理或举报成立） |
| remark | string(255) | 处理备注 |
| reply_count | int | 一级评论的可见回复数 |

列表附带 `author: { id, nickname }`、带登录态时附 `mine`；作品表新增 `comment_count`（可见评论数，含回复）。只有已发布作品可评论；评论人本人或作品作者可删除（一级评论连带回复）。评论举报复用 `work_report`（`target_type=comment`，`comment_id`），举报成立时隐藏评论并通知评论人（通知类型 `comment`）。管理端可隐藏 / 恢复 / 删除评论。

## 2. 路由

### 2.1 用户侧（`middleware.UserAuth`）

| 方法 | 路径 | 说明 | 请求体 / 参数 |
|---|---|---|---|
| GET | `/user/project` | 我的项目列表 | `keyword, page, page_size` |
| GET | `/user/project/trash` | 回收站 | 分页 |
| POST | `/user/project` | 新建 | `{ name, description }` |
| GET | `/user/project/:id` | 详情，返回 `{ project, canvases: [] }` | |
| PUT | `/user/project/:id` | 改名/描述/封面 | `{ name?, description?, cover_url? }` |
| DELETE | `/user/project/:id` | 进回收站（级联其画布） | |
| POST | `/user/project/:id/restore` | 恢复 | |
| GET | `/user/canvas` | 画布列表 | `project_id?, page, page_size` |
| GET | `/user/canvas/trash` | 画布回收站 | 分页，附 `deleted_at` |
| POST | `/user/canvas/:id/restore` | 恢复画布（所属项目在回收站则一并恢复） | |
| POST | `/user/canvas` | 新建 | `{ name, project_id?, graph? }` |
| GET | `/user/canvas/:id` | 读取（含 graph） | |
| GET | `/user/canvas/:id/versions` | 版本历史 | 见 1.2.1 |
| POST | `/user/canvas/:id/versions/:vid/restore` | 回滚到某版本 | 返回画布详情（含 graph） |
| PUT | `/user/canvas/:id` | 保存 | `{ name?, graph?, version, thumbnail_url?, snapshot?, note? }` → 返回 `{ id, version, updated_at }`；`snapshot: true` 单独记一条版本（见 1.2.1） |
| DELETE | `/user/canvas/:id` | 进回收站 | |
| GET | `/user/canvas/:id/chat` | 这张画布的对话历史（不含消息正文） | `{ items: [{ id, title, message_count, created_at, updated_at }] }`，最近更新在前，最多 30 条 |
| POST | `/user/canvas/:id/chat` | 新建一段对话 | `{ title?, messages }`。`messages` 为气泡数组（`role` 为 user/assistant，可带 `text` / `card`）。单段最多 200 条、2MB。进行中的气泡和 `undoToken` 不入库 |
| GET | `/user/canvas/:id/chat/:sid` | 读取一段对话 | 含 `messages` |
| PUT | `/user/canvas/:id/chat/:sid` | 覆盖保存标题和消息 | 请求体同新建 |
| DELETE | `/user/canvas/:id/chat/:sid` | 删除一段对话 | |
| DELETE | `/user/canvas/:id/chat` | 清空这张画布的对话历史 | `{ deleted }` |
| GET | `/user/asset` | 资产列表 | `type?, project_id?, source?, keyword?, page, page_size` |
| POST | `/user/asset/upload` | 上传（multipart） | 字段 `file`，可选 `project_id`；返回 asset |
| DELETE | `/user/asset/:id` | 进回收站（文件保留 30 天） | |
| GET | `/user/asset/trash` | 资产回收站 | 分页 |
| POST | `/user/asset/:id/restore` | 恢复资产 | |
| POST | `/user/self/mobile` | 绑定 / 换绑手机号 | `{ mobile, code }`，验证码走 `GET /sms/verification`；短信注册账号的 username 同步改为新号 |
| GET | `/generation/models` | 可用模型（匿名） | 返回 `[{ provider, model, type, label }]` |
| GET | `/generation/models/detail` | 模型详情（匿名，见 28） | `model?`（兼容 `model_name`）；返回积分定价 + 网关展示元数据 |
| POST | `/user/task` | 创建生成任务 | `{ type, model, prompt, params, inputs, canvas_id?, node_id?, project_id? }` |
| GET | `/user/task` | 任务列表 | `canvas_id?, status?, page, page_size` ，另支持 `keyword`（提示词 / 模型 / 单号）、`active=1`（只看进行中） |
| GET | `/user/task/:id` | 任务详情（前端轮询） | |
| POST | `/user/task/:id/cancel` | 取消（仅 pending/running） | |
| GET | `/user/work` | 我的作品 | `status?, keyword?, page, page_size`（keyword 按标题模糊搜，与公开 `GET /work` 相同） |
| POST | `/user/work` | 从资产发布作品 | `{ title, description, asset_id, project_id?, canvas_id?, cover_url?, category?, show_process? }` → status 1 |
| PUT | `/user/work/:id` | 修改 / 上下架 | `{ title?, description?, cover_url?, category?, status? (1/2), show_process? }` |
| DELETE | `/user/work/:id` | 删除 | |
| POST | `/user/work/:id/like` | 点赞 / 取消点赞（切换） | 返回 `{ liked, like_count }` |
| GET | `/user/work/likes` | 我赞过的作品 | 分页；只含仍在发布中的，按点赞时间倒序，`liked` 恒为 true |
| POST | `/user/work/:id/report` | 举报作品 | `{ reason, content? }`，见 1.8 |
| GET | `/user/task/summary` | 任务中心概览 | `{ total, active, succeeded, failed, cancelled, points_today, points_30d, points_frozen }` |
| POST | `/user/task/:id/retry` | 重试失败 / 已取消的任务 | 按原参数新建任务并重新预扣积分，返回新任务 |
| POST | `/user/work/:id/comment` | 发表评论 / 回复 | `{ content, parent_id? }`，30 次/分钟，返回评论（含 author） |
| DELETE | `/user/comment/:id` | 删除评论 | 本人或作品作者 |
| POST | `/user/comment/:id/report` | 举报评论 | `{ reason, content? }` |
| GET | `/user/skill` | 我的私有 Skill | `keyword?` |
| POST / PUT / DELETE | `/user/skill`、`/user/skill/:id` | 新建 / 修改 / 删除私有 Skill | `{ title, prompt, description?, category?, cover_url?, scene?, howto?, output?, canvas_id? }`，prompt 中 `{{prompt}}` 为灵感占位；`canvas_id` 把该画布（去敏后的 graph）存为模板，传 0 清除 |
| GET | `/user/skill/:id` | 我的 Skill 详情（含 `graph`），官方已上架技能也可读 | |
| GET | `/user/skill/favorites` | 我的收藏 | `keyword? / category?` |
| POST | `/user/skill/:id/favorite` | 收藏 / 取消收藏（切换） | 返回 `{ favorited, stars }` |
| POST | `/user/self/deactivate` | 自助注销 | `{ password }`（有密码的账号必填）。管理员不可自助注销；团队所有者需先移除其他成员。动作：作品下架、画布分享关闭、退出团队、邮箱 / 手机号 / 第三方绑定改为 `deleted-<id>-…` 释放、昵称改「已注销用户」、令牌失效、软删除。积分与会员作废不退款 |
| GET | `/user/notification` | 站内通知 | `unread=1?`，见 1.9 |
| POST | `/user/notification/read` | 标记已读 | `{ ids?: [] }` |
| GET | `/user/asset/:id` | 单个资产 | |

### 2.2 公开

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/banner` | 首页轮播（上架且在生效期内，按 sort_order 倒序，最多 10 条） |
| GET | `/notice`、`/notice/:id` | 已发布公告（置顶优先），分页 |
| GET | `/work` | 作品信息流（已发布，精选优先），`keyword, category, featured=1, user_id, page, page_size` |
| GET | `/author/:id` | 作者主页：`{ id, nickname, joined_at, work_count, like_total, view_total, mine }` |
| GET | `/category` | 分类标签，`scope=work\|skill`（默认 work） |
| GET | `/work/:id` | 作品详情，浏览数 +1 |
| GET | `/generation/models` | 可用模型 |
| GET | `/skill`、`/skill/:id` | 官方技能模板（带登录态附 `favorited`） |
| GET | `/work/:id/process` | 作品制作过程（作者公开时） |
| GET | `/work/:id/comments` | 评论列表：一级评论分页（最新在前），每条附最多 3 条回复；带登录态附 `mine` |
| GET | `/comment/:id/replies` | 某条评论的全部回复（最早在前），分页 |
| GET | `/upload/*` | 本地存储静态文件（换 OSS 后废弃） |

### 2.3 管理侧（`middleware.AdminAuth`，role ≥ 10）

以下操作仅超级管理员（`middleware.RootAuth`，role ≥ 100）可用，普通管理员调用返回 `权限不足`：系统设置读写、用户注销 / 换绑与额度调整、提现审核/打款、订单退款与手动标记已支付、积分调整、会员计划/积分包/促销/模型定价的增删改。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/admin/user` | 用户列表，`keyword?, status?, deleted? (1 查已注销软删账号)`；缺省不含软删记录 |
| GET | `/admin/user/:id` | 用户详情（脱敏） |
| DELETE | `/admin/user/:id` | 注销用户（软删；不可删管理员） |
| PUT | `/admin/user/:id/status` | `{ status: 1 \| 2 }` 启用/禁用（原有） |
| PUT | `/admin/user/:id/identity` | `{ identity }` 运营身份：`easytv_official` / `enterprise` / `easytv_teacher` / `easytv_teaching_assistant`；空 / `__none__` 去掉身份。普通管理员不能改管理员账号 |
| PUT | `/admin/user/:id/bind` | RootAuth。`{ mobile?, wechat_unbind? }` 换绑手机 / 解绑微信；不走短信。短信注册账号不能清空手机号；至少保留一种登录方式。`mobile` 变更时若 username 是手机号或 `wx_` 前缀则同步 username |
| POST | `/admin/user/:id/console_access` | 控制台（代登创作台）：返回 `{ ticket, path, expires_in }`，见第 19 节 |
| GET | `/admin/project` | 全站项目，`user_id?, keyword?` |
| GET | `/admin/canvas` | 全站画布（不含 graph），`user_id?, project_id?` |
| GET | `/admin/asset` | 全站资产，`user_id?, type?` |
| GET | `/admin/task` | 全站任务，`user_id?, status?, type?` |
| PUT | `/admin/project/:id/status` | `{ status: 1 \| 2 }` 恢复 / 移入回收站（级联画布） |
| DELETE | `/admin/project/:id` | 物理删除（含画布） |
| PUT | `/admin/canvas/:id/status` | `{ status: 1 \| 2 }` 恢复 / 移入回收站 |
| DELETE | `/admin/canvas/:id` | 物理删除 |
| PUT | `/admin/asset/:id/status` | `{ status: 1 \| 2 }` 恢复 / 移入回收站 |
| DELETE | `/admin/asset/:id` | 物理删除并清理文件 |
| GET | `/admin/work` | 全站作品（含所有状态），`user_id?, status?, keyword?, category?, featured?, review_status?`；`featured=1` 时按 sort_order 升序 |
| PUT | `/admin/work/:id` | 编辑：`{ title?, description?, category?, cover_url?, featured? (0/1), sort_order? }`（精选成片页用） |
| PUT | `/admin/work/:id/status` | `{ status, remark? }` 上下架 |
| PUT | `/admin/work/:id/review` | 发布审核：`{ status: 1 通过 \| 2 驳回, remark? }`，驳回后作品 status=-1，通过会恢复被驳回的作品 |
| GET | `/admin/work-report` | 举报列表，`status?, target_type? (work\|comment), keyword?`，待审核优先；评论举报附 `comment_id`、`comment_content` |
| PUT | `/admin/work-report/:id/status` | 处理举报：`{ status: 1 成立 \| 2 驳回, remark? }`；作品举报成立即下架作品，评论举报成立即隐藏评论 |
| GET | `/admin/comment` | 评论列表，`status? (1\|2), work_id?, user_id?, keyword?`，附 author 与 work_title |
| PUT | `/admin/comment/:id/status` | `{ status: 1 恢复 \| 2 隐藏, remark? }`，隐藏时通知评论人 |
| DELETE | `/admin/comment/:id` | 删除评论（一级评论连带回复） |
| GET / POST / PUT / DELETE | `/admin/category`、`/admin/category/:id` | 分类：name / scope / sort_order / status；列表 `scope?, keyword?, status?`，不分页 |
| DELETE | `/admin/work/:id` | 删除作品 |
| GET / POST / PUT / DELETE | `/admin/banner`、`/admin/banner/:id` | 轮播 Banner：title / subtitle / tag / cover_url / video_url / link_url / cta / sort_order / status / starts_at / ends_at |
| GET / POST / PUT / DELETE | `/admin/notice`、`/admin/notice/:id` | 公告：title / content / type(system\|activity) / pinned / status |
| GET | `/admin/notification/types` | 通知类型候选（配置全局开关的下拉来源）：`{ types: [{ type, label, desc, enabled, globally_off }] }`；全局开关写入 `PUT /admin/setting/:key`（key=`NotificationTypesEnabled`，逗号分隔） |
| GET | `/admin/skill` | 技能模板列表，`category?, keyword?, status?, source?(official\|user)` |
| POST | `/admin/skill` | 新建（官方，user_id=0），字段见 1.6 |
| PUT | `/admin/skill/:id` | 修改（含 `status` 上下架；私有 Skill 只能下架） |
| DELETE | `/admin/skill/:id` | 删除 |

管理端列表的项目/画布/资产附带 `owner_name`（作者昵称）与 `status`（1 正常 / 2 回收站），画布另附 `node_count`；`?status=1|2` 可按回收站过滤。

## 3. 前端页面 ↔ 接口

| 页面 | 接口 |
|---|---|
| web `/projects` | `GET /user/project`、`DELETE /user/project/:id`、`GET /user/project/trash`、`POST …/restore` |
| web 画布「分享」/ `/share/:token` | `GET/PUT /user/canvas/:id/share`、`GET /share/canvas/:token`（只读渲染）、`POST /user/share/canvas/:token/copy` |
| web `/canvas?id=` | `GET /user/canvas/:id`、`PUT /user/canvas/:id`（自动保存；Ctrl+S / 保存按钮带 `snapshot: true`）、无 id 首次保存走 `POST /user/canvas`；「历史」按钮 `GET /user/canvas/:id/versions` + `POST …/versions/:vid/restore` |
| web 画布对话栏 | `GET /generation/models`（能力列表、文本模型）、`POST /user/task`（type=text）+ 轮询 `GET /user/task/:id`；会话与历史走 `GET/POST/PUT/DELETE /user/canvas/:id/chat`（见上表），按当前用户和画布保存 |
| web `/settings` 我的作品 | `GET /user/work`（含 status / remark）、`PUT /user/work/:id`（status 1/2）、`DELETE /user/work/:id` |
| web `/assets` | `GET /user/asset`、`POST /user/asset/upload`、`DELETE /user/asset/:id` |
| web 画布 Inspector「生成」 | `GET /generation/models` → `POST /user/task` → 轮询 `GET /user/task/:id` → 结果写回节点 |
| web `/studio` 信息流、`/watch/:id` | `GET /work`、`GET /work/:id`、`GET /category?scope=work`、`POST /user/work/:id/report` |
| web `/agent` Skill / 收藏 / 我的 | `GET /skill`（附 favorited）、`GET /user/skill/favorites`、`GET/POST/PUT/DELETE /user/skill`、`POST /user/skill/:id/favorite`；「加入创作台」跳 `/canvas?skill=&prompt=&asset=`，画布用 `GET /skill/:id` 的模板生成文本节点、`GET /user/asset/:id` 放素材节点 |
| web `/watch/:id` 评论 | `GET /work/:id/comments`、`GET /comment/:id/replies`、`POST /user/work/:id/comment`、`DELETE /user/comment/:id`、`POST /user/comment/:id/report` |
| manage `/community-audit` 评论 | `GET /admin/comment`、`PUT /admin/comment/:id/status`、`DELETE /admin/comment/:id`；举报列表 `target_type` 区分作品 / 评论 |
| web `/watch/:id` 查看制作过程 | `GET /work/:id/process`（发布时带 `canvas_id`，`show_process` 可关） |
| web `/settings` 注销账号 | `POST /user/self/deactivate`；协议页 `/legal/terms` `/legal/privacy` `/legal/ai-guidelines` 为前端静态内容 |
| web `/settings` 额度卡片、团队用量 | `GET /user/usage`、`GET /user/team/usage` |
| web `/settings?tab=notify` 通知偏好 | `GET/PUT /user/notification/prefs` |
| web 消息中心 | `GET /notice` + `GET /user/notification`（打开即 `POST /user/notification/read` 全部已读）；侧栏铃铛角标为未读数 |
| web `/agent` 分类标签、发布弹窗分类 | `GET /category?scope=skill`、`GET /category?scope=work`（接口失败回退前端内置标签） |
| manage `/showcase` 精选成片 | `GET /admin/work?featured=1`、`PUT /admin/work/:id`（featured / sort_order / category / cover）、`PUT /admin/work/:id/status`；「加入精选」从 `GET /admin/work?status=1` 挑作品 |
| manage `/community-audit` 社区审核 | 作品：`GET /admin/work?review_status=`、`PUT /admin/work/:id/review`；举报：`GET /admin/work-report`、`PUT /admin/work-report/:id/status` |
| manage `/category` 内容分类 | `GET/POST/PUT/DELETE /admin/category` |
| web `/agent`、画布对话面板 | `GET /skill` |
| manage `/user` | `GET /admin/user`、`GET /admin/user/:id`、`PUT /admin/user/:id/status`、`PUT /admin/user/:id/identity`、`PUT /admin/user/:id/bind`、`POST /admin/user/:id/console_access`、`DELETE /admin/user/:id` |
| manage `/project` `/canvas` `/asset` `/work` `/skill` | 对应 `/admin/*` 列表 + status / delete |

## 4. 环境变量（新增）

| 变量 | 说明 |
|---|---|
| `UPLOAD_DIR` | 本地上传目录，默认 `./upload` |
| `UPLOAD_BASE_URL` | 上传文件对外前缀，默认空（即由本服务 `/upload/` 提供） |
| `UPLOAD_MAX_MB` | 单文件上限，默认 200 |
| `OSS_BUCKET` / `OSS_ENDPOINT` / `OSS_ACCESS_KEY_ID` / `OSS_ACCESS_KEY_SECRET` | 阿里云 OSS；配置 `OSS_BUCKET` 后上传与生成产物直接写 OSS，`url` 为公网地址，本地 `/upload/` 不再使用 |
| `OSS_BASE_URL` / `OSS_PREFIX` / `OSS_PUBLIC_READ` | 对外访问前缀（CDN 或自定义域名，默认 `https://<bucket>.<endpoint>`）、对象前缀（默认 `easytv`）、是否带 public-read ACL（默认 true） |
| `FREE_CONCURRENT` / `FREE_STORAGE_GB` | 未开通会员的并发路数与云存储 GB（默认 1 / 2），见 6.9 |
| `TASK_PENDING_TIMEOUT_MINUTES` / `TASK_RUNNING_TIMEOUT_MINUTES` | 任务超时阈值（默认 15 / 30 分钟），超时任务由后台标失败并退回预扣积分 |
| `EASYAPI_KEY` | 网关令牌，**已迁移为 DB setting `EasyAPIGatewayKey`**（`/admin/gateway/config` 热更新）；env 仅作首次启动回退。缺省时 `GET /generation/models` 为空，所有生成返回"暂未接入" |
| `EASYAPI_BASE_URL` | 网关地址，**已迁移为 setting `EasyAPIGatewayBaseUrl`**，默认 `https://api.easyapi.com` |
| `EASYAPI_TEXT_MODELS` / `EASYAPI_IMAGE_MODELS` / `EASYAPI_VIDEO_MODELS` / `EASYAPI_AUDIO_MODELS` | **已废弃**：模型清单统一由 `model_price` 表管理（`/admin/gateway/fetch-models` 从网关拉取 + `/admin/gateway/models/import` 导入）；env 值仅在 `model_price` 为空时补种一次 |
| `UPLOAD_BASE_URL` | 素材对外地址前缀，**已迁移为 setting `UploadBaseUrl`**；图生视频素材需公网可访问（OSS `OSS_BASE_URL` / 七牛 `QiniuDomain` 已配则满足） |
| 七牛（可选） | setting `QiniuAccessKey`/`QiniuSecretKey`/`QiniuBucketName`/`QiniuProjectCode`/`QiniuDomain`，AK/SK 非空即启用：服务端存储切七牛 + 前端直传凭证 `GET /user/qiniu/upload-token`、`GET /user/qiniu/key` |
| 定价换算 setting | `EasyAIPricingUsdRate`（汇率 7.3）/ `EasyAIPricingPartnerDiscount`（合作折扣，缺省 1）/ `EasyAIPricingMarkupRate`（毛利 3）/ `EasyAIPricingPointsRate`（积分/元 20），管理端 `/admin/gateway/config` 可改；按量模型假设用量 `EasyAIPricingTextInputTokens` 等见 docs/ops |

## 5. 测试数据

`go run ./cmd/seed` 把创作台原本写死的 14 部成片、20 个 Skill、1 个示例项目/画布、6 个资产灌入库中，幂等可重复执行；演示账号 `demo@easytv.ai / demo123456`（`SEED_USER` / `SEED_PASSWORD` 可覆盖）。

## 6. 付费域（会员 / 积分 / 订单）——2026-09-08 已实现（后端 + 定价页 + 管理端三页）

> 目标：定价页真下单、生成任务真扣积分、管理端真看订单。复用已有的微信 Native / 支付宝支付通道与 webhook，不引入新支付 SDK。
> 数值全部以 `easytv-web/app/utils/pricing-plans.ts` 现有定价为准，seed 时灌入。

### 6.0 核心规则

1. **积分是唯一计价单位。** 生成任务按模型定价扣积分，不再用 easyapi 遗留的 `quota`（余额）；`quota`/`recharge` 保留给提现等旧功能，不参与创作计费。
2. **积分按批次管理，到期作废。** 每笔入账是一个"批次"（来源 + 数量 + 过期时间），消耗时按**最早过期优先**扣减；余额 = 未过期批次剩余之和。
3. **会员 = 每月发积分 + 会员价。** 开通即发首月积分（31 天有效），之后每月发放日由后台任务补发；会员有效期内生成按 `member_points_per_unit` 计价，积分包按 `member_price` 售卖。
4. **任务先预扣、后结算。** 创建任务时按参数估算并扣减积分（一期"冻结"即扣减，记 `freeze` 流水）；成功写 `settle` 流水（金额 0，估算即结算）；失败/取消以一个新批次退回（`refund` 来源，365 天有效，`unfreeze` 流水），退回幂等。
5. **订单号即支付单号。** `order.order_no` 直接作为 `recharge.trade_no` 下单，webhook 成功回调后先查订单：有订单则履约（发会员/发积分），无订单才走旧的加 quota 逻辑。
6. **金额单位元，两位小数；服务端重新计价，前端展示的报价仅供参考**，下单响应里的 `price` 才是应付金额。

### 6.1 实体

#### membership_plan 会员计划（运营维护）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| code | string(20) | `starter` / `creator` / `pro` / `flagship`，唯一，前端选择时传 code |
| name | string(50) | 入门 / 创作 / 专业 / 旗舰 |
| featured | bool | 推荐标 |
| monthly_origin / monthly_price | decimal(10,2) | 月付原价 / 月付现价（创作版单席） |
| monthly_points | int | 每月发放积分（单席） |
| add_on_points / add_on_price | int / decimal | 加购积分与价格（单席，随会员单一次性购买） |
| concurrent | int | 并发生成路数 |
| storage_gb | int | 云存储 GB |
| sort_order | int | |
| status | int | 1 上架 / 2 下架 |
| created_at / updated_at | int64 | |

#### point_pack 积分包（运营维护）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| code | string(20) | `p30` / `p90` / `p365` / `p730` |
| points | int | |
| valid_days | int | 30 / 90 / 365 / 730 |
| origin / member_price | decimal(10,2) | 原价 / 会员价（非会员按 origin 结算） |
| featured | bool | |
| sort_order | int | |
| status | int | 1 上架 / 2 下架 |

#### 促销与团队配置（`setting` 表，key = `BillingPromo`，JSON）

```json
{ "enabled": true, "title": "年会员最低 6.2 折", "subtitle": "…", "ends_at": 1789142400,
  "cycle_rate": { "month": 1, "quarter": 0.85, "year": 0.62 },
  "cycle_off_label": { "quarter": "8.5 折", "year": "6.2 折" },
  "team_seat_min": 2, "team_seat_max": 10, "team_seat_rate": 0.9 }
```
`ends_at` 过期后 `cycle_rate` 全部按 1 计价，前端隐藏倒计时。

#### model_price 模型积分定价（运营维护）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| model | string(100) | 模型标识（**与网关可用模型一致**；模型清单单一数据源） |
| type | string | text / image / video / audio |
| label | string(100) | 展示名，如「Seedance 2.0 · 720P」 |
| unit | string | `second`（视频按秒）/ `image`（按张）/ `call`（按次，文本与语音） |
| points_per_unit | int | 标准价 |
| member_points_per_unit | int | 会员价，0 表示同标准价 |
| status | int | 1 生效 / 2 停用 |

估算规则：视频 `duration × count × 单价`；图片 `count × 单价`；文本、语音 `count × 单价`（count 缺省 1）。**创建任务时 count 只能是 1**（网关一次只落一个产物，见 11.1），批量由客户端拆成多条任务。**没有定价的模型不允许创建任务**（返回「模型未定价」）。

#### order 订单

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| order_no | string(32) | `ET` + 日期 + 序号，唯一，**同时作为 recharge.trade_no** |
| user_id | int | |
| kind | string | `membership` / `points` |
| plan_id / pack_id | int | 二选一 |
| edition | string | `creator` / `team`（仅会员单） |
| cycle | string | `month` / `quarter` / `year`（仅会员单） |
| months | int | 1 / 3 / 12 |
| seats | int | 创作版恒 1；团队版 2–10 |
| add_on | bool | 是否加购积分 |
| title | string(200) | 快照：「创作会员 · 创作 · 按年购买」「6000 积分 · 有效期 1 年」 |
| origin / price | decimal(10,2) | 原价 / 应付（服务端计算） |
| points | int | 本单履约总积分（会员单 = 首月积分 + 加购；积分包 = points） |
| valid_days | int | 积分包有效期；会员单为 0 |
| status | string | `pending` 待支付 → `paid` 已支付 / `closed` 已关闭（超时或用户取消）/ `refunded` 已退款 |
| payment_method | string | `wechat_native` / `alipay_f2f` / `alipay_pc` |
| paid_at / closed_at / refunded_at | int64 | |
| refund_reason / remark | string(255) | |
| created_at / updated_at | int64 | |

计价公式（与 `quoteMembership` 一致）：
```
seat_count = edition == team ? seats : 1
team_rate  = edition == team ? promo.team_seat_rate : 1
origin     = monthly_origin × months × seat_count
price      = round(monthly_price × months × cycle_rate[cycle] × seat_count × team_rate)
add_on     = round(add_on_price × seat_count × team_rate)      // 勾选加购时并入 price
积分包      = 会员有效期内取 member_price，否则 origin
```

#### membership 用户会员状态（每用户一条当前记录）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| user_id | int | 唯一 |
| plan_id / plan_code / plan_name | | 快照 |
| edition / seats | | |
| monthly_points | int | 每月发放（已乘席位） |
| started_at / expires_at | int64 | |
| next_grant_at | int64 | 下次发积分时间；后台任务每小时扫描 `next_grant_at <= now AND status = active` |
| status | string | `active` / `expired` |
| source_order_id | int | 最近一次开通/续费的订单 |
| created_at / updated_at | int64 | |

履约规则：同计划续费 → `expires_at += months`，`next_grant_at` 不变；不同计划 → 新计划从支付时刻起算，旧计划立即失效并记日志（一期不折算差价）；过期由后台任务置 `expired`。

#### point_lot 积分批次

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| user_id | int | |
| source | string | `membership_grant` 会员月发 / `add_on` 加购 / `pack` 积分包 / `gift` 新用户赠送 / `admin` 后台调整 / `refund` 任务退回 |
| order_id / task_id | int | 来源 |
| points | int | 入账数量 |
| remaining | int | 剩余 |
| expires_at | int64 | 会员月发 31 天；加购随会员到期；积分包按 valid_days；后台调整默认 365 天 |
| created_at | int64 | |

#### point_log 积分流水（只增不改）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | int | |
| user_id | int | |
| type | string | `grant` 入账 / `freeze` 任务预扣 / `settle` 结算 / `unfreeze` 退回 / `expire` 过期作废 / `adjust` 后台调整 |
| amount | int | 正入负出 |
| balance_after | int | 变动后可用余额 |
| biz_type / biz_no | string | `order` + order_no / `task` + task_no / `admin` + 管理员 id / `system` |
| reason | string(255) | 「开通创作会员到账」「视频生成 kling-v1 5s」 |
| operator_id | int | 后台调整时的管理员 id，其余 0 |
| created_at | int64 | |

生成任务表补两列：`points_frozen`（预扣）与已有的 `cost_points`（结算）。

### 6.2 路由

#### 公开

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/billing/plans` | `{ plans: [], packs: [], promo: {} }`，只含上架项；定价页用 |
| GET | `/billing/model-prices` | 生效中的模型定价；定价页对比表与 Inspector 的积分预估用 |
| POST | `/billing/quote` | 报价（可匿名；带登录态时积分包按会员价）。请求同下单，响应 `{ origin, price, points, title, months, add_on_points, add_on_price }` |

#### 用户侧（UserAuth）

| 方法 | 路径 | 说明 | 请求 / 响应 |
|---|---|---|---|
| GET | `/user/wallet` | 钱包总览 | `{ balance, frozen, expiring_in_7d, membership: {plan_code, plan_name, edition, seats, expires_at, next_grant_at} \| null }` |
| GET | `/user/points/log` | 积分流水 | `type?, page, page_size` |
| GET | `/user/points/lots` | 未过期批次（前端展示"XX 积分将于 X 日到期"） | |
| POST | `/user/order` | 下单并发起支付 | 请求 `{ kind, plan_code?, pack_code?, edition?, cycle?, seats?, add_on?, payment_method }`；响应 `{ order, quote, pay: { payment_method, code_url \| payment_url, trade_no, expires_at } \| null, pay_error? }`。`payment_method` 支持 `wechat_native` / `alipay_f2f`（返回 `code_url` 二维码，前端 qrcode 渲染）/ `alipay_pc`（电脑网站支付，返回 `payment_url` 跳转链接）；渠道未开通时 `pay` 为 null、订单保留 pending |
| POST | `/user/order/:id/pay` | 对待支付订单换渠道重新发起支付 | `{ payment_method }` |
| GET | `/user/order` | 我的订单 | `status?, page, page_size` |
| GET | `/user/order/:id` | 订单详情（支付页 3 秒轮询直到 paid/closed） | |
| POST | `/user/order/:id/close` | 用户取消待支付订单 | |
| GET | `/user/membership` | 当前会员（同 wallet.membership，便于单独刷新） | |

生成任务相关变化：`POST /user/task` 响应增加 `points_frozen`；余额不足返回 `积分不足，还需 N 积分`；未定价模型返回 `模型 X 未定价`；`GET /generation/models` 每个模型附带 `unit / points_per_unit / member_points_per_unit / priced`。

#### 支付回调（已有）

`POST /wechat/webhook`、`POST /alipay/webhook` 不变。`service.CompleteRecharge` 改为：置 recharge 成功 → 按 `trade_no` 查 `order` → 有则调用 `service.FulfillOrder`（幂等：仅 pending 可履约）→ 无则走旧逻辑加 quota。

补充：旧充值通道 `POST /user/pay` 现支持 `payment_method=alipay_pc`（电脑网站支付，返回 `data.payment_url`）与 `team_id`（团队充值，须为该团队 owner，完成后计入团队钱包 = owner 积分池，见 6.7）。新增 `POST /user/recharge/status`（body `{ trade_no }`）：主动向渠道查单（支付宝 `alipay.trade.query` / 微信 Native 查单），已支付则补入账（防 webhook 漏单），返回 `{ status: success | pending }`。支付宝走证书模式（需配置 `AlipayAppCertPublicKey`、`AlipayRootCert`）。

#### 管理侧（AdminAuth）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / POST / PUT / DELETE | `/admin/plan`、`/admin/plan/:id` | 会员计划 CRUD（下架用 PUT status） |
| GET / POST / PUT / DELETE | `/admin/points-pack`、`/admin/points-pack/:id` | 积分包 CRUD |
| GET / PUT | `/admin/billing/promo` | 促销与团队配置（写 setting `BillingPromo`） |
| GET / POST / PUT / DELETE | `/admin/model-price`、`/admin/model-price/:id` | 模型定价；GET 支持 `page?`, `page_size?`(默认10上限100)，`sort_by?`(model/type/unit/points_per_unit)，`order?`(asc/desc) |
| GET | `/admin/order` | `kind?, status?, user_id?, keyword?(order_no/用户)`，附带 `user_name` |
| POST | `/admin/order/:id/close` | 关闭待支付订单 |
| POST | `/admin/order/:id/mark-paid` | 线下收款 / 联调：手动标记已支付并履约 |
| POST | `/admin/order/:id/refund` | `{ reason }`：先按订单支付渠道调微信退款（`refunds/domestic`）或支付宝 `alipay.trade.refund` 全额退款，渠道受理后再标记 refunded、回收未消耗积分、终止来源会员，并把支付单置为已退款；`manual` 订单不调渠道。返回 `{ revoked_points, refund_no, channel }` |
| GET | `/admin/wallet` | 每用户一行：`user_id, user_name, mobile, balance, frozen, plan_name, expires_at`，`keyword?` |
| POST | `/admin/wallet/:user_id/adjust` | `{ delta, reason, valid_days? }`：正数新增批次，负数按最早过期扣减；写 log `operator_id` = 当前管理员 |
| GET | `/admin/points/log` | 全站流水，`user_id?, type?, biz_type?` |

### 6.7 团队版会员

| 表 | 字段 |
|---|---|
| team | id, owner_id（唯一）, name, seats, created_at, updated_at |
| team_member | team_id, user_id（唯一：一人只属一个团队）, role(owner/member), joined_at |
| team_invite | team_id, code（8 位，唯一）, created_by, expires_at（默认 30 天）, max_uses, uses |

规则：购买团队版会员即成为 owner，团队席位 = 订单 seats；成员凭邀请码加入；owner 的团队版会员有效期内，成员的生成任务从 owner 的积分池扣减和退回（`generation_task.payer_id`），并按会员价计费；会员过期或退款后成员回到各自钱包。`GET /user/wallet` 对成员附带 `team_pool: { owner_id, balance }`。

> 2026-09-14 变更：只有**团队空间**（请求头 `EasyTV-Team`）里的生成才花团队积分池，个人空间一律花自己的钱包；`team_pool` 已移除，改为团队空间下的 `team` 块。见 20。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/team` | 我的团队：`{ id, name, owner_id, seats, used_seats, role, members[], active, expires_at, pool_balance, invite_code(仅 owner) }`，不在团队为 null |
| PUT | `/user/team` | `{ name }` 改名（owner） |
| POST | `/user/team/invite` | 重新生成邀请码（旧码失效，owner） |
| POST | `/user/team/join` | `{ code }` 加入 |
| POST | `/user/team/quit` | 退出（owner 不可退出） |
| DELETE | `/user/team/member/:uid` | 移除成员（owner） |

### 6.9 套餐额度（并发与云存储）

计划字段 `concurrent`（并发路数）、`storage_gb`（云存储）在创建任务与上传资产时硬限制：

- 创作版：按用户本人统计进行中任务数与资产总字节数。
- 团队版：并发 = 计划并发 × 席位、存储 = 计划存储 × 席位，团队全体成员共享，用量一起计入。
- 未开通会员：`FREE_CONCURRENT`（默认 1）、`FREE_STORAGE_GB`（默认 2）。
- 2026-09-14 起付费主体按请求空间定：个人空间看本人的会员，团队空间看团队 owner 的会员（见 20）。
- 超限时 `POST /user/task` / `POST /user/task/:id/retry` 返回「并发已达上限…」，并额外带 `data: { reason: "concurrency", limit, used }`——**并发满属于排队，不是任务失败**，前端应等进行中的任务结束后重试，不要把节点标成失败（成片向导整层提交时会撞到）。`POST /user/asset/upload` 返回「云存储空间不足…」。生成产物落库不受存储限制（已计费），但计入后续用量。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/usage` | `{ plan:{ name, edition, seats, is_member, scope }, concurrent:{ used, limit }, storage:{ used_bytes, limit_bytes } }` |
| GET | `/user/team/usage` | 团队版：`{ members:[{ user_id, nickname, role, tasks_30d, points_30d, storage_bytes, active_tasks }], totals, concurrent, storage, pool_balance }`，非团队版返回失败 |

### 6.8 管理端概况

`GET /admin/stats` 返回 `users{total,today,members}`、`content{works_published,projects,canvases,assets,skills}`、`tasks{total,today,running,succeeded,failed}`、`points{consumed_today,consumed_month,granted_month}`、`orders{paid_today,money_today,paid_month,money_month,pending,refunded_month}`、`daily[14]{day,users,tasks,money}`。

### 6.3 状态机与后台任务

- 订单：`pending` —支付成功→ `paid`；`pending` —30 分钟超时 / 用户取消 / 后台关闭→ `closed`；`paid` —后台退款→ `refunded`。
- 会员：`active` —到期→ `expired`；续费从 `expired` 回到 `active`。
- 积分：入账写批次 + `grant` 日志；任务创建 `freeze`（批次 remaining 不动，钱包 `frozen` 增）；成功 `settle`（真正从批次扣，`frozen` 减）；失败 `unfreeze`。
- 后台任务（`service.RunBillingJobs`，每 10 分钟）：关闭超时订单、会员过期、会员月发积分、过期批次清零写 `expire` 日志。
- 任务超时（`service.ExpireStaleTasks`，每 2 分钟）：pending 超过 15 分钟或 running 超过 30 分钟的任务置 `failed`（error_msg「生成超时」），预扣积分退回；前端轮询看到 failed 即按失败处理。

### 6.4 前端页面 ↔ 接口

| 页面 | 接口 |
|---|---|
| web `/pricing` | `GET /billing/plans`、`GET /billing/model-prices`、`POST /billing/quote`；确认弹窗改为 `POST /user/order` → 展示二维码/跳转 → 轮询 `GET /user/order/:id` → 成功后刷新 `GET /user/wallet` |
| web 创作台顶栏「积分钱包」 | `GET /user/wallet`，点开显示余额、会员、`GET /user/points/log` |
| web 画布 Inspector 额度角标 | 按 `GET /generation/models` 附带的单价 × 参数实时估算，替换现在写死的 135/18/12/6 |
| manage `/plan` | `/admin/plan`、`/admin/points-pack`、`/admin/billing/promo`（页面已有三块，字段名与 AdminMembershipPlan / AdminPointsPack / AdminPromoConfig 一一对应） |
| manage `/order` | `/admin/order` + close / refund |
| manage `/wallet` | `/admin/wallet` + adjust、`/admin/points/log` |
| manage `/model-price` | `/admin/model-price` CRUD，并对比 `GET /generation/models` 提示未定价模型 |
| manage `/banner` `/notice` | `/admin/banner`、`/admin/notice` CRUD |
| web 首页轮播、消息中心 | `GET /banner`、`GET /notice` |

### 6.5 seed 与迁移

- seed 灌入 4 个会员计划、4 个积分包、`BillingPromo`、7 条模型定价（与 `.env` 示例模型对应，模型标识改为网关实际名后请同步改定价）。
- 演示账号赠送 1000 积分（source = gift，365 天）。
- `EASYTV_LIVE_DB=1 go test ./tests -run TestLiveBillingFlow` 在真实库上跑一遍履约/预扣/退款闭环。
- 已有的 `quota` 不迁移。

## 7. 增长域（迭代一，2026-09-14 ~ 09-27）——接口约定

对应任务单 T1 ~ T20（详见「EasyTV 增长地基任务单」）。所有奖励数值读 `setting` 表，键名与默认值集中在 `setting/growth_setting.go`（`GrowthXxx`），管理端「增长」面板可热更新；积分发放统一走 `service.GiftPointsWithBiz(userId, points, validDays, bizNo, reason)`：`point_log.biz_no` 记 `setting.GrowthReason*` 固定键（礼包 `signup_gift`、任务 `task_<key>`、签到 `checkin`…），`reason` 记给用户看的中文。新手任务：`first_*` 事件首次写入时由 `service.TrackOnce` 自动完成对应任务并发积分（`service/growth.go`），业务代码只需打事件。时间戳统一 Unix 秒；防刷用请求头 `EasyTV-Device`（前端生成的设备指纹，CORS 已放行）与客户端 IP。`POST /track` 请求体可额外带 `device`（页面卸载时 `sendBeacon` 无法带自定义头，服务端以头优先、其次取 body）。

### 7.1 实体（新增表 / 字段）

| 表 | 字段 | 说明 |
|---|---|---|
| `track_event`（T1） | `id, user_id, anon_id varchar(32), name varchar(40), props json, source varchar(40), medium varchar(40), campaign varchar(64), aff_code varchar(32), bonus_code varchar(32), ip varchar(45), device varchar(64), created_at` | 索引 `(name, created_at)`、`(user_id)`、`(anon_id)` |
| `user` 新增（T1 / T12） | `source, medium, campaign, bonus_code`（varchar），`invite_blocked tinyint` | 注册时从 `attr` 写入；`inviter_id`、`aff_code` 已有，`aff_code` 改为 6 位大写字母数字且保证唯一 |
| `user_task`（T7） | `id, user_id, task_key varchar(32), status tinyint, points, done_at, created_at` | 唯一 `(user_id, task_key)`；task_key 见 `setting.GrowthTaskKey*` |
| `checkin`（T10） | `id, user_id, day date, streak int, points int, created_at` | 唯一 `(user_id, day)`，按 Asia/Shanghai 取日 |
| `work` 新增（T5） | `source_canvas_id, source_skill_id, open_source tinyint, clone_count int` | 发布时携带；来源画布必须属于发布者，`source_skill_id` 必须是官方或自己的 Skill；`open_source=1` 必须有来源画布（未传时默认取 `canvas_id`）；已实现 |
| `bonus_code`（T15） | `id, code varchar(32) 唯一（比较时忽略大小写）, kind tinyint(1 首充加赠 2 定额积分 3 会员天数), partner_name, partner_user_id, bonus_points, first_recharge_rate decimal(5,4), first_recharge_cap_points, reward_valid_days, member_days, plan_code, max_claims, claimed_count, starts_at, expires_at, status, remark, created_at, updated_at` | |
| `bonus_code_claim`（T15） | `id, user_id, bonus_code_id, partner_user_id, bind_result tinyint, bonus_points, reward_rate, reward_cap_points, reward_expire_at, reward_status tinyint, reward_points, rewarded_at, created_at` | 唯一 `(user_id)`：每个用户只能领一次口令；`bind_result` 1 NEW_BIND / 2 SAME_OWNER；`reward_status` 0 待生效 1 已发 -1 过期 -2 不符合 |
| `model_price` 新增（T18） | `promo_points_per_unit, promo_label varchar(20), promo_starts_at, promo_ends_at` | 有效期内取 `min(标准价/会员价, 促销价)`；`promo_points_per_unit=0` 无促销，`starts_at=0` 立即、`ends_at=0` 不限；已实现（`model.ModelPrice.EffectiveUnitPoints`，`EstimateTaskPoints` 扣费按此） |
| `promoter_bill`（已有，T12 / T15 复用） | `biz_type`：1 注册返 / 2 充值返 / 6 返利作废 | `amount` 记积分数，`from_user_id` 记来源用户 |

### 7.2 公开

| 方法 | 路径 | 说明 | 请求 / 响应 |
|---|---|---|---|
| POST | `/track` | 事件上报（`TryUserAuth`，匿名可调；限流 60 次/分钟/IP） | 请求 `{ anon_id, events: [{ name, props?, ts? }] }`，单次最多 20 条；响应 `null` |
| GET | `/app/config` | 在现有响应上增加 `growth`（`setting.GrowthConfig` 的 json 字段：礼包、任务、签到、邀请展示值、促销条开关与链接）与 `server_time`（Unix 秒，前端倒计时校准用） | 已实现 |
| GET | `/bonus-code/preview?code=` | 口令预览（匿名） | `{ code, partner_name, kind, bonus_points, first_recharge_rate, first_recharge_cap_points, reward_valid_days, member_days }`；不可用时 `success=false`，message 区分 不存在 / 未开始 / 已过期 / 已领完；已实现 |
| GET | `/work/:id` | 已有；`open_source=1` 时附带 `source: { canvas_id, skill_id, clone_count }`（列表 / 详情本身也带 `open_source, clone_count` 列） | 已实现 |
| GET | `/work/:id/process` | 已有；开源作品不论 `show_process` 一律可看（没填 `canvas_id` 时用 `source_canvas_id`），非开源保持原策略 | 已实现 |
| GET | `/author/:id` | 已有；邀请落地页 `/invite/:code` 复用 | |
| GET | `/invite/:code` | 邀请码解析（匿名） | `{ user_id, nickname, aff_code }`（用户表暂无头像字段）；不存在 `success=false`；已实现 |
| GET | `/billing/model-prices`、`/generation/models` | 每个模型附带 `promo_points_per_unit, promo_label, promo_starts_at, promo_ends_at, promo_active, effective_points_per_unit（非会员实际单价）, effective_member_points_per_unit（会员实际单价）` | 已实现；前端按登录态取对应 effective 值展示，划线价用 `points_per_unit` / `member_points_per_unit` |
| GET | `/billing/plans` | 已有；`promo` 新增 `banner_enabled, banner_link`（顶部倒计时条开关与按钮链接，倒计时用已有 `ends_at`、文案用 `title`/`subtitle`） | 已实现；与 `/app/config.growth.promo_banner_*` 同源（`GrowthPromoBanner*` 键） |

注册接口 `POST /user/register` 与微信注册 `POST /wechat/oauth/register` 请求新增 `attr: { source?, medium?, campaign?, ref?(邀请码), code?(口令), anon_id? }`：写用户来源四列，`ref` 解析成 `inviter_id`（自邀 / 同设备已注册过 / 同 IP 当日注册数 ≥ `GrowthInviteDailyIpLimit` 时不绑定，只记日志），`code` 在注册成功后自动执行一次领取（`service.AfterRegister` 调 `ClaimBonusCode`，失败只记日志不阻断注册），`anon_id` 用于把注册前的匿名事件关联到用户。注册成功后发 `GrowthSignupPoints`（biz_no `signup_gift`）。以上均已实现（`service.AfterRegister`）。

### 7.3 用户侧（UserAuth）

| 方法 | 路径 | 说明 | 请求 / 响应 |
|---|---|---|---|
| GET | `/user/growth/tasks` | 新手任务（与生成任务 `/user/task` 区分） | `{ tasks: [{ key, title, points, status, done_at }], total_points, earned_points, done, valid_days }`；展示 `first_canvas` / `first_video` / `first_work` / `first_invite` 四条；已实现 |
| POST | `/user/checkin` | 签到 | 响应 `{ points, streak, bonus_points, already }`；`already=true` 表示今日已签，不重复发。基础积分含会员倍数；连续 7 的倍数天加 `GrowthCheckinStreakBonus`，当月满勤加 `GrowthCheckinMonthBonus`；已实现 |
| GET | `/user/checkin/status` | 签到状态 | `{ today_done, streak, month_days: [1,2,5...], next_bonus_in, points_today, streak_bonus, month_bonus, valid_days, is_member }`；已实现 |
| GET | `/user/invite/summary` | 邀请总览 | `{ aff_code, invite_url, invited, effective, points_total, rebate_points, rebate_cap, rebate_rate, inviter_points, invitee_points, records: [{ user_id, nickname(脱敏), status(registered/effective/paid), points, created_at }] }` 最近 20 条；老用户无 6 位邀请码时在此补发；已实现 |
| GET | `/user/invite/records` | 邀请记录分页 | `keyword?, page, page_size` → `{ items, total }`；keyword 按被邀请人 ID / 昵称筛；已实现 |
| GET | `/user/bills/self` | 资金流水 | `biz_type?` 逗号分隔（推广返利用 `1,2,6`）；行附 `from_nickname`（脱敏）；已实现 |
| POST | `/user/bonus-code/claim` | 领取口令 | 请求 `{ code }`；响应 `{ bonus_points, partner_name, bind_result, reward_rate, reward_cap_points, reward_expire_at, reward_status, member_days }`。一人一次；红包积分立即到账（`bonus_valid_days`）；首充权益：已付过费的用户 `reward_status=-2`；会员天数：有会员延期，无会员且配置了 `plan_code` 则按该计划开通指定天数（不发月积分）；已实现 |
| GET | `/user/bonus-code/record` | 我的口令记录 | `bonus_code_claim` 一行（附带 `partner_name`）或 `null`；已实现 |
| POST | `/user/work/:id/clone` | 做同款：来源画布去敏后（同分享复制，去掉 taskId / assetId 等）复制成我的新项目「同款 · 标题」+ 同名画布 | 响应 `{ canvas_id, project_id }`；非开源或无来源画布返回 `success=false`（message「作者未开放做同款」，与全站 200 + success 约定一致，不用 HTTP 403）；来源画布已删 → 「来源画布已不可用」；写事件 `work_clone`（props `work_id, author_id, source_canvas_id, skill_id, canvas_id`），`clone_count` +1；作者 ≠ 操作者时给作者 `GrowthWorkClonePoints`（默认 10，0 关闭，日上限 `GrowthWorkCloneDailyCap`，同一访客同一作品当日只奖一次）；限流 30 次/分钟/用户；已实现 |
| POST / PUT | `/user/work`、`/user/work/:id` | 已有；请求新增 `source_canvas_id?, source_skill_id?, open_source?(0/1)`；PUT 按合并后的状态校验 | 已实现 |

### 7.4 服务端事件与奖励触发点

| 事件 `track_event.name` | 触发位置 | 联动奖励 |
|---|---|---|
| `register` | 注册成功 | 礼包 `signup_gift`；口令自动领取 |
| `first_canvas` | 画布首次创建 | 任务 `first_canvas` |
| `first_task` | 首个生成任务创建 | 无 |
| `first_video` | 视频任务首次成功（`CompleteTask`） | 任务 `first_video`；邀请双方奖励（`invite_invitee` / `invite_inviter` + `promoter_bill` 注册返）；邀请人任务 `first_invite` |
| `first_work` | 首次发布作品 | 任务 `first_work` |
| `bind_wechat` | OAuth 绑定微信 | 不再作为新手任务展示；历史 `user_task` / 积分流水保留 |
| `pay_success` | `FulfillOrder`（props：`order_no, amount, kind, plan_code`） | 邀请首充返（`invite_rebate`，`amount × GrowthInviteRechargeRate × GrowthPointsPerYuan`，累计封顶）；口令首充加赠（`bonus_recharge`，`amount × reward_rate × PointsPerYuan`，封顶 `reward_cap_points`，置 `reward_status=1`） |
| `refund` | 订单退款 | 冲回上述两项（`promoter_bill` 返利作废 + 按批次扣减） |
| `work_clone` | 做同款 | 开源作者积分 `GrowthWorkClonePoints`（biz_no `work_clone`，日上限 `GrowthWorkCloneDailyCap`；自己做自己的同款不发） |

前端事件（`POST /track`）：`page_view`、`landing_cta`、`register_start`、`watch_clone_click`、`share_click`、`checkin_click`。

### 7.5 管理侧

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/admin/stats/funnel?from&to&source` | AdminAuth | 按天：`register, first_task, first_video, first_work, pay_success` 人数与转化率；`by_source: [{ source, aff_code, bonus_code, register, paid, amount }]` |
| GET / PUT | `/admin/setting`、`/admin/setting/:key` | RootAuth（已有） | 增长面板直接读写 `Growth*` 键 |
| GET / POST / PUT / DELETE | `/admin/bonus-code`、`/admin/bonus-code/:id` | RootAuth | 口令 CRUD；GET 支持 `kind?, status?, partner?, partner_user_id?, keyword?, page, page_size`，响应 `{ items, total }`；创建 / 更新字段见 `controller/bonus_code.go` 的 `bonusCodePayload`（一个码可同时带红包积分、首充加赠、会员天数）；DELETE 对有领取记录的码只停用；已实现 |
| POST | `/admin/bonus-code/batch` | RootAuth | `{ prefix, count(≤500), ...同创建字段 }` 批量生成（前缀 + 6 位随机），返回 `{ count, codes }`；已实现 |
| GET | `/admin/bonus-code/:id/stats` | AdminAuth | `{ claimed, registered, paid, amount, reward_points, bonus_points }`；已实现 |
| GET | `/admin/bonus-code/export?ids=1,2,3` | AdminAuth | CSV（带 BOM，最多 2000 个）；已实现 |
| GET | `/admin/user/:id/invites` | AdminAuth | `{ totals, records, bills, bills_total, invite_blocked }`；已实现 |
| GET | `/admin/user`、`/admin/user/:id` | 已有 | 列表新增筛选 `inviter_id?, source?, bonus_code?`；列表与详情响应新增 `inviter_id, inviter_nickname(仅列表), aff_code, source, medium/campaign(仅详情), bonus_code, invite_blocked`（T14）；另附 `mobile, identity, tags`（tags 为身份兼容字段，有身份时 `[identity]`）；已实现 |
| PUT | `/admin/user/:id/invite-block` | RootAuth | `{ blocked }` 冻结 / 解冻邀请奖励（冻结的用户作为邀请人或被邀请人都不发奖励，绑定关系保留）；已实现 |
| PUT | `/admin/user/:id/identity` | AdminAuth | `{ identity }` 运营身份：`easytv_official` / `enterprise` / `easytv_teacher` / `easytv_teaching_assistant`；空 / `__none__` 去掉；普通管理员不能改管理员账号；已实现 |
| PUT | `/admin/user/:id/bind` | RootAuth | `{ mobile?, wechat_unbind? }` 换绑手机 / 解绑微信，不走短信；至少保留一种登录方式；已实现 |
| GET / POST / PUT | `/admin/model-price`、`/admin/model-price/:id` | 已有 | 请求 / 响应新增 `promo_points_per_unit, promo_label(≤20 字), promo_starts_at, promo_ends_at`（促销价置 0 取消）；响应附带 `promo_active, effective_points_per_unit, effective_member_points_per_unit`；列表 `sort_by` 新增 `promo_points_per_unit, promo_ends_at`；新增 GET 单条；已实现 |
| POST | `/admin/gateway/models/import` | 已有 | 新模型入库时按 `GrowthNewModelPromoRate/Days/Label` 自动写首发价（促销价 = ceil(标准价 × 比例)，从导入时起 N 天；比例 0 或 1 关闭），请求可带 `auto_promo=false` 关闭本次；响应新增 `promoted, promoted_models`；已实现 |
| GET / PUT | `/admin/billing/promo` | 已有 | `BillingPromo` 新增 `banner_enabled, banner_link`，实现选「读写 `GrowthPromoBanner*` 键」：PUT 时这两项写到 setting 键而不进 BillingPromo JSON，增长面板改 `GrowthPromoBannerEnabled/Link` 效果相同；已实现 |

### 7.6 前端页面 ↔ 接口

| 页面 | 接口 |
|---|---|
| 落地页 / 任意入口（归因） | 读 `utm_*`、`ref`、`code` 写 cookie `easytv_attr`（30 天，首触不覆盖） |
| 注册页 | `POST /register` 带 `attr`；`GET /bonus-code/preview` 实时回显；`GET /invite/:code` 显示邀请人 |
| 创作台首页 | `GET /user/growth/tasks`、`GET /user/checkin/status`、`GET /app/config.growth` |
| 钱包面板 | `POST /user/checkin`、`GET /user/bonus-code/record` |
| 设置 → 推广返利（原邀请有礼，`?tab=invite` / `affiliate`） | `GET /user/invite/summary`、`GET /user/invite/records`、`GET /user/bills/self?biz_type=1,2,6`、`GET /user/withdraw/self`、`GET /user/partner/me` |
| `/invite/:code`（旧 `/i/:code` 301 过来） | `GET /invite/:code` + `GET /author/:id` |
| 播放页 | `POST /user/work/:id/clone`；匿名 `GET /work/:id`、`/work/:id/process` |
| 顶部促销条 / 模型卡 | `GET /billing/plans.promo`、`GET /generation/models` 的促销字段 |

## 8. 成片与社区（迭代二，2026-09-28 ~ 10-16）——接口约定

对应任务单 T23 ~ T34。约定同第 7 节：时间戳 Unix 秒、`{success,message,data}` 包裹、失败 HTTP 200 + `success=false`。新设置键集中在 `setting/growth_setting.go`（`GrowthSkillAuthorShare`、`GrowthShare*`、`GrowthWorkClone*`、`GrowthUgc*`、`ComposeWatermarkEnabled`、`ComposePlanPrompt`、`ComposePlanModel`），`GET /app/config.growth` 下发展示值（含 `work_clone_points`）。创作者收益走 `promoter_bill.biz_type = 7`（技能分成）。

### 8.1 实体（新增表 / 字段）

| 表 | 字段 | 说明 |
|---|---|---|
| `screenplay`（T23） | `id, user_id, project_id, canvas_id, input text, target_seconds, plan json, status(0 生成中 1 待确认 2 已建画布 -1 失败), error, task_no, points_used, created_at, updated_at` | `plan` 结构见 8.2 |
| `compose_job`（T24） | `id, user_id, canvas_id, screenplay_id, clips json, subtitles json, bgm_asset_id, watermark tinyint, status(0 排队 1 合成中 2 成功 -1 失败), progress, output_asset_id, output_url, duration, error, created_at, started_at, finished_at` | `clips: [{node_id, asset_id, url, duration, text}]` |
| `skill` 新增（T26） | `review_status(0 私有 1 待审 2 已上架 3 驳回), review_remark, submitted_at, published_at, revenue_share decimal(4,2), calls_total, revenue_total` | `revenue_share=0` 用全局 `GrowthSkillAuthorShare` |
| `generation_task` 新增（T26） | `skill_id` | 画布节点用技能时携带 |
| `ugc_submission`（T31） | `id, user_id, work_id, platform, url varchar(500), duration_tier, likes, status(0 待审 1 通过 2 驳回), points, viral tinyint, reviewed_by, reviewed_at, remark, created_at` | 唯一 `url` |
| `campaign`（T33） | `id, title, slug 唯一, banner_url, rules text, tag, starts_at, ends_at, status(0 草稿 1 进行中 2 已结束 3 已发奖), prizes json, model_promo json, entries_count, created_at, updated_at` | `prizes: [{rank_from, rank_to, points, member_days}]`；`model_promo` 活动价：`{"model":"veo-3","rate":0.5}` 或 `{"models":["veo-3","hailuo-02"],"rate":0.8,"label":"夏日 8 折"}` 或数组，也可写 `points` 一口价。进行中且已开始未结束时，生成扣费取 min(标准/会员/模型促销, 活动价)。公开接口另附解析后的 `model_promo_items` |
| `campaign_entry`（T33） | `id, campaign_id, work_id, user_id, status(0 待审 1 通过 2 驳回), likes_snapshot, rank, prize_points, created_at` | 唯一 `(campaign_id, work_id)` |
| `promoter_bill` 复用 | `biz_type 7 技能分成`，`from_user_id` 调用者，`ref_type 1 task`，`ref_id task_id`，`amount` 积分数 | |

### 8.2 成片工作室（UserAuth）

策划案 `plan` JSON：
```json
{ "title": "", "style": "电影感 · 冷色调", "aspect": "16:9",
  "characters": [{ "key": "c1", "name": "", "look": "外貌与服装提示词" }],
  "scenes": [{ "key": "s1", "name": "", "look": "场景提示词" }],
  "shots": [{ "no": 1, "scene": "s1", "characters": ["c1"], "prompt": "画面提示词", "motion": "镜头运动", "line": "台词/旁白", "seconds": 5 }] }
```

| 方法 | 路径 | 说明 | 请求 / 响应 |
|---|---|---|---|
| POST | `/user/screenplay` | 生成策划案 | 请求 `{ input(≤8000 字), target_seconds(15/30/60), style?, project_id? }`；同步调用文本模型（`ComposePlanModel` 或第一个已定价文本模型），成功后按文本模型单价扣分（`task_no` 记流水）；响应 `screenplay` 行（含 `plan`）；解析失败自动重试一次；限流 30 次/分钟/用户；已实现 |
| GET | `/user/screenplay/:id` | 读取 | |
| PUT | `/user/screenplay/:id` | 保存用户修改的 `plan` | 请求 `{ plan }`，校验镜头 ≤ 12、角色 ≤ 4、场景 ≤ 4 |
| POST | `/user/screenplay/:id/estimate` | 预估建画布后各层积分 | 请求可带 `{ image_model?, video_model?, audio_model? }`（空取各类型第一个已定价模型）；响应 `{ characters, shots_image, shots_video, voice, total, image_model, video_model, audio_model }`；已实现 |
| POST | `/user/screenplay/:id/build` | 按策划案建画布 | 请求 `{ image_model?, video_model?, audio_model? }`；新建画布（无 project_id 时新建同名项目）并写 graph：文本节点存策划案摘要；每个角色一个图片节点（`data.sp.layer=character`）；每镜一个分镜图节点（`sp.layer=shot_image`，连角色图作参考）→ 视频节点（`sp.layer=shot_video`，`data.refNodeId` 指向分镜图，`data.seconds`、`data.line`）→ 有台词时配音节点（`sp.layer=voice`，`data.text`）；节点带 `data.model` 建议模型；响应 `{ canvas_id, project_id, estimate_points, estimate }`；已建过直接返回已有画布（`existing=true`）；已实现 |
| POST | `/user/canvas/:id/compose` | 创建合成任务 | 请求 `{ clip_order?: [node_id], subtitles?: bool, bgm_asset_id?, screenplay_id? }`；取画布内已成功的视频节点（按 x 排序或 `clip_order`），字幕由策划案台词与片段时长生成；响应 `compose_job` 行；单用户排队 / 合成中 ≥ 3 个时先拒（「你已有 N 个成片在排队」），全局队列 ≥ 20 时再拒并带排队位次 |
| GET | `/user/compose/:id` | 轮询 | `status/progress/output_asset_id/output_url/error` |
| GET | `/user/compose?canvas_id=` | 该画布最近 10 次合成 | |

**向导默认模型与网关报错文案（2026-09-12 补）**

- setting 新增 `ComposeImageModel` / `ComposeVideoModel` / `ComposeAudioModel`（管理端「增长」面板「成片与分成」分区），随 `GET /app/config` 的 `growth.compose_*_model` 下发。向导默认模型的优先级是：本次已选 → 上次记住的 → 这三个键 → 模型清单第一个。加这三个键的原因是默认值原来只能靠定价行 id 顺序，而某个模型的某条通路在网关坏掉时运营需要能立刻换默认。
- 网关报错不再把原始响应丢给创作者（`service/providers/gateway_error.go`）：`unsupported duration` / `unsupported resolution` / `Inpaint image must match` / 内容审核 / Kling 风控（`risk control`，2026-09-28 补）/ 401、402、429 / 5xx 各有一句能照着做的中文，原文写进 SysLog 便于排查；没有映射规则的仍保留「网关返回 N：原文」。
- **网关 429 / 503 自动重试（2026-09-28）**：所有网关调用（文本 / 图片 / 视频 / 转写）在 429 或 503 时最多重试 2 次，间隔认 `Retry-After`（封顶 10 秒），没有则 1.5 秒、3 秒；只挑这两个「网关没受理」的状态码，502 / 504 可能上游已建任务，不重发。重试写 SysLog `gateway 429 <path>: retry n/2 after …`。实测 gpt-4o-mini 常因限流回 429，定风格 / 提示词优化 / agent 一次即败，重试后成功率明显改善；重试用尽仍回「模型当前排队较多」。
- **已知网关侧故障（不是本仓库的问题）**：`seedance-2.0-fast` 的图生视频连续三次返回 `502 cloudsway returned HTTP 500`，同模型同参数的**文生视频正常**（实测出片 5 秒）。需要找 EasyAPI 侧排查；在修好之前建议把 `ComposeVideoModel` 配成 `hailuo-02`（图生视频实测可用）。

合成 worker（`service/compose.go`）：并发 2，ffmpeg 拼接（统一 1280×720 / 30fps，按目标 `aspect`）、烧录 SRT 字幕、BGM 混音（-14 dB，淡出）、免费用户右下角水印「EasyTV.ai」（`ComposeWatermarkEnabled` 且 `!IsMemberForPricing`）；输出 mp4 上传 OSS，入资产库 `source=compose`，封面走抽帧；超过 10 分钟标失败；事件 `compose_ok` / `compose_fail`。合成不计积分。

### 8.3 技能上架与创作者分成

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| POST | `/user/skill/:id/submit` | UserAuth | 私有 / 驳回状态可提交，`review_status → 1`；需有标题、提示词、封面 |
| POST | `/user/skill/:id/withdraw` | UserAuth | 已上架 / 待审 → 私有 |
| GET | `/skill` | 公开 | 已有；用户技能只返回 `review_status=2` |
| GET | `/user/creator/summary` | UserAuth | `{ skills:[{ id, title, review_status, calls, stars, revenue }], calls_total, revenue_total, withdrawable, cash_available, points_per_yuan, share_rate, records:[{ id, skill_id, skill_title, from_user(脱敏), points, biz_type, created_at }] }` 最近 50 条 |
| POST | `/user/creator/exchange` | UserAuth | `{ points }` 收益兑积分（90 天批次），扣减 `withdrawable`，写 `promoter_bill` 负项 `biz_type 8`；响应 `{ withdrawable, cash_available }` |
| POST | `/user/creator/withdraw` | CriticalRateLimit + UserCriticalRateLimit("creator_withdraw") | `{ amount, way, account_info }` 技能收益提现人民币；`amount` 元，汇率 `GrowthPointsPerYuan`（默认 20 积分/元）；冻结对应积分（`biz_type 10`）并建 `withdraw.owner_type=3`；响应 `{ withdraw_no, status, withdrawable, cash_available }` |
| GET | `/user/creator/withdraws` | UserAuth | `{ items, total }` 技能分成提现单（不含渠道返佣 / 余额提现） |
| GET | `/creator/leaderboard?period=week\|month` | 公开 | `[{ user_id, nickname, calls, revenue, skills }]` 前 20 |
| GET / PUT | `/admin/skill?review_status=`、`/admin/skill/:id/review` | AdminAuth | 审核：`{ action: approve\|reject\|unpublish, remark?, revenue_share? }` |

分成入账（已实现）：`POST /user/task` 请求新增 `skill_id?`（须官方 / 已上架 / 本人技能），任务 `CompleteTask` 结算后若 `skill_id > 0`、技能已上架、作者 ≠ 调用者与付款人，按 `revenue_share`（0 取 `GrowthSkillAuthorShare`）× 实际扣分向下取整写 `promoter_bill(biz_type 7, ref_type 2, ref_id task_id)`，同一任务只记一次；技能 `calls_total / revenue_total` 累加。兑换：`POST /user/creator/exchange` 写 `biz_type 8` 负项并发 90 天积分批次。提现：`POST /user/creator/withdraw` 按 `GrowthPointsPerYuan` 把积分换成人民币，写 `biz_type 10` 冻结积分并进入现有提现审核；驳回 / 打款失败写 `biz_type 11` 退回积分（不退 `quota`）。作者 `withdrawable` = 7 / 8 / 10 / 11 类有效流水之和；`cash_available` = `withdrawable ÷ 汇率` 截到分。兑换与提现共用用户锁，避免同一笔收益花两次。`GET /skill` 新增 `source=official|community`；后台 `GET /admin/skill` 新增 `review_status` 筛选并附 `author_nickname`。

### 8.4 分享回流（T30）

前端在播放页带 `ref` 打开时上报事件 `share_open`（匿名可，props `{ work_id, ref }`）。服务端 `POST /track` 里对 `share_open` 做奖励：作者 = `ref` 对应用户且作品作者一致、访客 `anon_id` 当日首次、非作者本人 → 给作者 `GrowthShareOpenPoints`（日上限 `GrowthShareDailyCap`，biz_no `share_open`）。

### 8.5 站外投稿（T31 / T32）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| POST | `/user/ugc` | UserAuth | `{ platform(douyin\|xiaohongshu\|bilibili\|other), url, work_id? }`；同 url 只能一次；每月 `GrowthUgcMonthlyEntries` 条 |
| GET | `/user/ugc` | UserAuth | `{ items(含 work_title / work_cover), total, month_points, month_cap, month_entries, month_entries_cap }`；已实现 |
| GET | `/admin/ugc?status=&platform=&keyword=&page` | AdminAuth | `{ items(附 nickname、user{id,nickname}、work{id,title,cover_url}), total }`，待审优先；已实现 |
| PUT | `/admin/ugc/:id/review` | AdminAuth | `{ action: approve\|reject, duration_tier(30/60/120), likes, remark?, to_banner? }`；通过按档位发积分，`likes ≥ GrowthUgcViralLikes` 翻倍，月累计封顶；`to_banner` 用作品封面创建首页 Banner；发站内通知 |

### 8.6 活动（T33）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/campaign` | 公开 | 进行中与已结束（近 3 个）；含 `model_promo_items` |
| GET | `/campaign/:slug` | 公开 | 详情 + 我的投稿状态（TryUserAuth）+ `model_promo_items` |
| GET | `/campaign/:slug/entries?sort=likes\|new&page` | 公开 | 已通过的投稿墙（作品卡字段） |
| POST | `/user/work/:id/enter` | UserAuth | `{ campaign_id }` 投稿；进行中且作品已发布 |
| GET / POST / PUT / DELETE | `/admin/campaign`、`/admin/campaign/:id` | RootAuth | CRUD |
| GET / PUT | `/admin/campaign/:id/entries?status=&sort=`、`/admin/campaign/entry/:id/review` | AdminAuth | 投稿列表附 `work`（作品行含作者）与 `user{id,nickname}`；审核 `{ action: approve\|reject }`；已实现 |
| POST | `/admin/campaign/:id/settle` | RootAuth | 结束后按点赞快照排名，按 `prizes` 发积分 / 会员天数，`status → 3`，幂等 |

## 9. 营销体系收尾（迭代三，2026-10-19 起）——接口约定

补齐突围方案第 07 节里剩下的 M10（活动中心前端）、M11（关注 / 榜单 / 通知）、M12（渠道伙伴计划）、M13（召回与生命周期触达）与 M7 的会员日。约定同前：Unix 秒、`{success,message,data}`、失败 HTTP 200 + `success=false`。新设置键仍在 `setting/growth_setting.go`。

### 9.1 实体（新增表 / 字段）

| 表 | 字段 | 说明 |
|---|---|---|
| `user_follow`（M11） | `id, user_id, author_id, created_at` | 唯一 `(user_id, author_id)`；不能关注自己 |
| `work` 新增（M11） | 无新列，热度按 `work_like.created_at` 在窗口内计数 | 周榜 = 近 7 天点赞数，同分按浏览数 |
| `partner`（M12） | `id, user_id 唯一, name, kind(mcn/school/kol/dev/other), contact, intro, code varchar(20) 唯一, rebate_rate decimal(5,4), status(0 待审 1 已通过 2 已驳回 3 已停用), remark, customers, paid_users, amount_total decimal(12,2), rebate_total decimal(12,2), applied_at, reviewed_at, reviewed_by, created_at, updated_at` | `code` 审核通过时生成（6 位大写），`rebate_rate` 0 取 `GrowthPartnerRebateRate` |
| `partner_customer`（M12） | `id, partner_id, user_id 唯一, source(code/link/manual), first_paid_at, amount decimal(12,2), rebate decimal(12,2), created_at` | 一个用户只归属一个伙伴，30 天归因窗口由前端 cookie 控制 |
| `user` 复用（M12） | `partner_id`（缓存列）、`quota`（返佣入账走这里，复用现有提现） | 真相在 `partner_customer` |
| `promoter_bill` 复用（M12） | `biz_type 9 伙伴返佣`，`amount` 记人民币元，`from_user_id` 记客户 | 与技能分成（7 / 8 记积分）区分：9 记钱，可提现 |

### 9.2 关注与榜单（M11）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/user/author/:id/follow` | 关注 / 取关（幂等切换），响应 `{ following, followers }`；不能关注自己 |
| GET | `/user/following?page&page_size` | 我关注的作者的作品流（已发布，按发布时间倒序） |
| GET | `/user/follow/authors?page` | 我关注的作者列表 `{ id, nickname, works, followers, is_following }` |
| GET | `/author/:id` | 已有；响应新增 `followers, following_count, is_following`（`TryUserAuth`） |
| GET | `/rank/works?period=week\|month` | 作品榜前 20：`{ work, likes_in_period, views }`，近 7 / 30 天点赞数排序 |
| GET | `/rank/authors?period=week\|month` | 作者榜前 20：`{ user_id, nickname, works, likes, followers }` |
| GET | `/rank/skills?period=week\|month` | 技能榜前 20：`{ skill, calls, stars }`（按 `promoter_bill` 7 类调用数 + uses） |

关注的作者发布新作品时给粉丝发站内信（类型 `work`，`link=/watch/:id`，单次最多 500 个粉丝，`HasRecentNotification` 去重）。

### 9.3 渠道伙伴计划（M12）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| POST | `/user/partner/apply` | UserAuth | `{ name, kind, contact, intro }`；已申请过返回当前状态 |
| GET | `/user/partner/me` | UserAuth | 未申请返回 `null`；已通过返回 `{ partner, link, poster_link, customers, paid_users, amount_total, rebate_total, withdrawable, recent:[{ user(脱敏), amount, rebate, created_at }] }` |
| GET | `/partner/:code` | 公开 | 落地页数据 `{ name, intro, valid }`；无效返回 `success=false` |
| GET / PUT | `/admin/partner?status=&keyword=`、`/admin/partner/:id/review` | AdminAuth / RootAuth 审核 | 审核 `{ action: approve\|reject\|disable, rebate_rate?, remark? }`；通过时生成 `code` 并发通知 |
| GET | `/admin/partner/:id/customers?page` | AdminAuth | 归属客户与其贡献 |

归因与返佣：注册请求 `attr.pcode`（前端从 `?pcode=` 或 `/partner/:code`（旧 `/p/:code` 301 过来）落地写 cookie，30 天）→ 绑定 `partner_customer` 并写 `user.partner_id`；`FulfillOrder` 履约后若付款人有归属伙伴，按 `rebate_rate`（0 取 `GrowthPartnerRebateRate`，默认 0.15）× 实付金额入账到伙伴的 `quota`（人民币元）并写 `promoter_bill(biz_type 9)`，同一订单只记一次，首年内有效（`GrowthPartnerRebateMonths`，默认 12）；退款时写 `biz_type 6` 冲回并扣减 `quota`。提现复用现有 `/user/withdraw`。

### 9.3.1 代理商后台（easytv-partner，2026-11-02）

独立前端仓库 `easytv-partner`（Nuxt 4，与 easytv-manage 同栈），给已通过审核的渠道伙伴（代理商）一个自己的后台：看板、归属客户、返佣与提现、口令效果、推广物料、资料维护。**不另建账号体系**：伙伴用自己的 EasyTV 账号登录（手机验证码 / 账号密码，与创作台同一套 `/user/login`、`/user/sms_login`），鉴权仍是 `UserAuth`，数据只限本人名下的归属客户聚合（昵称脱敏，不含客户手机号 / 邮箱 / 订单明细）。管理端「渠道伙伴」页可用现有 `POST /admin/user/:id/console_access` 签 ticket，拼上代理商后台地址（`NUXT_PUBLIC_PARTNER_URL` + `/console?ticket=`）免密进入。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/partner/console/overview` | 看板总览：`GET /user/partner/me` 的全部字段 + `rebate_months`、`month:{ new_customers, paid_users, orders, amount, rebate }`、`server_time`。未申请返回 `success=false, code="not_applied"`；待审 / 驳回 / 停用照常返回 `partner`（`month` 为 0），前端据 `partner.status` 展示状态页 |
| GET | `/user/partner/console/stats?range=7d\|30d\|90d` | 日趋势（含今天，缺的日期补 0）：`{ rows:[{ date, new_customers, orders, amount, rebate }], start_date, end_date, totals }`；`amount` 客户实付、`rebate` 返佣，均为人民币元，只统计有效返佣流水（`promoter_bill` 9 且 `status=1`） |
| GET | `/user/partner/console/customers?paid=1\|0&sort=recent\|amount&page&page_size` | 归属客户 `{ items:[{ id, user_id, nickname(脱敏), source, first_paid_at, amount, rebate, created_at }], total }` |
| GET | `/user/partner/console/bonus-codes?page&page_size` | 归属到我（`bonus_code.partner_user_id`）的口令红包及效果 `{ items:[{ …bonus_code, stats:{ claimed, registered, paid, amount, reward_points, bonus_points } }], total }`；只读，创建仍在运营后台 |
| PUT | `/user/partner/console/profile` | `{ name?, kind?, contact, intro }`：待审可改全部；已驳回改完自动回到待审（清空驳回原因，`applied_at` 更新）；已通过只能改 `contact` / `intro`（改名或类型报错）；已停用不可改 |

未通过审核时 `stats` / `customers` / `bonus-codes` 返回 `success=false, code="not_approved"`。返佣流水与提现沿用现有接口：`GET /user/bills/self?biz_type=9,6,3,4`、`GET /user/withdraw/self`、`POST /user/withdraw`（余额即用户 `quota` 折算的人民币）。

### 9.4 召回与生命周期触达（M13）

后台任务 `service.RunLifecycleJobs()`（每 6 小时，启动 3 分钟后先跑一次）。**总开关 `GrowthLifecycleEnabled` 默认关闭**：这套任务会真的发积分、赠会员天数并发邮件，必须由运营在管理端「增长」面板确认文案与参数后主动打开，避免部署即群发。

| 触发 | 条件 | 动作 |
|---|---|---|
| 回归礼 | `last_login_at` 距今 ≥ `GrowthRecallInactiveDays`（30）且 90 天内没发过回归礼 | 发 `GrowthRecallPoints`（100，30 天有效）+ `GrowthRecallMemberDays`（3）天会员，站内信 + 邮件 |
| 会员续费提醒 | 会员剩余 ≤ `GrowthRenewRemindDays`（7）天且未提醒过 | 站内信 `/pricing`，带续费折扣文案 |
| 积分过期提醒 | 有批次在 `GrowthPointsExpireRemindDays`（3）天内到期且该批次未提醒过 | 站内信，列出将过期积分数 |

单次每类最多处理 200 个用户；去重用 `HasRecentNotification(userId, type, link, since)`。邮件只发给邮箱注册且开了邮件通知的用户。

### 9.5 会员日（M7 剩余项）

`GET /billing/plans` 的 `promo` 新增 `member_day`（`{ day, pack_discount, active, next_at }`）：每月 `GrowthMemberDayOfMonth`（11）日积分包按 `GrowthMemberDayPackDiscount`（0.8）折；`POST /billing/quote` 与下单在当天对 `kind=points` 自动应用，响应 `discount_label` 说明。会员日不与会员价叠加计算两次，取折后价。

## 10. 功能补齐（迭代四，2026-11 起）——接口约定

对应突围方案第 06 节的 F2 / F4 / F5 / F7 与 F6 出海套件（F8 开放平台已于 2026-09-14 下线，见 10.6），加上 M7 会员日的前端展示。约定同前。

### 10.1 实体（新增表 / 字段）

| 表 | 字段 | 说明 |
|---|---|---|
| `subject`（F2 主体库） | `id, user_id, team_id, kind(character/scene/prop), name, prompt text, look text, cover_asset_id, cover_url, refs json([{asset_id,url,label}]), shared tinyint, uses, created_at, updated_at, deleted_at` | `shared=1` 团队内共享；`refs` 存三视图 / 表情表等参考图 |
| `generation_task` 新增（F4 / F5） | `parent_task_id, retake_of, lang varchar(10)` | 续写 / 重拍溯源，`lang` 记多语言配音与字幕语种 |
| `compose_job` 新增（F5） | `lang varchar(10), subtitle_text text` | 多语言成片：按语种取译文字幕与配音 |
| `team_member` 新增（F7） | `points_limit int, points_used int, limit_reset_at` | 成员积分月度上限，0 不限 |

### 10.2 主体库（F2）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/subject?kind=&keyword=&page` | 我的 + 团队共享的主体 |
| POST | `/user/subject` | `{ kind, name, look, prompt?, cover_asset_id?, refs?, shared? }`；`look` 用于文生图 |
| PUT / DELETE | `/user/subject/:id` | 改名 / 改描述 / 换参考图 / 共享开关 / 删除（软删） |
| POST | `/user/subject/:id/generate` | 按 `look` 生成三视图（一次建 3 个图片任务，`node_id` 留空，完成后自动写回 `refs`），响应任务列表 |
| POST | `/user/subject/from-asset` | `{ asset_id, kind, name }` 用已有图片直接建主体 |

画布与成片引用：任务请求新增 `subject_ids: [int]`，服务端把主体的参考图并入 `inputs`（角色 → `role=character`，场景 → `role=scene`，道具 → `role=image`），并把 `look` 追加到提示词末尾；策划案 `build` 时角色若匹配到同名主体则直接引用主体参考图，不再生成角色图。

### 10.3 片段重拍、续写、超分（F4）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/user/task/:id/retake` | 同参数重跑，`retake_of` 记原任务；成功后前端把节点结果替换。请求可带 `{ prompt?, params?, model? }`：面板改了提示词 / 参数 / 模型就传；传了 `params` 时按「只覆盖传来的键」合并到原任务参数上，不传则完全沿用原任务 |
| POST | `/user/task/:id/extend` | 续写：取原视频末帧作首帧建新视频任务，`parent_task_id` 记原任务；请求可带 `{ prompt?, seconds?, node_id? }`。**`node_id` 必须传新建节点的 id**，否则任务会记在源节点上；末帧图片入资产库后作为 `first_frame` 输入 |
| POST | `/user/task/:id/upscale` | 超分（工具条「高清」）：请求 `{ asset_id?, model?, resolution?, scale? }`。不传 `asset_id` 时取 `:id` 任务的产物；模型不传按 `upscale` 能力位挑（`model_price.abilities`，没打标签时按模型名含 `upscale` / `超分` 推断） |

超分未接入模型时返回「暂未接入超分模型，请联系运营」，面板指定的模型不具备 `upscale` 能力时返回「模型 X 不支持超分」，与 `GET /user/media/actions` 的可用性口径一致（见第 23 章）。

末帧提取复用 `service/cover.go` 的 ffmpeg 能力（新增 `ExtractLastFrame`），产物入资产库 `source=generated`，再作为 `first_frame` 输入。

### 10.4 字幕与多语言（F5）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/user/subtitle/translate` | `{ text, target_lang }` → `{ text }`，走文本模型；限流 30 次/分钟 |
| POST | `/user/screenplay/:id/localize` | `{ target_lang }` 把策划案台词整份翻译，写入 `plan.shots[].lines[lang]`，响应新 plan |
| POST | `/user/canvas/:id/compose` | 请求新增 `lang?`、`subtitle_text?`（整份 SRT 文本，优先于自动生成） |
| POST | `/user/task` | 音频任务请求新增 `lang?`、`voice?`，服务端按语种先翻译再 TTS |

支持语种：`zh, en, ja, ko, es, fr`（`setting.ComposeLangs` 可配）。字幕文本来自策划案台词或前端手填；2026-09-24 起也可以先对素材跑媒体动作 `transcribe`（语音转字幕，见 35.2）拿到 SRT 再作为 `subtitle_text` 传入。

### 10.5 团队增强（F7）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/team` | 已有；成员行新增 `points_limit, points_used, points_left` |
| PUT | `/user/team/member/:id/limit` | owner 设成员月度积分上限 `{ points_limit }`，0 不限；当月已用超限时拒绝生成 |
| GET | `/user/team/usage` | 已有；新增按成员的当月消耗明细 |

扣费校验：`FreezeTaskPoints` 前若付款人是团队 owner 且发起人是成员，检查成员当月 `points_used + points` ≤ `points_limit`，超限报「本月团队额度已用完，请联系管理员」；结算成功后累加 `points_used`，每月 1 日重置。主体库与技能模板按 `team_id` 共享（`shared=1`）。

### 10.6 开放平台（F8）——已下线（2026-09-14）

产品决定砍掉开放平台：`/open/v1/*`、`/user/access-key*`、`/admin/access-key*` 接口与创作台密钥管理页全部删除。生产库 `access_key` 表（0 行）已于 2026-09-14 DROP，建表语句备份在工作区 `db-backups/access_key-20260914.json`。

### 10.7 出海套件（F6，不含国际支付）

| 项 | 说明 |
|---|---|
| 英文作品广场 | `GET /work` 新增 `lang?` 过滤（作品新增 `lang varchar(10)`，发布时按界面语言写入），英文站 `/en/watch/:id` 已由 i18n 路由覆盖 |
| 海外注册 | 邮箱注册已不依赖手机号；注册接口对非 +86 不强制短信（现状即如此，补文档与前端引导） |
| 分发引导 | 播放页分享弹窗新增 TikTok / YouTube 文案与跳转（前端） |
| 英文礼包与邀请页 | `/en/invite/:code`、`/en/partner/:code` 由现有 i18n 路由自动覆盖（旧 `/en/i`、`/en/p` 同样 301），文案走词条 |

**国际支付（Stripe）不在本迭代实现**：缺商户凭据且无法联调，留到负责人配置后单独做，避免上线未验证的支付路径。

### 10.9 管理端补充（迭代四）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/admin/subject?user_id=&team_id=&kind=&shared=&keyword=&page` | AdminAuth | 主体库只读，含 `owner_name, refs, refs_count, uses` |
| DELETE | `/admin/subject/:id` | RootAuth | 违规主体软删 |
| GET | `/admin/team?keyword=&owner_id=&page` | AdminAuth | 团队列表，含 `owner_name, member_count, points_limit_total, points_used_month` |
| GET | `/admin/team/:id/members` | AdminAuth | 成员与月度额度（`points_limit/points_used/points_left`） |
| PUT | `/admin/team/member/:id/limit` | RootAuth | `{ points_limit }` 代改成员额度（owner 联系不上时） |
| GET | `/admin/work?lang=` | 已有 | 补上语种过滤（F6 出海，英文广场运营用） |

### 10.11 模型参数能力白名单（2026-09-12 已实现）

网关只会在提交时回 `400 unsupported duration`（sora-2 不吃 10 秒），用户白等一轮再靠失败退款兜，所以把各模型支持的秒数声明在定价里。

| 表 | 字段 | 说明 |
|---|---|---|
| `model_price` 新增 | `durations varchar(60) default ''` | 支持的时长秒数，逗号分隔（如 `4,8,12`）；**空串表示不限**，即老行为 |
| `model_price` 新增 | `resolutions varchar(120) default ''` | 支持的分辨率，逗号分隔（如 `512P,768P,1080P`）；空串表示不限。大小写不敏感 |
| `model_price` 新增 | `frame_match tinyint default 0` | 1 = 图生视频的首帧图必须与输出尺寸完全一致（sora 系）。置 1 后服务端在提交前把首帧裁成输出尺寸并换成新资产 |

- `GET /generation/models` 每行新增 `durations: [int]` 与 `resolutions: [string]`（未声明时为 `[]`）。
- `POST /user/task` 在预扣积分之后、入库之前校验（`service.CheckModelParams`）：声明了而请求值不在列表里，返回「<model> 只支持 4 / 8 / 12 秒，当前请求 10 秒」或「<model> 只支持 512P / 768P / 1080P，当前请求 720P」，**不建任务、不扣分**。请求里没带该参数（图片任务等）一律放过。
- `POST / PUT /admin/model-price` 接受 `durations` 与 `resolutions`，服务端分别用 `model.NormalizeDurations`（去空白、去非法、去重、升序，上限 600 秒）与 `model.NormalizeResolutions`（去空白、大写、去重、保序）清洗。
- 前端：画布右栏的时长与分辨率档位按模型声明生成，换模型后原值不在新列表时回落到第一档；成片向导按模型档位对齐策划案给的镜头秒数，分辨率取声明里的第一个（没声明仍是 720P）。
**首帧尺寸适配（frame_match）**

`sora-2` 图生视频会回 `400 Inpaint image must match the requested width and height`：它要求首帧图与输出尺寸逐像素一致，而我们的分镜图是按比例生成的（16:9 → 1792x1024），和 720P 的 1280x720 对不上。

- `service.FitTaskFirstFrame` 在 `POST /user/task` 预扣之后、入库之前执行：只对 `frame_match=1` 的模型动手，把 `role=first_frame`（含兼容的 `image`）素材按目标尺寸「铺满后居中裁切」成一张新图片资产（`source=generated`），并改写任务 `inputs`。首帧加黑边会直接出现在成片第一帧，所以用裁切而不是补边。
- 目标尺寸由 `service.TargetVideoSize(ratio, resolution, size)` 推导：显式 `size` 优先，否则用分辨率数字做短边、比例定方向，边长取偶数。推不出来就跳过适配，交给网关判断。
- 原图尺寸已经等于目标尺寸时直接复用，不多造资产。适配失败只记日志不阻断提交（失败照样全额退分）。
- 实测：`sora-2` + 16:9 720P + 8 秒，首帧 1792x1024 被裁成 1280x720 后提交成功，输出 1280x720 / 8.3 秒。

**已实测与已声明的网关约束**（2026-09-12 写入 `model_price`）

| 模型 | durations | resolutions | frame_match | 依据 |
|---|---|---|---|---|
| `sora-2` | 4,8,12 | 720P | 1 | 时长与首帧要求是实测；分辨率取网关模型说明「720p videos」 |
| `hailuo-02` | 6,7,8,9,10 | 512P,768P,1080P | 0 | 网关说明「6-10 seconds, 512p/768p/1080p」，范围按整秒全列 |
| `hailuo-2.3` | 6,7,8,9,10 | 768P,1080P | 0 | 网关说明「6-10 seconds, 768p/1080p」 |
| `wan2.6-t2v` | 空 | 720P,1080P | 0 | 网关说明「720p/1080p」，未提时长 |

其余 15 个已定价视频模型（seedance 三个、wan2.7-t2v、grok-imagine-video-1.5、Kling 七个、wan2.6-r2v 两个）在网关的模型说明里没写时长与分辨率，三个字段一律留空 = 不限 = 老行为，需要运营按网关文档补。网关的 `/pricing` 接口只有 `description` 文本，没有结构化的能力字段，所以这份数据只能人工维护。

### 10.10 团队协作补齐（F7 续，2026-09-12 已实现）

F7 原本还有两项没做：技能模板的团队共享、项目评论批注。补在这里。

| 表 | 字段 | 说明 |
|---|---|---|
| `skill` 新增 | `team_id int default 0 index, shared tinyint default 0` | `shared=1` 时 `team_id` 为作者当时所在团队；作者退团后共享自动失效（按当前团队比对） |
| `project_comment`（新增） | `id, project_id, user_id, canvas_id, node_id varchar(40), content varchar(1000), resolved tinyint, resolved_by, resolved_at, created_at, updated_at, deleted_at` | `node_id` 为空是项目级留言，非空是画布节点批注；`canvas_id=0` 同理 |

**技能模板团队共享**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/skill` | 已有；现在返回「我的 + 同团队成员已共享的」，共享项带 `team_shared: true` 与 `author_nickname`，且不可改 / 不可删 |
| POST / PUT | `/user/skill`、`/user/skill/:id` | 请求新增 `shared?: bool`；置 true 时写入当前团队 id，用户不在团队返回「你还不在团队里，无法共享给团队」 |

只有作者能改、删、提交上架团队共享的技能，其他成员只能使用。作者退出团队后 `team_id` 与当前团队不匹配，列表里对原团队不再可见。

**项目评论批注**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/project/:id/comment?canvas_id=&node_id=&only_open=1&page` | 项目所有者或同团队成员可读；返回 `author_nickname, resolved_by_nickname`，未解决的排在前面 |
| POST | `/user/project/:id/comment` | `{ content, canvas_id?, node_id? }`，正文 1–1000 字；他人评论时给项目所有者发站内信（同一项目 10 分钟内只发一条） |
| PUT | `/user/project/comment/:id` | `{ resolved: bool }` 标记解决 / 重新打开；作者、项目所有者、团队 owner 可操作；解决时给评论作者发站内信 |
| DELETE | `/user/project/comment/:id` | 评论作者或项目所有者可删（软删） |

> 2026-09-14 变更：队友只能看到**团队空间**里的项目（`project.team_id` = 自己所在团队），队友的个人项目不再可见；`scope=team` 保留为兼容别名。主体库改为按空间（`team_id`）共享。见 20。

可见性一律走 `service.CanAccessProject(userId, projectId)`：所有者本人，或与所有者同属一个团队。项目不存在、无权访问都返回 `success=false`「项目不存在或无权访问」，不区分两者。

为了让队友够得着项目，项目接口另加两处（默认行为不变）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/project?scope=team` | 同团队**其他**成员的项目，行带 `team_shared: true` 与 `owner_nickname`；不传 `scope` 仍然只返回自己的 |
| GET | `/user/project/:id` | 自己的项目不变；队友的项目只读可进，返回 `team_shared: true`，画布按所有者查 |

两处项目列表与详情都附 `open_comments`（未解决批注数，列表走一次 GROUP BY）。改名、删除、移入回收站仍然只有所有者能做（走 `user_id` 条件，未放开）。

## 11. 评审修复（2026-09-13）——行为变更

外部评审 F01–F20 的修复里改变了接口可见行为的部分。

### 11.1 生成任务

- `POST /user/task`：`params.count > 1` 返回 `success=false`「一次只能生成 1 个结果，需要多个请分多次提交」。原来按 count 倍扣分却只出一个产物。重试沿用旧参数时把 count 改成 1 再计费。
- 非文本任务创建前校验云存储空间，已满返回「云存储空间不足…」。
- 团队成员月度额度：已用 = 本月已结算 + 该成员进行中任务的预扣；校验在预扣事务里锁成员行。报错文案改为「本月团队额度不足（上限 N 积分，已用及进行中 M，本次需要 K）…」。
- 置失败 / 取消与退回预扣积分在同一事务提交；`ExpireStaleTasks` 每轮先补退「已失败 / 已取消但 points_frozen > 0」的任务。
- 媒体产物：下载校验 2xx、拒绝 HTML/JSON 错误页、10 分钟总超时、超过 512MB 报错（不再静默截断），资产写入 `size`。资产入库与任务置成功同一事务；任务已被取消 / 判超时则丢弃产物并删存储文件，不再出现「积分退了、资产照样进库」。
- 视频 `input_references` 角色映射：`character` / `scene` / `style` → `reference`（image），`voice` → `reference_audio`，`mask` 在视频任务里忽略。原来这些角色一律报「不支持的素材角色」。主体库并入的参考图角色由 `image` 改为 `subject`（见 11.5；旧版视频接口会把 `image` 当首帧）；团队共享主体的参考图属于队友，provider 取素材时允许同团队资产。
- 历史 size=0 的资产用 `go run ./cmd/backfill-asset-size [--dry-run]` 回填（本地存储读文件，OSS 发 HEAD）。

### 11.2 成片合成

- `POST /user/canvas/:id/compose`：已生成视频节点超过 24 个直接拒绝（原来悄悄只合前 24 个）；`clip_order` 上限同为 24。
- 有视频节点的资产不可用（已删除 / 不是视频）时拒绝并列出节点 id，不再静默跳过。
- 本地存储（`/upload/...` 相对地址）的镜头可以合成（直接读盘）；首帧适配、末帧提取、BGM 同样受益。
- 合成中的任务超过 12 分钟仍是 running（执行进程已不在）由 worker 每分钟回收为失败，error「合成中断（服务重启或超时），请重新合成」，不再永久占用单用户排队名额。
- 云存储空间已满时拒绝合成。

### 11.3 分享 / 做同款 / Skill 模板复制

`SanitizeSharedGraph` 递归删除 `assetId` 与所有 `*AssetId` / `*AssetIds` 键（含 `refItems[].assetId`、导演台 `scene` 里的），以及 `subjectIds`。媒体地址保留，前端对「只有 url 没有 assetId」的引用应提示重新选入素材。

### 11.4 图片编辑与参考图（F01 / F06）

原来图片任务只调 `/v1/images/generations` 且不读 `inputs`，「智能修改 / 局部修改 / 参考图」实际只凭提示词重新出图。现在：

- `model_price` 新增 `image_input varchar(10)`：`none` 不支持；`edits` 走 `POST /v1/images/edits`（multipart：`model / prompt / n=1 / size / quality`，一张原图用 `image`、多张用 `image[]`，可选 `mask`）；`url` 在 `/v1/images/generations` 里带 `image`（单张字符串、多张数组）。空串按模型名推断：`gpt-image*` / `chatgpt-image*` → edits；`seedream* / seededit* / qwen-image* / gemini*-image / nano-banana* / *kontext*` → url；其他 none。管理端定价表单可覆盖。
- `GET /generation/models` 图片模型附 `image_input`（生效值）。
- `POST /user/task`：`type=image` 带 `inputs` 而模型是 `none` 时返回「模型 X 不支持参考图或图片编辑…」。主体库并入的参考图遇到 none 模型会被丢弃，只保留提示词里的主体描述。
- 图片任务 inputs 角色：`image / reference / first_frame / character / scene / style` 当原图或参考图（最多 10 张，必须是图片资产，可用同团队主体素材）；`mask` 为蒙版，只有 edits 方式支持。
- 画布标记（涂抹处不透明）在发给 OpenAI 前反转 alpha（OpenAI 约定透明处为要改的区域）。
- `quality` 只对 OpenAI 图像模型传，`low / std / high` → `low / medium / high`。
- 成片向导分镜图：所选图片模型支持参考图时，带上连到该分镜的已生成角色图（role `character`，最多 4 张）。

### 11.5 成片混入配音、视频参考图角色（F05）

- `POST /user/canvas/:id/compose` 请求新增 `voice?: bool`（缺省 true）。每个镜头视频节点按「视频 → 配音」连线（没有连线时按 `sp.shot` 镜号对 `sp.layer=voice` 的配音节点）找已生成的音频资产，写进 `clips[].voice_asset_id / voice_url`。
- 合成时每段：原声压到 35%，配音延后 0.15 秒混入；配音比画面长时冻结末帧补齐（最多延长 10 秒），字幕时间轴按补齐后的时长。
- 配音任务的 `lang` 与成片 `lang` 不一致（空串与 zh 视为中文）的镜头不混入，`clips[].voice_skipped=true`，成片 `warning` 提示「N 个镜头的配音语种与成片语言不一致，未混入」。
- 画布视频「全能参考」模式的参考图 role 由 `image` 改为 `reference`（原来多张都按首帧提交）。（2026-09-14 起视频统一走 `/v1/videos`，`reference` 按外观参考图提交，不再兜底当首帧，见第 18 节。）主体库并入的参考图 role 改为 `subject`（视频映射成 reference、图片任务当参考图），不会被当成首帧。

### 11.6 团队只读画布（F09）

> 2026-09-14 变更：「与所有者同团队」改为「画布所属项目在自己所在团队的团队空间（`project.team_id`）」，见 20。

- `GET /user/project?scope=team` 的行现在也带最近画布 `canvas_id`（原来没有，队友点开卡片只能进空白草稿）。
- `GET /user/canvas/:id`：自己的画布附 `readonly: false`；不是自己的但与所有者同团队（`service.CanAccessProject`）时返回画布并附 `readonly: true, team_shared: true, owner_nickname`，`share_enabled / share_token` 置空，不返回所有者邮箱。其他情况仍是「资源不存在」。移出团队后立即读不到。
- 保存、版本历史、分享、生成、合成仍按 `user_id` 校验，队友调用一律失败；批注走项目评论接口（10.10）。
- 前端：`readonly` 时锁定画布、隐藏加节点工具栏 / 右键菜单 / 快捷键 / 历史 / 保存 / 分享 / Agent 对话，顶栏显示「只读 · 某某的画布」，不自动保存。

## 12. 可控成片（产品建议第 1 期，2026-09-14）

### 12.1 镜头视图 `GET /user/canvas/:id/shots`

按视频节点归拢镜头（`sp.shot` 镜号排序，没有镜号按画布位置）。每个镜头：
`{ no, line, seconds, video, image, voice, character_node_ids }`，其中 `video / image / voice` 为
`{ node_id, asset_id, url, cover_url, duration, task_status, candidates[] }`（image / voice 可为 null）。
分镜图取 `data.refNodeId`，没有则取连进视频的第一个图片节点；配音按「视频 → 配音」连线，没有按镜号。
`candidates` 是该节点生成成功且资产未删除的历史产物（`{ task_id, node_id, asset_id, url, cover_url, duration, model, prompt, created_at }`，新的在前，每节点最多 8 条）。
`generation_task` 新增联合索引 `idx_task_canvas_node(canvas_id, node_id)`。

### 12.2 时间线 `GET / PUT /user/canvas/:id/timeline`

新表 `canvas_timeline(id, canvas_id 唯一, user_id, data text, version, updated_at)`，不放进画布 graph（画布页与向导整份保存 graph 会互相覆盖）。

- GET 返回 `{ version, timeline, shots }`；没保存过 `version=0`，给缺省时间线（有视频的镜头启用、画幅取策划案）。保存过的会与当前画布对齐：已删除的镜头去掉，新镜头追加在末尾。
- PUT `{ version, timeline }`，乐观锁；首次保存 `version=0`；冲突返回「时间线已在其他地方修改，请刷新后再保存」。返回 `{ version, timeline }`（已清洗）。

```
timeline = { aspect: 16:9|9:16|1:1, resolution: 720p|1080p, subtitles, voice, lang, bgm_asset_id, bgm_volume(0~2，缺省 0.2), subtitle_node_id(可选，见 35.4),
  clips: [{ node_id, enabled, asset_id(0=节点当前视频，否则换候选), in, out(秒，out=0 到结尾), voice_mode: mix|replace|off,
            orig_volume(0~2), voice_volume(0~2), subtitle(null=用台词，""=这一镜不要字幕) }] }
```
清洗：非法画幅回落 16:9、分辨率只认 1080p 其余 720p、音量钳到 0~2、入出点保留一位小数、节点去重、最多 60 条、逐镜字幕最多 200 字。

### 12.3 按时间线合成

`POST /user/canvas/:id/compose` 新增 `timeline`（结构同上）。给了就只合启用的镜头（最多 24 个），顺序、候选、入出点、音量、字幕、画幅、分辨率、BGM 全以它为准，旧字段不再生效。
- 换候选：`asset_id` 必须是自己的视频资产。
- `voice_mode=replace` 且该镜有配音时原声音量置 0；`off` 不混配音。字幕优先逐镜字幕，其次该语种台词，最后节点台词。
- 输出尺寸：720p → 1280×720 / 720×1280 / 960×960；1080p → 1920×1080 / 1080×1920 / 1080×1080。
- `compose_job` 新增 `resolution, bgm_volume, qc`。合成后质检 `qc = { expected_duration, duration, width, height, has_audio, issues[] }`：时长偏差超过 0.6 秒、分辨率不符、没有音轨都会写进 `issues` 并追加到 `warning`。
- 缺省音量：有配音时原声 0.35、没有配音保持原音量；配音 1。
- 字幕字号按输出尺寸换算（横屏字高约为画面宽度 4.3%，竖屏约 5%），竖屏不再一句折成三行。
- 网关生成的音视频入库后异步用 ffprobe 回写 `duration / width / height`（原来是 0，时间线只能按策划秒数估算）。

## 13. 服务端批量生成（产品建议第 2 期，2026-09-14）

原来成片向导由前端逐个提交、轮询，关掉页面排队就断了。现在前端把节点连同依赖交给服务端，worker（`service.RunBatchWorker`，每 5 秒）推进。

新表：
- `generation_batch(id, user_id, canvas_id, project_id, status running|paused|done|cancelled, budget_points, estimate_points, spent_points, total, succeeded, failed, pause_reason, created_at, updated_at, finished_at)`
- `generation_batch_item(id, batch_id, node_id, layer, type, model, prompt, params, inputs, depends, status waiting|submitted|succeeded|failed|skipped|cancelled, estimate, task_id, asset_id, attempts, error, seq, updated_at)`

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/user/canvas/:id/batch` | `{ budget_points?, items: [{ node_id, layer, type, model, prompt, params, inputs: [{asset_id, role}], depends: [{node_id, role}] }] }`。同一画布同时只能有一个进行中 / 暂停的批次；最多 80 个节点；节点必须在画布里；依赖指向本批次节点，或画布里已有产物的节点；不能成环；count 只能是 1。返回批次（含估算总积分 `estimate_points`） |
| GET | `/user/canvas/:id/batch` | 画布最近一个批次（含 `items`），没有时 `data=null` |
| POST | `/user/batch/:id/pause` / `resume` / `cancel` / `retry-failed` | 暂停只停止提交（已提交的继续跑并同步结果）；取消会取消还在跑的任务并退分；只重跑失败把 failed / skipped 条目放回 waiting，批次回到 running |

worker 每轮：
1. 已提交条目按任务状态同步（成功记 `asset_id`，失败 / 取消记 `error`）；
2. 依赖失败 / 跳过 / 取消的等待条目级联标 `skipped`（error「上游节点 X 没有生成成功」）；
3. 依赖都成功的等待条目按 `seq` 提交：输入 = 静态 `inputs` + 依赖节点产物（按依赖的 `role`，不在批次里的依赖取画布节点当前 `assetId`）。并发满留到下一轮；`spent + 本条估算 > budget` 时暂停（`pause_reason`）；积分不足暂停；其他错误该条标失败；
4. 没有等待 / 已提交条目时批次标 `done`，发站内信，写事件 `batch_done`（创建时写 `batch_start`）。

任务提交逻辑从控制器抽成 `service.StartTask`，与 `POST /user/task` 共用（并发、存储、估价、能力校验、首帧适配、预扣、提交网关）。

前端（成片向导第 3 步）：「生成本层」「一键生成全部」都走批次，可填预算上限；批次面板显示状态、进度、已用 / 预计积分，并提供暂停、继续、取消、只重跑失败。向导轮询批次，把新提交的 `task_id` 挂到节点上，再由任务轮询取结果；重新打开页面会自动接上。单个节点的「重新生成」仍直接提交。

## 14. 素材生命周期与可复刻模板（产品建议第 3 期，2026-09-14）

### 14.1 素材引用与安全删除

- `GET /user/asset/:id/refs` → `{ refs: [{ type: canvas|subject|work|timeline, id, name, node_ids? }] }`，最多 20 处。画布按节点数据里任意 `assetId / asset_id / *AssetId` 键精确匹配（含 refItems、导演台场景），主体看封面与参考图，作品看成片资产，时间线看换上的候选。不维护引用表，按用户范围 LIKE 粗筛后解析 JSON 确认。
- `DELETE /user/asset/:id`：仍有引用时返回 `success=false`，message「该素材正在被 N 张画布、M 个主体… 使用，删除后这些地方会失效」，`data={ reason: 'in_use', refs }`；确认后带 `?force=1` 再删。前端资产页据此二次确认并列出引用。

### 14.2 主体版本

- `subject` 新增 `version`（缺省 1）；新表 `subject_version(subject_id, version 唯一, kind, name, look, prompt, cover_asset_id, cover_url, refs, created_at)`。
- 名称 / 类型 / 描述 / 提示词 / 封面 / 参考图有变化时版本 +1，并存新快照（旧版本缺快照的先补）。共享开关等不算新版本。新建主体存 v1 快照。
- `GET /user/subject/:id/versions` → `{ version, versions[] }`（自己的或团队共享的，最多 30 条）。
- `POST /user/task` 新增 `subject_versions: { "主体 id": 版本 }`：锁定版本且快照存在时按快照内容并入参考图与描述，否则用最新内容。
- 前端：画布节点引用主体时锁定当时的版本（`data.subjectVersions`），主体芯片显示 `v2`；主体之后更新了会出现「更新到 v3」按钮，手动切换。

### 14.3 做同款预览

- `work` 新增 `template_meta text`：作品开源发布、或改为开源 / 换来源画布时，给来源画布拍快照 `{ captured_at, node_count, generative_nodes, models: [{ model, type, label, nodes, unit, points_per_unit, durations, resolutions, image_input }] }`。
- `GET /work/:id/clone-preview`（可匿名，登录时按会员价估算）→ `{ has_snapshot, captured_at, node_count, generative_nodes, estimate_points, unpriced_nodes, models: [{…, available, priced, changes[] }], missing_refs: [{ node_id, label, fields[] }], subject_nodes }`：
  - `changes` 对比快照：标准价变化、时长 / 分辨率 / 参考图能力变化、模型已下线、暂未定价；
  - `estimate_points` 按当前价格估算全部生成节点各生成一次（视频按节点 `seconds`，缺省 5 秒）；
  - `missing_refs` 列出复制后资产 ID 会被清掉、只剩地址的参考素材字段（参考图、尾帧、风格、标记、音色、角色、场景、手动参考图）与主体引用。
- 前端：播放页「做同款」先弹预览，确认后再复制。

导演台（Vue 版）目前没有本地文件导入入口，素材都来自资产库或画布连线，不存在「本地素材刷新后丢失」的问题，本期不涉及。

## 15. 团队角色（产品建议第 4 期，2026-09-14）

`team_member` 新增 `permission varchar(10) default 'editor'`（`admin / editor / viewer`）；`role` 仍是 `owner / member`，所有者恒为管理员，老数据空值按编辑者。

> 2026-09-14 变更：下表「队友画布 / 队友项目」只指**团队空间**里的项目（`project.team_id>0`）；「用团队积分生成」只发生在团队空间，个人空间里查看者也能花自己的钱生成。见 20。

| 能力 | 所有者 | 管理员 | 编辑者 | 查看者 |
|---|---|---|---|---|
| 打开队友画布 | ✓ | ✓ 可编辑 | ✓ 可编辑 | ✓ 只读 |
| 保存队友画布、看 / 恢复版本历史 | ✓ | ✓ | ✓ | 只能看历史 |
| 在队友画布上生成（花团队积分，受月度额度） | ✓ | ✓ | ✓ | ✗ |
| 用团队积分生成（任何画布） | ✓ | ✓ | ✓ | ✗ |
| 批注（项目评论，可定位到画布节点） | ✓ | ✓ | ✓ | ✓ |
| 分享 / 删除画布、合成、批量生成、时间线 | 仅作者 | 仅作者 | 仅作者 | ✗ |
| 删除 / 恢复队友项目 | ✓ | ✓ | ✗ | ✗ |
| 重置邀请码、看邀请码、设成员额度、移除成员 | ✓ | ✓（不能动其他管理员） | ✗ | ✗ |
| 调整成员权限 | ✓ | ✓（只能在编辑者 / 查看者之间） | ✗ | ✗ |

接口：
- `GET /user/canvas/:id` 附 `permission`（`owner / admin / editor / viewer`）与 `readonly`（查看者 true）；队友画布附 `team_shared, owner_nickname`，不给分享 token。
- `PUT /user/canvas/:id`、`POST /user/canvas/:id/versions/:vid/restore`：编辑者 / 管理员可操作队友画布，按作者 `user_id` 落库；队友保存的版本快照备注「队友 某某 编辑」。
- `POST /user/task` 带队友画布的 `canvas_id` 时要求可写；输入素材可以是自己或同团队队友的。查看者用团队积分生成返回「你在团队里是查看者，不能使用团队积分生成…」。
- `PUT /user/team/member/:uid/permission { permission }`：所有者可设任意；管理员只能在编辑者 / 查看者间调整、不能管理其他管理员；改后给成员发站内信。
- `DELETE /user/team/member/:uid`、`PUT /user/team/member/:uid/limit`、`POST /user/team/invite` 放开给管理员。`GET /user/team` 附 `permission`；管理员也能看到邀请码。
- `DELETE /user/project/:id`、`POST /user/project/:id/restore`：管理员可操作同团队队友的项目。
- 镜头视图 / 时间线 / 合成查找资产时包含队友在这张画布上生成的产物。

前端：画布页区分只读（查看者）与「某某的画布 · 可编辑」（编辑者 / 管理员），队友画布隐藏分享；顶栏「批注」打开画布批注，节点右键「批注」定位到节点；设置页团队成员可调整权限。

## 16. 懂上下文的画布助手（产品建议第 5 期，2026-09-14）

画布右侧对话栏开启「结合画布」后，问题连同选中节点（没选时为整张画布，最多 40 个节点）及其上下游一起发给助手；助手解释问题并给出修改方案，前端展示变更预览与预计积分，用户确认后在画布上执行，可撤销。

- `POST /user/canvas/:id/assist`（需可写画布；限流同生成任务）
  - 请求：`{ question, model?, nodes: [{ id, kind, label, prompt, content, has_media, task_id, selected, upstream: [id], downstream: [id] }], history: [{ role: user|assistant, content }] }`
  - `model` 为文本模型，缺省用策划案模型（设置 `ComposePlanModel`，否则第一个已定价文本模型）。
  - 服务端按 `task_id` 补全节点上次生成的模型、状态和失败原因（只认这张画布、这个节点的任务），拼成提示词后按一次**文本任务**提交：计费、并发、团队额度、失败退分与 `POST /user/task` 一致。返回任务（`id` 用于轮询 `GET /user/task/:id`）。
- `GET /user/canvas/:id/assist/:task_id`（只能取自己发起的）
  - 返回 `{ task_id, status, error_msg, question, reply, changes, dropped, estimated_points, cost_points }`；任务未成功时 `changes=[]`。
  - 模型回复不是 JSON 时整段作为 `reply`，没有修改。引用不存在节点、动作未知或超过 8 条的修改被丢弃，计入 `dropped`。
  - `changes[]` 动作：
    - `update_prompt { node_id, node_label, kind, prompt, before, reason }`：`before` 为提问时的提示词。
    - `regenerate { node_id, node_label, kind, type, model, params, points, reason }`：只用于图片 / 视频 / 音频节点；模型按「助手建议 → 上次生成的模型 → 默认模型」取第一个可用的；沿用上次参数（去掉 `camera / camera_setup / fx / count`，换模型时清空）；上次是图片编辑（`params.edit`）时前端在当前图片上再编辑一次，而不是文生图；`points` 为按当前会员价的预计积分，`estimated_points` 为合计。
    - `add_node { kind: text|image|video|audio, label, prompt, from?, reason }`：`from` 为上游节点，应用时自动连线。
    - `connect { source, target, node_label, reason }`
- 执行：前端把勾选的修改作为一步写进画布撤销历史（`撤销这次修改` 或 Ctrl+Z 恢复）；「应用并生成」对 `regenerate` 逐个走 `POST /user/task`（生成已花的积分不随撤销退回）。

## 17. 创作指标（产品建议第 6 期，2026-09-14）

管理后台「创作指标」页。数据直接取任务 / 合成 / 积分流水表，只有引用丢失靠事件。

- `GET /admin/stats/creation?days=7&idle_minutes=10`（管理员；`days` 1-90，含今天；`idle_minutes` 1-1440）
  - `tasks`：窗口内创建的图片 / 视频 / 音频任务（不含文本对话）的总数、成功、失败、取消、进行中，`success_rate` = 成功 /（成功 + 失败）；`timeout_failed` 为失败原因含「超时」的；`by_type` 分类型。
  - `compose`：合成任务数与成功率；`first_*` 为窗口内**第一次**合成的用户，首次就成功 / 首次失败的人数与首次成功率，`eventually_done / eventual_rate` 为其中至今合成成功过的。
  - `retries`：窗口内每个画布节点（镜头）提交的生成次数。给出平均次数、重试过（>1 次）的节点比例、分桶（1 / 2 / 3 / 4-5 / 6+）与分类型平均。
  - `ref_missing`：`ref_missing` 事件的次数、丢失个数、影响用户，`per_hundred_tasks` 为每 100 个生成任务的丢失次数（不含合成镜头丢失），`by_source` 分来源。
  - `refund`：`backlog_*` 为已失败 / 取消但预扣积分还没退回的任务数、积分、最早结束时间（正常应为 0，补偿任务每 2 分钟兜底一次）；`refunded_*` 为窗口内退回任务积分的笔数与积分。
  - `stuck`：进行中且超过 `idle_minutes` 没有进展的任务（最近进展 = max(进度前进时间, 开始时间, 创建时间)），最多列 20 条，按空闲时间倒序；`compose_jobs` 为排队或合成超过该时长的合成任务。
  - `failures`：失败最多的模型（前 10，含失败率）与最常见的失败原因（前 80 字归并，前 8）。
  - `daily`：每天的任务数 / 失败数、合成数 / 成功数 / 失败数。
- 统计口径（2026-09-14 补）：`tasks` / `retries` / `failures` / `daily` 排除「积分不足」被拒的任务（任务行在扣积分前已插入并标失败，`started_at = 0`，从未提交模型），否则会被算成模型失败。
- `generation_task` 新增 `progress_at bigint`：开始执行时写入，之后只在进度真正前进时刷新。
- 新事件 `ref_missing`（props：`source`、`count`，可带 `canvas_id / type`）：
  - `canvas_unlinked`：创作台提交时有参考素材只有地址、没有资产 ID（复制来的画布），本次不会使用。
  - `input`：`POST /user/task` 的输入素材不存在或无权使用（请求被拒）。
  - `subject`：引用的主体已删除 / 不可见，或主体参考图的素材没了。
  - `compose_clip`：合成时镜头视频已删除或地址失效（请求被拒）。
- 前端不再上报 `node_retake / node_extend / node_upscale`（服务端已记，之前重复计数）。

## 18. 视频生成全量迁移 `/v1/videos`（2026-09-14）——行为变更

旧版网关端点 `POST /v1/video/generations`（只收一张 `image` 首帧）全量下线，所有视频模型统一走 `POST /v1/videos` + `GET /v1/videos/{job_id}`（`input_references` 多素材）。setting `EasyAPIVideoV2Models` 路由开关随之删除（不再需要）。

- **接口契约不变**：`POST /user/task` 的 `type/model/prompt/inputs/params` 与响应字段都不变，前端无需改动。
- **`reference` 语义变化**：只有参考图、没有 `first_frame` 的任务（画布「全能参考」模式）不再把第一张 `reference` 当首帧提交，而是作为「外观参考图」传给网关（影响生成但不锁定画面第一帧）。这类任务的成片效果可能与旧版不同；要锁定首帧请用 `first_frame` 角色。
- **首帧尺寸适配（`frame_match=1`，sora 系）只对 `first_frame` / `image` 角色生效**：`reference` 不再被裁成输出尺寸。
- **参数走顶层字段**：`ratio` / `aspect_ratio`、`resolution`、`size`、`duration`、`generate_audio`（画布 / 向导传的 `audio` 视为同义）、`seed` 均放请求体顶层；`metadata` 只透传白名单 `negative_prompt` / `camera_fixed` / `watermark`，`count` / `mode` / `edit` 等前端业务字段不发给网关（部分渠道会把 metadata 合并进上游请求体，多余字段会被拒）。
- **响应格式（2026-09-14 补）**：创建与查询都兼容两种格式——包裹格式 `{code,msg,data:{job_id,status,download_url,message}}` 与直通格式 `{id,status,progress,error:{message}}`；`code`/`id` 字符串或数字均可。成片地址依次取 `download_url` / `url` / `video_url` / `result_url` / `metadata.url`，都没有时带网关令牌取 `GET /v1/videos/{job_id}/content`。异步失败原因与同步报错共用人话规则（时长 / 分辨率 / 首帧尺寸 / 内容审核）。
- **进度与扩展名**：网关给了 `progress` 就用（压在 30–95），没给停在 30；成片固定按 `.mp4` 落盘。
- **上线注意**：发布前确认网关 `/v1/videos` 支持生产在用的每个视频模型（模型 ID 可能与旧端点不同）；发布瞬间仍在旧端点跑的任务无法恢复轮询（新端点查不到旧 task_id），若网关对旧 task_id 回 404，轮询连续 5 次（约 40 秒）即判失败并全额退分；网关回其他错误时仍走 30 分钟超时退款，建议避开有在跑视频任务时发布。

## 19. 后台「控制台」（2026-09-14，参照 EasyAPI 后台）

管理员在后台用户列表点「控制台」，以该用户身份打开创作台，用于排查用户反馈、代看作品与画布。

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | `/admin/user/:id/console_access` | AdminAuth | 返回 `{ ticket, path: "/console?ticket=…", expires_in: 60 }`。管理员账号只有 Root 能进；禁用用户拒绝 |
| POST | `/user/console_access/exchange` | 匿名 + 严格限流 | body `{ ticket }` → 与登录响应同结构的 `{ id, username, nickname, role, status, group, access_token, console_access: true }` |

- ticket 32 位随机串，60 秒有效、只能兑换一次（Redis 事务 GET+DEL，线上 Redis 5.0 不支持 GETDEL；Redis 不可用时存进程内存）。签发与兑换都写 SysLog（管理员 id → 用户 id）。
- 兑换返回用户现有的 access_token（没有才生成），不更新用户的最近登录时间。
- 管理端用运行时配置 `studioUrl`（`NUXT_PUBLIC_STUDIO_URL`）拼 `path` 后新窗口打开；创作台 `pages/console/index.vue`（全屏 loading）+ `middleware/console-auth.global.ts` 识别 `/console?ticket=` 完成兑换并进入工作室。
- 创作台会话带 `console_access: true`：此时「退出登录」只清本浏览器会话，不调 `/user/logout`，避免作废用户本人的令牌把真实用户踢下线。
- 注意：代登会覆盖该浏览器里创作台原有的登录态。

## 20. 个人空间 / 团队空间切换（2026-09-14）

创作台顶部加空间切换器：**个人空间**与**团队空间**。一个用户仍最多属于一个团队。

产品规则（负责人定稿）：
1. 个人空间**永远花自己的钱包**，团队成员也一样；团队积分池只在团队空间里用。
2. 本次上线前的项目全部留在个人空间；团队空间只显示 `team_id = 该团队` 的项目。
3. 项目作者可以把项目移入团队，也可以移回个人空间。

### 20.1 请求头与中间件

| 头 | 说明 |
|---|---|
| `EasyTV-Team: <team_id>` | 选择团队空间；缺省、空、`0`、非数字、负数都按个人空间。已加入 CORS `Access-Control-Allow-Headers` |

- `middleware.TeamSpace()` 挂在 `/user/**` 登录分组上（在 `UserAuth` 之后）。头 > 0 时校验当前用户是该团队成员：
  - 通过：写入 gin 上下文 `team_id`、`team_permission`（`admin / editor / viewer`，owner 为 `admin`），控制器用 `teamIdOf(c)` / `teamPermissionOf(c)` 读取；
  - 不是成员（被移出、团队不对）：`{ success:false, message:"你已不在该团队，请切回个人空间", data:{ reason:"team_forbidden", team_id } }`，**前端收到后清掉头、切回个人空间并重试**；
  - 查成员表出错：`success:false`「查询团队失败，请稍后重试」（不带 reason，不要切空间）。
- 不看空间头（带了也忽略）：`/user/self`（GET/PUT）与团队管理接口 `/user/team`、`/user/team/*`（含新增的 `/user/team/mine`、`/user/team/usage`），保证被移出团队后切换器照样能拉到最新状态。
- 其他新增的机器可读错误：`data.reason = "team_expired"`（团队版会员过期，message「团队版会员已过期，团队积分暂不可用，请联系团队负责人续费或切回个人空间」），出现在生成类接口。

### 20.2 实体（AutoMigrate 加列，默认 0，不迁移老数据）

| 表 | 新增列 | 含义 |
|---|---|---|
| `project` | `team_id int default 0 index` | 0 个人空间；>0 团队空间 |
| `asset` | `team_id int default 0 index` | 团队空间上传 / 团队任务产物 / 团队合成成片 |
| `generation_task` | `team_id int default 0 index` | 任务所在空间；扣费、退款、产物归属都读它 |
| `compose_job` | `team_id int default 0 index` | worker 据此定水印、给成片打团队归属 |
| `generation_batch` | `team_id int default 0 index` | worker 提交的每个任务沿用它 |

### 20.3 生成与扣费

**任务空间**（`service.ResolveTaskTeam`）：
- 任务挂在项目上（带 `canvas_id` 时取画布所属项目）→ **跟项目走**：项目 `team_id>0` 用项目的团队（用户必须仍是该团队成员，否则「资源不存在」）；项目 `team_id=0` 就是个人空间（即使请求带着团队头）。
- 没有项目（主体参考图、无项目的草稿）→ 用请求空间。
- 适用：`POST /user/task`、`/task/:id/retry|retake|extend|upscale`、`/canvas/:id/assist`、`/canvas/:id/batch`、`/canvas/:id/compose`。主体三视图 `POST /user/subject/:id/generate`、策划案 `POST /user/screenplay` 直接用请求空间；`POST /user/screenplay/:id/build` 新建的项目落在请求空间。

**买单**（`service.PayerForTeam(userId, teamId)`）：

| 空间 | 买单账户 | 会员价（`IsMemberForPricing`） |
|---|---|---|
| 个人空间（任何人） | 自己 | 自己有有效会员（创作版 / 团队版均可） |
| 团队空间，owner 团队版有效 | 团队 owner | 是 |
| 团队空间，owner 团队版过期 / 退款 | 拒绝：`team_expired` | 否 |
| 团队空间，已不是该团队成员 | 拒绝：`team_forbidden` | 否 |

- 团队空间里**查看者**不能生成：「你在团队里是查看者，不能使用团队积分生成，请联系团队管理员调整权限」。个人空间不受团队权限影响。
- 成员月度额度（F7）仍在付款人 ≠ 发起人时校验（即成员在团队空间生成时）。
- `FreezeTaskPoints` 按 `task.team_id` 决定付款人，不再看当前团队关系；批次 / 合成的 worker 读存下来的 `team_id`。worker 执行时用户已不在团队 → 该任务失败。
- 并发与云存储额度（6.9）按空间：个人空间看本人会员，团队空间看 owner 的会员（团队版按全队共享）。
- 合成水印：个人空间看自己是否会员，团队空间看 owner 团队版。签到奖励、积分包 / 会员报价、做同款预览一律按个人。
- 画布的生成产物 `asset.team_id = task.team_id`；末帧 / 首帧适配产生的衍生图沿用来源资产的 `team_id`。

### 20.4 项目

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/project?keyword=&page` | 个人空间：自己 `team_id=0` 的项目。团队空间：`team_id=该团队` 的全部项目（不分作者）。兼容：个人空间带 `scope=team` 等同于自己所在团队的团队空间（新前端改用头，不要再传） |
| POST | `/user/project` | 团队空间创建时 `team_id = 该团队` |
| POST | `/user/canvas` | 不传 `project_id` 自动建的项目：团队空间里落在团队 |
| GET | `/user/project/:id` | 作者本人，或团队项目且自己是该团队成员；返回的 `project` 同样带下列标记 |
| POST | `/user/project/:id/move` | **新增** `{ team_id }`，见下 |

项目行（列表、详情、创建、移动的返回）新增字段：`team_id`、`owner_id`（= `user_id`）、`owner_nickname`（团队列表 / 队友项目详情）、`canvas_id`（最近画布，已有）、`permission`、`can_edit`、`can_delete`、`can_move`、`team_shared`（不是作者时 true）：

| 当前用户 | permission | can_edit | can_delete | can_move |
|---|---|---|---|---|
| 作者（个人或团队项目） | `owner` | ✓ | ✓ | ✓ |
| 团队项目 · 管理员（含 owner） | `admin` | ✓ | ✓（删除 / 恢复） | ✗ |
| 团队项目 · 编辑者 | `editor` | ✓ | ✗ | ✗ |
| 团队项目 · 查看者 | `viewer` | ✗ | ✗ | ✗ |

`POST /user/project/:id/move { team_id }`：
- 只有项目作者能调用（别人的项目「资源不存在」）；`team_id` 缺省或 < 0「无效的参数」；
- 目标与当前相同：「项目已经在这个空间里了」；
- `team_id > 0`：必须是该团队成员（否则 `team_forbidden`），查看者拒绝「你在团队里是查看者，不能把项目移入团队」；
- `team_id = 0`：移回个人空间，不限制（已退团的作者也可以）；
- 同一事务里更新 `project.team_id`，并把**作者本人**在该项目里的资产（`asset.project_id = 项目`，或是项目画布上任务的产物 `generation_task.result_asset_id`，含回收站里的）改成同一 `team_id`。队友在团队项目里产生的资产不动；历史任务的 `team_id` 不改（扣费记录保持原样）。
- 返回更新后的项目行。

访问控制（替换 10.10 / 11.6 / 15 里的「同团队」判断）：`CanvasAccess`、`CanAccessProject` 对非作者要求 `project.team_id > 0 且 = 自己所在团队`；团队项目内角色矩阵沿用 15。删除 / 恢复队友项目要求项目在自己团队的团队空间且自己是管理员。项目批注的「团队 owner 可管理」同样只对团队项目生效。

### 20.5 资产

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/asset` | 个人空间：自己 `team_id=0` 的资产；团队空间：`team_id=该团队` 的全部资产（队友上传 / 生成的都在），行带 `team_id`、`user_id` |
| POST | `/user/asset/upload` | 带 `project_id` 时跟项目走（自己的项目，或自己团队的团队项目）；否则用请求空间。存储额度按该空间校验 |
| GET | `/user/asset/:id` | 自己的，或当前团队空间里的团队资产 |

- 删除、恢复、回收站、引用查询仍只认上传者本人。
- 生成任务的输入素材（`inputs`）：自己的，或任务所在团队空间的资产；个人空间任务只能用自己的。
- 合成 / 镜头视图在画布上找资产：作者的，或画布项目团队空间的资产（个人项目只认作者）。

### 20.6 主体库

- `GET /user/subject`：个人空间只列自己 `team_id=0` 的主体；团队空间列 `team_id=该团队` 的全部主体（`mine` 标出自己建的）。
- `POST /user/subject`、`POST /user/subject/from-asset`：团队空间里创建时 `team_id=该团队, shared=1`；个人空间 `team_id=0`。
- `PUT /user/subject/:id` 不再处理 `shared`（归属在创建时由空间决定）。改 / 删仍只有创建者。
- 任务引用主体（`subject_ids`）：自己的任意主体，或任务所在团队空间的主体。技能模板的团队共享（10.10）不变。

### 20.7 任务列表与积分流水

- `GET /user/task`：个人空间只列自己 `team_id=0` 的任务；团队空间只列**自己**在该团队空间的任务。带 `canvas_id` 时不按空间过滤（画布已经限定范围，避免打开团队画布却看不到自己任务）。
- `GET /user/task/summary`：统计同样按 `user_id + team_id` 过滤。任务详情 / 取消不变。
- `GET /user/points/log`：
  - 个人空间：自己的全部流水（团队 owner 的个人流水里仍会看到团队任务的扣退，因为是同一个账户）；
  - 团队空间 · 管理员（含 owner）：团队 owner 账户里 `biz_type=task` 且 `biz_no` 是 `team_id=该团队` 的任务单号的流水（策划案扣费不是 generation_task，不在其中）；
  - 团队空间 · 编辑者 / 查看者：`{ items: [], total: 0 }`（success=true）。

### 20.8 钱包

`GET /user/wallet`：
- 个人空间：`balance / frozen / expiring_in_7d / membership / is_member / membership_days` 全部是自己的，`team: null`；**不再返回 `team_pool`**。
- 团队空间：`balance / frozen / expiring_in_7d` 是团队积分池（owner 账户）；`membership / is_member / membership_days` 取 owner 的有效**团队版**会员（过期则 `is_member:false`）；另附
  ```json
  "team": { "id": 3, "name": "阿杰的团队", "permission": "editor", "member_limit": 500, "member_used": 120, "member_left": 380 }
  ```
  `member_limit/used/left` 是当前用户的成员月度额度（同 `GET /user/team` 成员行的 `points_limit/points_used/points_left`）；`member_limit=0` 表示不限，此时 `member_left: null`。
- `GET /user/usage` 同样按空间统计。

### 20.9 空间切换器

`GET /user/team/mine`（不看空间头）：

```json
{ "items": [ { "id": 3, "name": "阿杰的团队", "role": "member", "permission": "editor", "member_count": 4, "edition_active": true, "expires_at": 1790000000 } ] }
```

不在团队时 `items: []`。`edition_active=false` 时前端可以提示团队版已过期（团队空间仍能浏览，生成会返回 `team_expired`）。`expires_at` 仅在有效时给值，否则 0。

### 20.10 未覆盖 / 保持原样

- 团队管理接口 `/user/team/*` 行为不变；技能模板团队共享不变；签到永远个人。
- 画布列表 `GET /user/canvas`、项目 / 画布 / 资产回收站仍按作者列出全部空间的数据。
- 分享复制画布、作品「做同款」新建的项目仍在个人空间。
- 策划案扣费没有月度额度校验（与之前一致）。
- 移回个人空间时仍在跑的团队任务，产物按任务的 `team_id` 进团队资产库。

## 21. 发票（2026-09-15，参照 EasyAPI）

发票由第三方「EasyAPI 发票平台」开具与存档，本地**不建表**；已开票金额每次从平台按用户汇总。发票按**账户**开，不分个人 / 团队空间（`/user/invoice/*` 不看 `EasyTV-Team` 头，同 `/user/self`）。

### 21.1 配置（`setting` 表，管理端 `PUT /admin/setting/:key` 编辑，无新增管理接口）

| key | 缺省 | 说明 |
|---|---|---|
| `InvoiceEnabled` | `false` | `"true"`/`"1"` 开启 |
| `InvoiceServiceUrl` | `https://fapiao-api.easyapi.com` | 平台地址（末尾 `/` 自动去掉） |
| `InvoiceAppKey` | 空 | 密钥类：`GET /admin/setting` 只下发 `********`，回传占位值不覆盖 |
| `InvoiceAppSecret` | 空 | 同上 |
| `InvoiceTaxRate` | `0.01` | 开票税率 |
| `InvoiceItemNo` | `3040203` | 税收商品编码 |
| `InvoiceItemName` | `技术服务费` | 开票品名 |
| `InvoiceCategory` | `全电电子普通发票` | 缺省票种 |

`GET /app/config` 新增 `invoice_enabled`（bool），前端据此显示发票入口。

### 21.2 平台用户与可开票金额

- 平台 username：账号是邮箱时用归一化邮箱（与 EasyAPI 发票平台一致）；否则 `easytv_<userId>`。汇总占用时两种都查，避免改用户名后漏算旧票。
- 实付 `paid` = 套餐/积分包订单实付 + 纯充值实付：
  - 订单：本人 `order` 中 `status='paid' AND price>0 AND payment_method NOT IN ('manual','')` 的 `price` 合计（`manual` = 管理端标记已支付的测试 / 补单；已退款订单状态为 `refunded`，不计）。
  - 纯充值：本人 `recharge` 中 `status='success'`、有真实支付渠道、且 `trade_no` 对不上任何订单号的 `money − refunded_amount` 合计（避免与套餐支付单重复）。
- 查额度时按占用总额 FIFO 回写充值单的 `invoiceable_amount` / `invoiced`（作废、红冲后占用变少会把余额还回去）。
- 已开票 `invoiced` = 平台 `GET /invoices`（page 从 0 起，size 100，最多 50 页，以 `last` / `code=0` 结束）中 `state ∈ {0 审核中, 4 开票中, 1 已开票}` 的 `price + tax` 合计；`2 作废 / 3 红冲 / -1 失败 / -9 放弃` 不计。
- 已开票还包括**刚提交、平台列表里还没出现**的申请：`POST /user/invoice/apply` 成功后按商户订单号记下金额（Redis hash `easytv:invoicepending:<uid>`，24 小时；无 Redis 存内存），平台列表出现该 `outOrderNo`（任意状态）或超过 24 小时后不再计入。防止提交后立刻再申请、平台尚未入账导致重复开票。
- 可开票 `available` = `max(0, round2(paid − invoiced))`。
- 平台出错时 fail-closed：`available = 0`，`invoice_error` 给出原因——密钥未配置「发票功能配置不完整，请联系客服」；网络 / 平台错误「发票平台暂时不可用，可开票金额暂不可用」。

### 21.3 接口（登录用户，`/user` 组）

| 方法 | 路径 | 限流 | 说明 |
|---|---|---|---|
| GET | `/user/invoice/summary` | — | 额度总览 |
| GET | `/user/invoice/access-token` | CriticalRateLimit + UserCriticalRateLimit("invoice") | 开票用户令牌，前端持令牌直调平台查发票列表 |
| POST | `/user/invoice/apply` | 同上 | 申请开票（服务端代提交） |

**`GET /user/invoice/summary`**

- 未开启：`{ "enabled": false }`
- 开启：
  ```json
  { "enabled": true, "paid": 399.00, "invoiced": 100.00, "available": 299.00, "invoice_error": "",
    "tax_rate": "0.01", "item_name": "技术服务费", "category": "全电电子普通发票", "service_url": "https://fapiao-api.easyapi.com" }
  ```
- 查订单库失败：success=false「查询可开票金额失败，请稍后重试」。

**`GET /user/invoice/access-token`** → `{ access_token, username, service_url }`

错误（success=false）：「发票功能未开启」；「发票功能配置不完整，请联系客服」；平台返回 `code<0` 时原样给平台 `message`；平台 `code=1` 但无令牌「发票平台未返回有效令牌」；网络 / HTTP 异常「发票平台暂时不可用，请稍后再试」。

**`POST /user/invoice/apply`**

body：
```json
{ "amount": 88.8, "type": "企业", "category": "", "email": "fin@example.com",
  "purchaser_name": "某某科技有限公司", "purchaser_taxpayer_number": "91310000XXXXXXXX",
  "purchaser_address": "", "purchaser_phone": "", "purchaser_bank": "", "purchaser_bank_account": "", "remark": "" }
```

处理顺序与错误（success=false，message 如下）：

1. 未开启「发票功能未开启」；密钥缺失「发票功能配置不完整，请联系客服」；body 解析失败「无效的参数」。
2. 表单校验（字符串先去首尾空白，`category` 为空取 `InvoiceCategory`）：
   - `amount <= 0`「开票金额必须大于 0」；超过两位小数「开票金额最多保留两位小数」
   - `type` 不是 `企业`/`个人`「抬头类型只能是企业或个人」
   - `purchaser_name` 为空「请填写抬头名称」；企业缺 `purchaser_taxpayer_number`「企业抬头需填写纳税人识别号」
   - `category` 匹配 `/专[用票]|增值税专用/` 且无税号「增值税专用发票需填写抬头税号」
   - `email` 为空或格式不对「请填写接收发票的邮箱」
   - `remark` 超过 200 字「备注不能超过 200 字」
3. 用户锁：Redis `SET easytv:invoicelock:<uid> NX EX 30`（释放时比对值再删）；无 Redis 用进程内锁。抢不到「发票申请正在处理，请稍后再试」。
4. 重算可开票金额（同 21.2）：平台出错返回对应 `invoice_error` 文案；`amount > available`「开票金额不能超过可开票金额 ¥299.00」。
5. 取令牌 → `POST {base}/invoice/make`：
   ```json
   { "accessToken": "…", "username": "ada@example.com", "email": "…", "type": "企业", "category": "全电电子普通发票",
     "outOrderNo": "ETI2026091510300081234",
     "purchaserName": "…", "purchaserTaxpayerNumber": "…", "purchaserAddress": "", "purchaserPhone": "",
     "purchaserBank": "", "purchaserBankAccount": "", "remark": "",
     "items": [ { "no": "3040203", "taxRate": "0.01", "sum": "88.80", "name": "技术服务费" } ] }
   ```
   `outOrderNo` = `ETI` + `yyyymmddHHMMSS` + 用户 id + 4 位随机数字；`sum` 为含税金额两位小数。
   平台 `code<0` 或 `code=0` 返回平台 `message`（`code=0` 且无 message 时「发票平台未受理开票申请」）；网络异常「发票平台暂时不可用，请稍后再试」。
6. 成功：`{ "out_order_no": "ETI…", "content": <平台 content 原样> }`，写 SysLog（用户 id、金额、票种、单号），释放锁。

### 21.4 说明 / 未验证

- 与 EasyAPI 的差异：EasyAPI 由前端拿令牌直调 `/invoice/make`，服务端不校验金额；这里改为服务端加锁、重算、校验后代提交。前端仍可用 access-token 直调平台**查询**发票列表 / 下载。
- 平台协议（username 格式、`/invoices` 分页字段、`/invoice/make` 返回的 content 结构、平台是否即时计入刚提交的发票）均按 EasyAPI 代码实现，需用真实 appKey/appSecret 联调确认。若平台列表有延迟，锁释放后立刻再次申请存在重复开票窗口。
- 调平台的错误日志只记接口路径和底层原因，不带 URL 里的 appKey/appSecret。

## 22. 审计日志（2026-09-15）

独立表 `audit_log`，记录登录、用户关键功能使用、管理端写操作与安全事件；与用量日志、`point_log` 互不影响，按自己的保留期清理。

### 22.1 分类与事件

| category | action | 触发 | success | params（`other.op.params`） |
|---|---|---|---|---|
| `login` | `user.register` | 邮箱注册成功 | true | `email`（脱敏 `a***@domain`） |
| `login` | `user.login` | 邮箱密码登录，**令牌签发成功后** | true | — |
| `login` | `user.sms_login` | 手机验证码登录（含自动注册），令牌签发成功后 | true | `mobile`（脱敏 `138****0000`） |
| `usage` | `user.checkin` | 每日签到 | true | `points_earned` |
| `usage` | `recharge.create` / `recharge.complete` | 支付宝 / 微信充值下单、入账（webhook 与查单补单） | true | `trade_no`、`amount`、`payment_method` |
| `usage` | `model.api_call.create` / `model.api_call.complete` | 生成任务创建 / 完成或失败 | 按结果 | `task_id`、`task_no`、`model`、`type`、`provider`、`cost_points` |
| `usage` | `invoice.apply` | 开票申请（表单校验通过后每次都记） | 按结果 | `amount`、`out_order_no`（失败时可能为空）、失败时 `reason`：`busy` / `quota_query_failed` / `platform_unavailable` / `amount_exceeded` / `access_token_failed` / `make_failed`。**不记抬头、税号、邮箱等开票资料** |
| `operation` | 见 `middleware/audit.go` 的 `auditRouteActions`，未命中为 `generic` | `/admin` 下 POST / PUT / PATCH / DELETE（中间件兜底） | 按响应 `success` 字段，解析不到按 HTTP 状态 | 路由参数（如 `id`、`key`）；`generic` 另带 `method`、`route`。**不记请求体** |
| `security` | `admin.access_denied` | `/admin` 接口 AdminAuth / RootAuth 拒绝（未登录、凭证无效、EasyTV-User 头缺失 / 不匹配、账号禁用、权限不足） | false | `reason`：`not_logged_in` / `invalid_token` / `user_header_missing` / `user_header_invalid` / `user_mismatch` / `user_disabled` / `insufficient_role`；`required_role`：`admin` / `root` |
| `security` | `user.login_failed` | 邮箱密码登录账号或密码错误（数据库错误不记） | false | `account`（脱敏）、`reason=invalid_credentials` |
| `security` | `user.sms_login_failed` | 短信验证码错误 | false | `account`（脱敏手机号）、`reason=wrong_code` |
| `security` | `user.console_access_exchange` | 后台「控制台」ticket 兑换成功（API.md 19） | true | `admin_id`、`target_user_id` |

- 行字段：`user_id` / `username` / `actor_role` 是操作者快照。登录失败与未登录的鉴权拒绝是匿名事件：`user_id=0`、`actor_role=0`，`username` 存脱敏账号（登录失败）或空；已登录用户被拒记其 id 与角色。控制台兑换的操作者是签发 ticket 的管理员。
- `ip`、`user_agent`（截断 512 字）、`method`、`route`（路由模板，如 `/admin/user/:id`，不含 query）、`status`、`request_id` 由请求上下文采集；无上下文（查单补单、任务结算）时 `status` 按成败记 200 / 500。
- 去重：`admin.access_denied` 同一实例内「IP + 方法 + 路由模板」每分钟只记一条（进程内计数，多实例各自计数，key 超过 1 万时整表重置），防扫描刷表。登录失败不去重，靠登录接口的 CriticalRateLimit 限流。
- `other` JSON：`{ "op": { "action": "…", "params": { … } }, "device": "…" }`，参数中的数字原样保留。

### 22.2 查询接口

`GET /admin/audit`（AdminAuth；非 Root 看不到 `actor_role=100` 的事件，匿名事件可见）

| query | 说明 |
|---|---|
| `page` | 从 1 起，**最大 500**，超过返回「页码过大，请缩小时间范围或增加筛选条件」 |
| `page_size` | 缺省 10，最大 100 |
| `category` | `login` / `usage` / `operation` / `security`，其他值「无效的日志分类」 |
| `action` | 精确匹配 |
| `user_id` | 精确匹配；非数字「无效的用户 ID」 |
| `username` | 精确匹配 |
| `operator` | 用户名前缀匹配（≤ 64 字） |
| `ip` | 精确匹配 |
| `success` | `true` / `false`，其他值「无效的筛选条件」 |
| `start_timestamp` / `end_timestamp` | 秒级时间戳，闭区间；结束早于开始报错 |
| `keyword` | 模糊匹配 `route`、`content`、`action`（≤ 100 字，`%` `_` 按字面匹配） |

排序 `created_at DESC, id DESC`。响应：

```json
{ "success": true, "message": "", "data": {
  "items": [ { "id": 12, "event_id": "…", "user_id": 0, "username": "", "actor_role": 0, "created_at": 1789459200,
    "category": "security", "action": "admin.access_denied", "ip": "1.2.3.4", "user_agent": "…",
    "method": "GET", "route": "/admin/user", "status": 200, "success": false, "request_id": "…",
    "content": "管理接口鉴权拒绝：未登录",
    "other": { "op": { "action": "admin.access_denied", "params": { "reason": "not_logged_in", "required_role": "admin" } } } } ],
  "total": 1, "page": 1, "page_size": 10 } }
```

索引：`(user_id, created_at)`、`(category, created_at)`、`created_at`、`category`、`action`、`username`、`request_id`、`event_id` 唯一。存量库由 AutoMigrate 补建新增的 `idx_audit_category_time` 与 `idx_audit_log_action`。

### 22.3 保留期

| key | 缺省 | 说明 |
|---|---|---|
| `AuditLogRetentionDays` | `180` | 保留天数；`0` 永久保留；负数或非数字按 180。管理端「系统设置 → 基础」编辑 |

后台任务启动 10 分钟后先跑一次、之后每 24 小时：删除 `created_at` 早于「现在 − N 天」的记录，每批先取 5000 个 id 再按 id 删除，批间停 200ms。

### 22.4 写入方式与注意

- **异步写入**：请求元数据在调用时同步取出，入进程内有界队列（容量 1000），单个后台协程每次最多攒 100 条批量 INSERT；批量失败逐条重试。队列满时**丢弃新事件**并记 SysError（第 1 条及每 100 条一次），不阻塞业务请求。
- 因此：写入有秒级以内延迟，接口返回后立刻查询可能还查不到；进程被强杀时队列里未落库的事件会丢；进程正常退出前 `closeDB` 最多等 5 秒刷盘。测试用 `model.FlushAuditLogs(timeout)` 同步等待，`model.ReplaceAuditLogWriter` 捕获写入内容。
- 参数禁止放明文邮箱、手机号、凭证、请求体；需要账号时用 `model.MaskAccount` / `MaskEmail` / `MaskMobile`。`username` 列对已登录用户仍是其账号（与用户表一致）。

## 23. 媒体工具（素材后处理，2026-09-16 起）

画布工具条上的 20+ 个入口统一走这一章：本地 ffmpeg 动作服务端真跑（免费、真产物），
AI 动作按「能力位」接模型（运营配了模型即上线，没配就明确不可用），
已经实现的（重拍 / 续写 / 重新生成）只在清单里指路，不另造一套。

### 23.1 实体（新增表 / 字段）

| 表 | 字段 | 说明 |
|---|---|---|
| `media_job` | `id, user_id, team_id, engine, action, source_asset_id, task_id, node_id, canvas_id, project_id, params text, outputs text, status, progress, output_asset_id, output_url, error, created_at, started_at, finished_at` | 媒体工具作业。`engine`：`local` 本地 ffmpeg / `model` 走生成任务；`status`：`0` 排队 / `1` 处理中 / `2` 成功 / `-1` 失败；`outputs` 为 `[{asset_id,url,type,name,label,task_id?,time?,width?,height?,text?,cues?,warning?,warning_code?,warning_params?}]`（`width/height` 为图片 / 视频产物像素尺寸，2026-09-24 起下发；`text` / `cues[{start,end,text}]` 为语音转字幕的 SRT 与逐句时间轴，`warning` 为给人看的中文保留说明，`warning_code` / `warning_params` 给前端拼 i18n，见 35） |
| `model_price` 新增 | `abilities varchar(255) default ''` | 能力标签，逗号分隔：`upscale / remove_subtitle / vocal_separation / bgm_separation / matting / subject_remove / subject_modify / subject_replace / motion_capture / native_audio`（`native_audio`=视频模型能按提示词里的台词原生出声，口播预设只列这类模型；按名推断 sora / veo-3 / seedance-2 / wan2.6+ / Kling 2.6+、3.0、O1、Omni；`video_reference`=视频模型能接收 `reference_video` 素材，全能参考 / 爆款复刻只列这类模型，按名推断 seedance-2 / wan r2v 系列；Kling 全系（含 O1、Omni）经本网关带参考视频必败，不在其列，sora / hailuo / veo 与 wan t2v 同理，见 29）。空串按模型名推断（`*upscale*` / `*超分*` / `*subtitle*` / `*demucs*` / `*vocal*` / `*matting*` / `*mocap*` 等）；管理端非法值被丢弃而不是报错 |

资产新增来源 `source=media`（本地处理产物）。

### 23.2 路由（`UserAuth` + `EasyTV-Team`；创建限流 30 次/分钟）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/media/actions` | 动作清单与可用性（前端整条工具条据此驱动） |
| GET | `/admin/media/actions` | AdminAuth | 盘点：用户侧可用性 + `priced_models`（已定价具备能力）+ `gap`（`disabled` / `no_ffmpeg` / `no_model` / `not_on_gateway`）+ `ffmpeg` |
| POST | `/user/media/jobs` | 创建媒体处理作业，限流 `media` |
| GET | `/user/media/jobs` | 作业列表；query `canvas_id / project_id / asset_id / action / active=1` + 分页 |
| GET | `/user/media/jobs/:id` | 作业详情（模型动作的状态实时由生成任务映射） |

创建请求：

```json
{ "action": "crop", "asset_id": 123, "task_id": 0, "params": { "rect": { "x": 0, "y": 0.8, "w": 1, "h": 0.2 } },
  "node_id": "video-3", "canvas_id": 12, "project_id": 5, "model": "" }
```

`asset_id` 与 `task_id` 二选一（后者取该任务产物）；团队空间里队友的素材也能处理。响应：

```json
{ "success": true, "message": "", "data": { "job": {
  "id": 8, "engine": "local", "action": "crop", "status": 0, "progress": 0,
  "source_asset_id": 123, "task_id": 0, "node_id": "video-3", "canvas_id": 12, "project_id": 5,
  "params": { "rect": { "x": 0, "y": 0.8, "w": 1, "h": 0.2 } },
  "outputs": [], "output_asset_id": 0, "output_url": "", "error": "",
  "created_at": 1789459200, "started_at": 0, "finished_at": 0 } } }
```

**模型动作创建时同步提交生成任务**：任务计费失败（积分不足 / 未定价 / 并发满）时创建接口直接报错且不落作业；
任务提交成功但网关同步报错时，任务已标失败退分，作业状态同样是 `-1`。
取消模型动作走既有 `POST /user/task/:id/cancel`（作业里带 `task_id`）；本地动作秒级完成，不设取消。

### 23.3 动作清单

**本地动作（`engine=local`，免费，立即可用；服务器没装 ffmpeg 时不可用）**

| action | 名称 | 源素材 | 产物 | 参数 | 实现 |
|---|---|---|---|---|---|
| `crop` | 画面裁切 | 视频 / 图片 | 同源 | `rect{x,y,w,h}`（归一化 0~1，必填）、`size?`（`720p` 或 `1280x720`） | `crop` 滤镜，宽高取偶数，可选 `scale` |
| `grid_split` | 宫格切分 | 图片 | 图片 ×N | `cells`（归一化矩形数组 `[{x,y,w,h}]`，1~25 个）、`size?` | 逐片走与 `crop` 相同的滤镜，一次作业出 N 个产物，`label=切片`、`name=切片 NN · 源名`，顺序与 `cells` 一致，`outputs[].width/height` 为切片像素尺寸（前端按它定节点比例）。不在浏览器里裁：OSS 图片没有 CORS 头读不到像素；也不拆成 N 个 crop 作业：单用户排队上限 3 个（2026-09-24，L4） |
| `montage` | 分镜组拼接 | 图片 ×N | 图片 | `asset_ids`（有序图片 id，2~25 张，须含 `asset_id` 本身）、`cols?`（1~10，缺省接近正方形：4 张 2 列、9 张 3 列）、`ratio?`（`original` / `1:1` / `16:9` / `9:16` / `4:3` / `3:4`，缺省 `original` 按第一张的比例）、`gap?`（0~64 px，缺省 8）、`number?`（左上角序号，缺省 false）、`cell?`（单格宽 256~1024，缺省 512）、`bg?`（`white` / `black` / `#rrggbb`） | 每张 `scale`+`pad` 到同一格子尺寸 → `concat` → `tile=C×R`（最后一行不满补背景色），一帧 jpg。多图归属在创建时逐张校验（团队空间认队友素材）；`number` 依赖 ffmpeg 的 `drawtext` 与服务器字体，缺任一项静默跳过序号。产物 `label=分镜组`，资产带整图宽高（2026-09-23，L4） |
| `trim` | 片段截取 | 视频 / 音频 | 同源 | `start`、`end` 或 `duration`、`accurate?`（默认 true） | 默认重编码保证起止点精确；`accurate=false` 走 `-c copy`（快但按关键帧切） |
| `split` | 视频分割 | 视频 / 音频 | 同类 ×2 | `at`（分割点秒数） | 在 `at` 秒切成前后两段（重编码，切点精确），两段都至少 0.3 秒；`outputs` 顺序固定为前段、后段 |
| `remove_silence` | 去停顿 | 视频 / 音频 | 同源 | `threshold?`（dB，缺省 -35）、`min_silence?`（秒，缺省 0.6） | `silencedetect` 找静音 → `select` / `aselect` 只保留有声段并重排时间戳，音画同步；没检测到停顿时原样交回并带 `outputs[].warning` + `warning_code=no_silence`，见 35.3（2026-09-24，L8） |
| `extract_audio` | 音轨提取 | 视频 | 音频 | `format?`（`m4a` / `mp3`） | `-vn`；源没有音轨时报「这段素材没有音轨」 |
| `mute_video` | 无声视频 | 视频 | 视频 | — | `-c:v copy -an`，容器不兼容时回退重编码 |
| `mix_bgm` | 混入配乐 | 视频 | 视频 | `audio_asset_id`（音频资产，必填）、`volume?`（0.05~1，缺省 0.2） | 配乐循环铺满原片时长，尾部淡出后与原声 `amix`；原片没有音轨时配乐成为唯一音轨。视频流优先复制，失败再重编码。画布「配乐」先走 `POST /user/task` `type=audio` 生成这条音频，再提交本动作，原片保留，产物是旁边的新视频 |
| `split_av` | 音视频分离 | 视频 | 音频 + 视频 | `format?` | 一次出两份产物，前端挂成「音轨」「无声」两个节点 |
| `extract_frame` | 取帧 | 视频 | 图片 | `at`：`first` / `last` / 秒数 | 末帧沿用 `-sseof -0.5`，容器不支持时回退首帧 |
| `extract_frames` | 抽帧 | 视频 | 图片 ×N | `mode`（`interval` / `scene` / `spread`）、`count`（≤ 24）、`interval?` | 按固定间隔、画面切换或按 `count` 均匀铺满整段抽帧，最长边压到 1280；`outputs[].time` 为该帧在源视频里的秒数（`scene` 模式不填）。作业开始时若源视频缺 `duration` / `width` / `height` 会用本地文件探测后写回资产 |
| `storyboard` | 逐帧拉片 | 视频 | 图片 ×N + 文本 | `count`（默认 4）、`interval?`、`analyze?`（默认 true） | 抽帧后再建一个 `type=text` 生成任务做画面解读，`outputs` 里带上 `task_id` 供二次轮询 |
| `compress` | 压缩 | 视频 / 图片 | 同源 | `target_height?`、`crf?`（默认 23） | 降分辨率 + 提高 CRF |

**模型动作（`engine=model`，按能力位挑模型，计费走生成任务）**

| action | 名称 | 能力位 | 源素材 | 产物 |
|---|---|---|---|---|
| `upscale` | 高清 | `upscale` | 视频 / 图片 | 同源 |
| `remove_subtitle` | 智能去字幕 | `remove_subtitle` | 视频 | 视频 |
| `vocal_separation` | 人声提取 | `vocal_separation` | 视频 / 音频 | 音频 |
| `bgm_separation` | 背景音提取 | `bgm_separation` | 视频 / 音频 | 音频 |
| `matting` | 智能抠像 | `matting` | 视频 / 图片 | 视频 |
| `subject_remove` | 主体消除 | `subject_remove` | 视频 / 图片 | 同源 |
| `subject_modify` | 主体修改 | `subject_modify` | 视频 / 图片 | 同源 |
| `subject_replace` | 主体替换 | `subject_replace` | 视频 / 图片 | 同源 |
| `motion_capture` | 深度动作捕捉 | `motion_capture` | 视频 | 视频 |
| `depth_extract` | 深度提取 | `depth_extract` | 视频 / 图片 | 同源（深度图 / 深度视频）。没配能力模型时 `available=false`，前端不落空节点 |
| `transcribe` | 语音转字幕 | `transcribe` | 视频 / 音频 | 文本（SRT，`outputs[].text/cues`）；不走生成任务提交路径，本地 worker 抽音轨后调网关转写，按源时长预扣、按实际秒数结算，见 35.2（2026-09-24，L8） |

模型动作的参数：`model?`（留空自动挑）、以及各动作自己的业务参数（`rect` / `prompt` / `resolution` / `scale` / `mode`）。
请求参数**原样透传给网关**，多余键会被忽略；任务类型取该模型在 `model_price` 上的 `type`，素材按 `reference_video` / `reference_audio` 角色传入。`depth_extract` 的图片源按 `image` 角色，并按源素材类型优先挑同类型模型。

**既有动作（`engine=existing`，清单里指路）**

| action | 名称 | 接口 |
|---|---|---|
| `retake` | 整段重拍 | `POST /user/task/:id/retake`（见 10.3）；只重做选中片段的「片段重拍」见 23.8 `retake_segment` |
| `extend` | 智能续写 | `POST /user/task/:id/extend`；没有来源任务的视频资产（上传 / 处理产物）走 `POST /user/asset/:id/extend`，见 30 |
| `regenerate` | 重新生成 | `POST /user/task/:id/retry` |

### 23.4 能力清单

`GET /user/media/actions` 返回动作数组，元素在动作定义之外多三个字段：

```json
{ "action": "remove_subtitle", "label": "智能去字幕", "engine": "model", "ability": "remove_subtitle",
  "source": ["video"], "outputs": ["video"],
  "available": false, "reason": "暂未接入智能去字幕模型，请联系运营", "models": [], "free": false }
```

- 本地动作：`available` 取决于服务器有没有装 ffmpeg；`free=true`。
- 模型动作：`models` 列出所有**已定价、已接入且具备该能力**的模型（`available=false` 时为空）。
- 既有动作：`endpoint` 给出接口写法。
- 被运营关掉的动作（`MediaActionsDisabled`）:`available=false`，`reason="该功能已被关闭"`。

`GET /admin/media/actions` 在同一份动作上多三个运营字段：`priced_models`（已定价、具备能力，不论网关有没有）、`gap`（`disabled` 开关关了 / `no_ffmpeg` 没装 ffmpeg / `no_model` 没打能力标签也推不出模型 / `not_on_gateway` 定价有了但网关没这个模型）、顶层 `ffmpeg`。用户接口不暴露这些，避免把运维术语给到画布。

`GET /generation/models` 的每一项也新增 `abilities` 数组，前端可以直接用它点亮选择器里的模型。

### 23.5 计费与配额

- 本地动作**不计积分**（与成片合成口径一致），只受 `CheckStorage` 存储额度与队列上限约束。
- 模型动作走 `POST /user/task` 同一套：`EstimateTaskPoints` 预扣、失败自动退款、团队积分池与成员月度上限照常生效。
- 队列上限：单用户排队 + 处理中 ≤ `MediaJobMaxPerUser`（默认 3），全局 ≤ `MediaJobMaxQueue`（默认 20），
  超出报「你已有 N 个处理任务在排队…」/「处理任务排队中，前面还有 N 个任务…」。
- 审计：创建写 `usage / media.process.create`（含 job_id、action、asset_id、engine、task_id），
  完成与失败写 `usage / media.process.complete`。

### 23.6 配置项（setting 表，管理端 `PUT /admin/setting/:key` 热更新）

| key | 缺省 | 说明 |
|---|---|---|
| `FFmpegPath` | PATH 里的 `ffmpeg` | ffmpeg 可执行文件路径；缺省回退环境变量 `FFMPEG_PATH` |
| `FFprobePath` | PATH 里的 `ffprobe` | ffprobe 可执行文件路径；缺省回退环境变量 `FFPROBE_PATH` |
| `MediaActionsDisabled` | 空（全开） | 禁用的动作，逗号分隔（如 `storyboard,compress`） |
| `MediaJobMaxQueue` | `20` | 全局队列上限 |
| `MediaJobMaxPerUser` | `3` | 单用户队列上限 |
| `MediaStoryboardModel` | 空（自动挑） | 逐帧拉片解读用的文本视觉模型 |

可执行文件路径在启动时与每 60 秒的配置热同步里解析一次（`service.ApplyMediaConfig`），运行期不再查配置表。

### 23.7 处理细节与限制

- 抽帧一次最多 `24` 张；源素材下载上限 `1GB`；本地动作整体超时 `10` 分钟、并发 `4`。
- 进程重启或崩溃导致作业卡在处理中：超过「超时 + 2 分钟」由 `ResetStaleMediaJobs` 标失败，不做断点续跑。
- 逐帧拉片的解读任务受文本模型单次带图上限约束，最多读取前 4 帧（抽出来的帧本身都会进资产库）。
- 本地产物入资产库 `source=media`，视频自动抽封面、音视频自动补时长（ffprobe）。
- 处理失败会推一条站内通知（`NotificationTypeTask`），点进去落在 `/create?media=<job_id>`。

### 23.8 真·片段重拍 `retake_segment`（engine=pipeline，2026-09-16）

10.3 的 `/user/task/:id/retake` 是「整段重跑」；工具条上的「片段重拍」要的是**只重做选中的那一段、其余画面原样保留**。
走媒体作业：

```json
POST /user/media/jobs
{ "action": "retake_segment", "asset_id": 123, "task_id": 0,
  "params": { "start": 2.0, "end": 4.5, "prompt": "她转身把手机塞进口袋", "model": "", "keep_audio": true },
  "node_id": "video-7", "canvas_id": 12, "project_id": 5 }
```

| 参数 | 说明 |
|---|---|
| `start` / `end` | 秒；`end > start`，段长 ≥ 0.5 秒且不超过源视频；`end` 可等于总时长 |
| `start_ratio` / `end_ratio` | 0~1 比例，没给秒数时用它（前端因素材跨域读不到时长的场景），服务端按探到的源时长换算成秒 |
| `prompt` | 重拍这一段的提示词；留空沿用源任务提示词（源是上传素材时必填） |
| `model` | 视频模型，留空取 `ComposeVideoModel` → 源任务模型 → 清单第一个 |
| `keep_audio` | 缺省 `true`：成片整条沿用源视频原声（重拍段画面换了、声音不换）；`false` 时重拍段用新片段自带的声音 |

流程（服务端）：① ffmpeg 取 `start` 处一帧作首帧、`end` 处一帧作末帧（`end` 到结尾时不取末帧）→ ② 建一条视频生成任务
（`mode=fl2v`，`duration` 取模型 `durations` 里 ≥ 段长的最小档，没声明档位时取 `ceil(段长)` 且 ≥ 3 秒，`inputs` 带 `first_frame`/`last_frame`，`retake_of` 记源任务）
→ ③ 任务成功后 worker 把新片段按段长裁切并缩放到源尺寸，与 `[0,start)`、`[end,总长)` 拼成整条，原声按 `keep_audio` 处理，产物入资产库。

- 响应同 23.2，`engine=pipeline`；状态：任务排队 / 生成中映射为 `0 / 1`，任务失败 → `-1`（积分已按生成任务退回），拼接失败 → `-1`（`error` 写明，生成的片段仍进资产库不丢）。
- `outputs[0]` = 整条成片 `{type:"video", label:"重拍成片"}`，`outputs[1]` = 重拍的片段 `{type:"video", label:"重拍片段"}`；`task_id` 为生成任务（取消走 `POST /user/task/:id/cancel`，取消后作业标失败）。
- 生成任务参数：同模型沿用源任务参数，换模型只保留画幅；**音频开关（`audio` / `generate_audio`）一律不带**（hailuo 等模型会 400）。任务失败且带了末帧时，worker 自动去掉 `last_frame` 重提一次（不少模型不收末帧），只重试一次。
- 计费：只有生成任务那一段按模型定价扣分；拼接免费。队列：与本地动作共用 `MediaJobMaxPerUser` / `MediaJobMaxQueue`。
- `GET /user/media/actions` 里 `retake_segment` 的 `available` = 有 ffmpeg 且至少一个已定价视频模型；`models` 列出可选视频模型。
- 前端：`retakeParams.start/end` 是 0~1 的比例，提交前乘源时长换算成秒。

## 24. 短剧复审修复（2026-09-16）

对应 `SHORT_DRAMA_REVIEW_2026-09-15.md` 第 2 节 D01–D07。回归测试：`model/screenplay_review_test.go`、`model/canvas_timeline_review_test.go`、`service/drama_review_test.go`（D01 真跑 ffmpeg）、`tests/screenplay_review_test.go`。

### 24.1 剧本修订号与「同步到画布」（D02）

`screenplay` 表新增 `revision`、`built_revision`（AutoMigrate 加列，缺省 0）。

- `PUT /user/screenplay/:id` 每次保存 `revision + 1`；`POST /user/screenplay/:id/localize` 写回译文同样 +1。
- `POST /user/screenplay/:id/build` 首次建画布时记 `built_revision = revision`。
- `GET /user/screenplay/:id` 与所有返回 `screenplay` 行的接口新增 `revision / built_revision / stale`：`stale = status==2 && canvas_id>0 && revision != built_revision`（已建画布后剧本又改过）。
- `POST /user/screenplay/:id/build`（已建画布时）：
  - 不带 `sync`：照旧返回 `{ canvas_id, project_id, estimate_points: 0, existing: true }`，另带 `revision / built_revision / stale`。
  - 带 `{ "sync": true }`（可同时带 `image_model / video_model / audio_model`，只用于补建节点的建议模型）：按镜号把 plan 同步进既有画布，响应
    `{ canvas_id, project_id, synced_shots: [镜号], added_shots: [镜号], removed_shots: [镜号], revision }`，并把 `built_revision` 推到 `revision`。

同步规则（`service.SyncScreenplayCanvas`，纯逻辑在 `syncPlanGraph`）：

| 情况 | 处理 |
|---|---|
| plan 里的镜号在画布有 `sp.layer=shot_video` 节点 | 只改视频节点 `data.prompt`（镜头运动 + 画面）、`data.line`、`data.lines`（多语言）、`data.seconds`，分镜图节点 `data.prompt`，配音节点 `data.prompt / data.text / data.lines`；**不动** `assetId / taskStatus / position`，不重新生成素材 → `synced_shots` |
| 该镜有台词但画布没有配音节点 | 补建 `audio-N`（`sp.layer=voice`）并连线 视频 → 配音 |
| plan 里的镜号画布没有 | 与建画布同样补建 分镜图 → 视频（→ 配音）三层节点（id 取空闲的 `image-N / video-N / audio-N`），连角色图 → `added_shots` |
| 画布有而 plan 没有的镜号 | 只在 `removed_shots` 列出，**不删节点** |
| 策划案文本节点（`sp.layer=plan`） | `content` 重写为新摘要 |

画布用乐观锁保存（`canvas.version` 冲突返回「画布已在其他地方修改」），成功后记一条 `manual` 版本快照「同步剧本第 N 稿」。
已知取舍：镜号由 `Normalize` 重新编号，删中间一镜会让后面的镜头「顶上来」按镜号覆盖既有节点内容；稳定镜头 id 留到下一轮。

### 24.2 策划案超限与引用校验（D03）

`model.Plan.Normalize` 不再截断：镜头 > 12 / 人物 > 4 / 场景 > 4 返回 `ErrPlanLimit`，文案形如「策划案超出上限：镜头最多 12 个，当前 13 个，请删减镜头或拆成多集」；
镜头引用了不存在的人物 / 场景 key 返回 `ErrPlanRef`，形如「策划案引用不存在：第 2 镜引用了不存在的人物 c5」（场景可留空，人物列表里的空串会被清掉）。
`PUT /user/screenplay/:id` 把这两类错误原样放进 `message`，原稿不落库；自动策划案路径（`POST /user/screenplay`）遇到模型超限会带原因重试一次，仍失败则报「模型输出策划案超出上限：…，请换个说法再试」。

### 24.3 时间线片段 id（D04）

`timeline.clips[]` 新增 `id`（`c_` + 8 位随机串，服务端为空 / 重复时生成）。归一化改为**按 `id` 去重**，同一 `node_id` 可以出现多次（A / B / A 正反打、反应镜头），各自独立入出点、音量、字幕。
`GET /user/canvas/:id/timeline` 与画布对齐时按 `node_id` 判断节点是否还在，不再按节点去重；合成片段 `compose_job.clips[]` 带 `clip_id`。
`asset_refs` 的时间线引用仍按 `node_id` 列出。

### 24.4 时间线保存失败（D05，前端）

`CreateTimeline.vue` 保存失败不再自动重载：保留本地草稿与未保存标志，展示错误；乐观锁冲突（「时间线已在其他地方修改」）时给两个选择 ——「用服务端版本」（丢弃本地改动重载）/「保留我的修改并覆盖」（取最新 version 再存一次）。冲突未处理前不自动保存。接口无变化。

### 24.5 短片段字幕（D06）

`BuildSRT` 内缩量取 `min(0.2 秒, 片段时长 × 10%)`，保证 `start < end`（0.3 秒片段 → `00:00:00,030 --> 00:00:00,270`）；时长 ≤ 0 的片段不出字幕。

### 24.6 BGM 音量 0（D07）

- `timeline.bgm_volume` 区分「未设置」与「明确为 0」：JSON 里**缺失**该字段解码为 -1（`model.TimelineVolumeUnset`）→ 归一化回落 0.2；传 `0` 保持 0（静音）。`PUT /user/canvas/:id/timeline` 与 `POST /user/canvas/:id/compose` 的 `timeline` 都按此解码。
- 合成：`bgm_volume == 0` 时即使绑定了 `bgm_asset_id` 也不下载、不混 BGM；`compose_job.bgm_volume` 去掉了列缺省 0.2（否则 GORM 建行会把 0 顶成 0.2），建行时始终显式写值（旧接口不带时间线的合成写 0.2）。

### 24.7 配音变长不再带回已裁掉的画面（D01）

`normalizeClip` 先把画面与原声严格截到 `[in, in + (out - in)]`（`trim / atrim`），再对截断后的画面 `tpad` 冻结末帧、`apad` 补静音到配音长度（仍最多延长 10 秒）。
原出点之后的素材在任何配音长度下都不会出现；接口无变化。

### 24.8 稳定镜头 id（D02 后续，2026-09-17）

- `plan.shots[].id`：`sh_` + 8 位随机串，`Normalize` 给缺失 / 重复 / 超长的镜头重发，**客户端保存时必须把服务端返回的 id 原样带回**（否则视为丢失，见下）；`no` 只是展示序号，每次归一化按顺序重排。
- 建画布与同步把 id 写进节点 `data.sp.shot_id`（`shot_image / shot_video / voice` 三层）。
- `build {sync:true}` 匹配规则：plan 里只要有一个镜头 id 能在画布上找到 → **id 模式**：按 id 认领节点并改写 `sp.shot` 与默认标签（「镜头 N / 分镜 N / 配音 N」，用户改过的名字不动），序号只给没有 `shot_id` 的老节点兜底；没被认领的镜头节点列进 `removed_shots`（按它原来的镜号），**不会被后面的镜头顶上来覆盖**。一个 id 都对不上（老画布或 id 丢失）→ 退回纯序号匹配，行为同 24.1。
- 前端：策划案编辑器新增镜头不带 id，保存后用服务端返回的 plan 替换本地副本；移动 / 删除保持 id 不变。

### 24.9 口播视频预设（前端预设，2026-09-17）

不新增接口。画布视频节点新增「口播」模式：台词 → 自动算时长档（约 3.5 字/秒 + 1 秒收尾，落到模型 `durations` 档位，缺省 3~15 秒）→ 固定机位近景口播提示词 → 连线的角色图 / 主体图作 `first_frame`；提交为普通视频任务：`params.mode = i2v|reference`，`params.audio = true`，`params.preset = "talking"`（网关忽略未知键，重试沿用）。模型下拉只列 `abilities` 含 `native_audio` 的视频模型。
- 实测（2026-09-17）：网关 `/v1/videos` 对 sora-2 / hailuo-02 都回「model does not support setting generate_audio」，所以 provider 在收到含 `generate_audio` 的 400 时会**去掉该键自动重提一次**（`service/providers/easyapi.go`）；原生带声模型不带开关也出声。sora-2 8 秒口播实测：任务 191 → 资产 167，1280×720 带语音（平均 -20.5 dB）。

## 25. 短剧第二批：把一场双人对白戏做完整（2026-09-17）

对应复审 G02 / G03 / G04 / G06 / G07 的最小版本。沿用画布、主体库、批量任务与时间线，不新增独立编辑器。

### 25.1 对白条目与角色声音（G02）

- `plan.characters[]` 新增 `voice`（TTS 音色名，如 `nova`）、`voice_style`（说话风格）。
- `plan.shots[]` 新增 `dialogue: [{ speaker, text, emotion?, lines? }]`：`speaker` 为人物 key，空串 / `narrator` 为旁白；每镜最多 12 条、每条 200 字；非空时 `line` 由各条正文用空格拼出（老前端与整镜字幕仍可用）。`Normalize` 校验说话人存在（`ErrPlanRef`）。
- 配音节点 `data.segments: [{ speaker, name, voice, style, text, emotion }]`（建画布与同步都写），所有段同一音色时另给 `data.voice`。前端提交音频任务时把 `segments` 原样放进 `params.segments`（每项可带 `pause` 秒，缺省 0.35）。
- 音频任务 `params.segments` 非空且（多段或带音色）时：provider 逐段 TTS → ffmpeg 段间补静音拼成一条 mp3 → 入库，并把各段起止写进任务 `result_text`：`{"cues":[{speaker,text,start,dur}]}`（`start` 相对音频开头）。服务器没装 ffmpeg 时退化为整段一次 TTS（音色取第一段）。
- 合成：时间线片段没手改字幕（`subtitle` 为 null）时，字幕按配音各段出条（起点加 0.15 秒混音延后，不超出本镜）；成片语种非中文时按序号换成对白译文。`compose_job.clips[].cues` 可见。
- 翻译（`/screenplay/:id/localize`）按对白条翻，译文写进 `dialogue[].lines[lang]`，整镜 `lines[lang]` 用空格拼回。

### 25.2 本镜人物状态（G03）

`plan.shots[].states: { <人物 key>: "穿病号服，左手缠绷带" }`（每条 100 字，key 必须是人物）。分镜图与视频提示词在该人物外貌后追加「（本镜：…）」，只影响这一镜；主体库的版本锁定照旧。

### 25.3 正反打模板（G04）

`POST /user/screenplay/:id/dialogue-shots`

```json
{ "scene": "s1", "a": "c1", "b": "c2", "seconds": 4, "insert_at": 0,
  "lines": [{ "speaker": "c1", "text": "手机呢", "emotion": "试探" }, { "speaker": "c2", "text": "在我这" }] }
```

生成：全景建立（A 左 B 右）→ 每句一个说话人的过肩近景（每镜只带自己那句 `dialogue`，提示词写明机位在轴线同一侧）→ 每 3 句后插一个听者反应近景 → 收尾双人中景。最多 10 句；加上后超过 12 镜报错、策划案不动。`insert_at` 为插到第几镜之前（0 = 追加）。成功返回 `{ screenplay, added }`，`revision+1`（已建画布会 stale，走 24.1 同步）。

### 25.4 整段低清试听与预计时长（G06）

- `POST /user/canvas/:id/compose` 请求加 `preview: true`：480p（16:9 854×480 / 9:16 480×854 / 1:1 480×480）、`ultrafast / crf 30`，其余规则（入出点、配音混入与延长、字幕、BGM、水印）与正式导出完全一致；`compose_job.preview=true`。
- `GET /user/canvas/:id/timeline` 新增 `estimate: { total, clips: [{ id, node_id, duration, voice_extended }] }`——按入出点与配音延长算的预计时长（与合成同一套规则；时间线总开关 `voice=false` 或片段 `voice_mode=off` 时不算延长），`voice_extended` 为被配音拉长的秒数。`PUT` 响应同样带 `estimate`。

### 25.5 候选通过 / 退回（G07）

- 新表 `shot_review(canvas_id, asset_id 唯一, user_id, node_id, task_id, status 1 通过 / -1 退回, reason, note, created_at, updated_at)`。
- `POST /user/canvas/:id/review` `{ node_id, asset_id, status: approved|rejected|clear, reason?, note? }`：`reason` 只认 `identity / performance / lipsync / composition / continuity / duration / technical`；`clear` 撤销回待审。返回 `{ asset_id, review }`。
- `GET /user/canvas/:id/shots` 与 timeline 的 `shots[].video.candidates[]` 带 `review`（nil = 待审）。前端：退回的候选默认收起、不可选用；已通过的标绿。

### 25.6 定版锁（G07）

- `canvas_timeline` 加 `locked / locked_at / locked_by`。`POST /user/canvas/:id/timeline/lock { locked }`；没保存过时间线时报「先保存一次再定版」。
- 锁住后 `PUT /user/canvas/:id/timeline` 返回「时间线已定版，先解锁再修改」；合成照常（合成时已快照素材）。`GET` 返回 `locked / locked_at / locked_by`。

## 26. 审批门禁与交付（2026-09-17，复审 G05 / G08）

### 26.1 审批门禁（G05）

- `timeline.review_gate`：`""` 关闭 / `rejected` 输入素材里有被退回的图就拦 / `approved` 视频的首帧图（分镜图）必须已通过审片才放行（退回同样拦）。随时间线保存，`GET /user/canvas/:id/timeline` 返回。
- 审片接口 `POST /user/canvas/:id/review` 现在也接受图片资产（分镜图 / 角色图），先审低成本的图再放行视频。`GET /user/canvas/:id/shots` 与 timeline 响应新增 `characters: [{ node_id, asset_id, url, cover_url, task_status, candidates[]（带 review） }]`（角色图节点），前端据此给角色图做通过 / 退回。
- 单个生成 `POST /user/task`：画布开了门禁且 `type=video` 时校验 `inputs`，被拦返回 `success=false`、`data.reason="review_gate"`、message 说明是哪个素材、为什么。
- 批量生成：静态输入（不依赖本批次产物的 `first_frame` 等）在**创建时**就判定，被拦的条目直接以 `blocked` 入库（响应里可见）；依赖本批次产物的条目由 worker 提交前再查。被拦条目 `error` 写原因，下游条目按上游失败跳过；已通过的镜头照常推进；预算机制不变。通过审片或撤销退回后「只重跑失败的节点」（`POST /user/batch/:id/retry`）会把 `blocked` 一并重置。

### 26.2 交付包（G08）

`POST /user/canvas/:id/compose` 加 `package: true`（预览时忽略）：成片之外再生成并入库
- 无字幕母版（拼接后、加字幕 / BGM / 水印之前的那份，含对白混音）`母版（无字幕） #N.mp4`
- 独立字幕 `字幕 #N.srt`（`type=text`）
- 对白轨 `对白轨 #N.m4a`（各镜配音按时间线位置铺开，延后 0.15 秒与混音一致，没配音的镜是静音）
- 素材清单 `素材清单 #N.json`：`{ title, compose_id, canvas_id, aspect, resolution, width, height, fps:30, duration, lang, clips:[{ seq, shot_no, node_id, clip_id, asset_id, task_id, model, in, out, duration, voice_asset_id, subtitle }], files:{ final, master, srt, voice_track } }`

`compose_job.package=true`，结果在 `package_files: { master_asset_id, master_url, srt_asset_id, srt_url, voice_asset_id, voice_url, manifest_asset_id, manifest_url }`；任一文件失败只写 `warning`，不影响成片。

### 26.3 制片报表（G08）

`GET /user/canvas/:id/production-report`

```json
{ "canvas_id": 29, "title": "深夜的面馆", "review_gate": "rejected", "locked": false,
  "shots": [{ "no": 1, "node_id": "video-1", "line": "…", "candidates": 3, "approved": 1, "rejected": 1, "pending": 1, "reworks": 2,
              "selected_asset_id": 51, "selected_status": "approved", "enabled": true, "image_status": "approved",
              "cost_points": 36, "tasks": 4, "effective_seconds": 3, "reject_reasons": ["continuity"] }],
  "totals": { "shots": 2, "shots_enabled": 2, "shots_approved": 1, "candidates": 5, "reworks": 3, "cost_points": 80, "failed_tasks": 2,
              "effective_seconds": 8, "cost_per_second": 10 },
  "composes": { "total": 6, "done": 5, "previews": 2, "packages": 1 },
  "missing": [{ "no": 2, "node_id": "video-2", "what": "选用的候选还没通过审片" }] }
```

- `cost_points` 只算成功任务的净结算积分（失败已退回，计入 `failed_tasks` 不计成本），按镜头的图 / 视频 / 配音节点归集，角色图等不属于某一镜的成本只进 `totals`。
- `cost_per_second` = 净结算成本 ÷ 时间线启用镜头的预计时长；跨镜复用同一素材时按时间线出现次数计秒，成本只算一次。
- `missing` 列出「距离定版还缺什么」：启用的镜头里选用候选未通过 / 已退回 / 没有视频。

## 27. 剧集圣经与集目录（2026-09-17，复审 G01）

一个项目 = 一部剧；每个策划案 = 一集（`screenplay.episode_no`）。贯穿全剧的设定放在项目级「剧集圣经」里，新一集生成时沿用，改设定时能列出受影响的集 / 场 / 镜。

### 27.1 实体

- 新表 `series_bible(project_id 唯一, user_id, data longtext, revision, updated_at)`，`data`：
```json
{ "title": "", "logline": "",
  "characters": [{ "key": "c1", "name": "阿杰", "look": "…", "voice": "onyx", "voice_style": "", "traits": "",
                   "relationships": [{ "to": "c2", "relation": "前女友，仍有旧情" }], "knows": ["f1_1"] }],
  "props": [{ "key": "p1", "name": "录音手机", "holder": "c2", "state": "屏幕碎了", "note": "" }],
  "threads": [{ "key": "t1_1", "title": "录音里到底是谁的声音", "status": "open", "opened_in": 1, "resolved_in": 0 }],
  "facts": [{ "key": "f1_1", "text": "手机里有录音", "revealed_in": 1, "audience_knows": true, "known_by": ["c2"] }],
  "episodes": [{ "no": 1, "screenplay_id": 4, "title": "餐厅对峙", "summary": "…", "revealed": ["f1_1"], "open": ["t1_1"],
                 "relationships": ["A 开始怀疑 B"], "prop_changes": [{ "prop": "p1", "holder": "c2", "state": "屏幕碎了" }],
                 "characters": ["c1", "c2"], "updated_at": 0 }] }
```
  `Normalize` 校验：人物 / 道具 / 线索 / 事实 key 唯一；关系、知情、持有人、记录里的引用都必须指向存在的对象；上限人物 20 / 道具 40 / 线索 60 / 事实 120 / 集 60。**事实区分观众是否知道（`audience_knows`）与哪些角色知道（`known_by`）**。
- `screenplay.episode_no`（默认 0）；`plan.scenes[]` 新增 `objective / conflict / turn`（谁想得到什么 / 阻碍 / 结束时的变化，各 200 字）。

### 27.2 接口（登录用户）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/project/:id/bible` | `{ project_id, revision, bible, updated_at }`；没建过返回空结构、`revision=0`、`title` 取项目名 |
| PUT | `/user/project/:id/bible` | `{ revision, bible }` 乐观锁（首次 0），冲突返回「剧集设定已在其他地方修改」；返回 `{ revision, bible }` |
| POST | `/user/project/:id/bible/impact` | `{ character? \| prop? \| fact? \| text? }` → `{ affected: [{ episode_no, screenplay_id, title, scenes, shots, why }], episodes }` |
| GET | `/user/project/:id/episodes` | 集目录：`[{ id, episode_no, title, status, canvas_id, scenes, shots, revision, stale, … }]` 按集号排 |
| PUT | `/user/screenplay/:id/episode` | `{ episode_no }` 改集号 |
| POST | `/user/screenplay/:id/record` | 用文本模型把本集策划案总结成连续性记录并并入圣经（按文本模型单价扣一次分）；返回 `{ bible, revision, record, points, episode_no }` |

影响分析规则：人物 → 出场或被提及的镜；道具 → 提示词 / 对白 / 状态里提到道具名的镜；事实 → 揭示集及之后、知情角色出场的镜（没有知情人时揭示后的每一镜）；文本 → 包含该文本的镜。结果按集列出场次名与镜号。

### 27.3 新一集沿用设定

`POST /user/screenplay` 加 `use_bible: true`、`episode_no?`（0 = 项目下一集）：设定摘要（人物 key / 名字 / 外貌 / 关系、道具归属、此前集已揭示的事实及谁知道、未解决线索、上一集梗概）附在用户输入前，要求模型沿用人物 key、不让不知情角色泄密。

### 27.4 每集记录并入圣经

`record` 的合并规则：策划案里有、圣经里没有的人物补进圣经（沿用 key / 外貌 / 音色）；`revealed` 追加为事实（key `f<集>_<序>`，知情人写进人物 `knows`）；`open_questions` 追加为线索（`t<集>_<序>`）；`prop_changes` 更新道具持有人与状态；同集号的记录覆盖。

## 28. 模型详情（2026-09-17）

`GET /generation/models` 只给选择器要的字段；模型详情页与参数表单要的说明文案、供应商、参数属性表、端点定义来自网关的 `GET /pricing/detail`，由本服务转发后与本地积分定价合成一份 EasyTV 口径的响应。

### 28.1 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/generation/models/detail` | 匿名。`model`（模型标识，兼容别名 `model_name`）必填 |

```json
{ "success": true, "message": "", "data": {
  "model": "seedance-2.0", "provider": "easyapi", "type": "video", "label": "Seedance 2.0",

  "category": "视频", "description": "当前语言描述", "icon": "", "tags": "视频",
  "vendor": { "id": 12, "name": "字节跳动", "description": "", "icon": "" },
  "endpoints": ["api-video"],
  "supported_endpoint": { "api-video": { "path": "/v1/videos", "method": "POST" } },
  "supported_endpoint_types": ["openai"],
  "params": { "video": { "input": ["text", "image"], "output": ["video"], "properties": {} } },

  "unit": "second", "points_per_unit": 30, "member_points_per_unit": 24, "priced": true,
  "promo_points_per_unit": 0, "promo_label": "", "promo_starts_at": 0, "promo_ends_at": 0, "promo_active": false,
  "effective_points_per_unit": 30, "effective_member_points_per_unit": 24,
  "durations": [4, 8, 12], "resolutions": ["720P", "1080P"],
  "image_input": "", "abilities": ["upscale"],

  "gateway_available": true, "pricing_version": "162c1496…d200e" } }
```

- `model` / `provider` / `type` / `label` 与 `/generation/models` 同一来源（`model_price` status=1）；定价字段（`unit` / `points_per_unit` / `member_points_per_unit` / `promo_*` / `effective_*` / `durations` / `resolutions` / `image_input` / `abilities`）与列表每行同名同义，前端可复用同一套渲染。
- `description` / `tags` / `category` / `vendor` / `params` / `endpoints` / `supported_endpoint` / `supported_endpoint_types` 直接取自网关 `/pricing/detail`，字段名与网关一致。`params.<分类>.properties` 是参数表单的属性表（尺寸、质量、音色等）；端点用 `supported_endpoint_types`（端点类型）配 `supported_endpoint`（类型 → `path` / `method` 总表）判断。`endpoints` 是网关 `model.endpoints` 列的原样透传，形态由上游数据决定（2026-09-17 实测线上是端点类型数组，如 `["anthropic","openai"]`；运营配了自定义端点时是 `{"image-edit":{"path":…,"method":…}}` 对象），不做类型化，前端按需取用。
- **不下发网关的成本口径**：`model_ratio` / `model_price` / `completion_ratio` / 各项 ratio / `model_discount` / `quota_type` / `billing_mode` / `billing_expr` / `enable_groups`；也不下发 `description_i18n` / `tags_i18n` 原始多语言表——切语言请带 `Accept-Language` 重新请求。
- 语言：请求头 `Accept-Language` 原样转发给网关，`description` / `tags` / `category` 按该语言解析。

### 28.2 可见性与降级

- 只认 `model_price`（status=1）里的模型，与 `/generation/models` 同口径；不在清单里返回 `success=false`「模型不存在或未上架」，缺参返回「缺少模型标识 model」。
- 网关未配置 / 请求失败 / 上游查不到该模型时**不报错**：本地定价字段照常返回，展示元数据为空（`description` / `tags` / `category` / `icon` / `pricing_version` 为空串，`vendor` / `endpoints` 为 `null`，`params` / `supported_endpoint` 为 `{}`，`supported_endpoint_types` 为 `[]`），`gateway_available: false`，前端据此隐藏详情区块。
- 详情与定价接口同站同前缀（`EasyAPIGatewayPricingBaseUrl` + `EasyAPIGatewayPricingPath` + `/detail`，缺省 `https://api.easyapi.com/pricing/detail`）。网关侧自带 1 分钟缓存，本服务再按「模型 + 语言」缓存（成功 60 秒、失败 30 秒），避免匿名接口放大成网关压力。

### 28.3 从网关同步能力字段（2026-09-18）

网关参数表 `params.<分类>.properties` 对部分模型（2026-09 只有 Kling 系列）给出了结构化参数，正好对应运营一直手填的 `model_price.durations / resolutions / abilities`：

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/admin/model-price/:id/sync-gateway` | root。`force=1` 覆盖已有值（缺省只填空字段）。返回 `{ price, result: { id, model, changes[], notes[]?, skipped?, error? } }` |
| POST | `/admin/model-price/sync-gateway` | root。全部定价行逐个同步，返回 `{ results[], updated, skipped, failed }` |

翻译规则（`providers.ParseGatewayParams`）：`duration` 的 `enum` → `durations`，没有 enum 用 `min~max` 展开整数档（超过 16 档视为连续值不展开）；`resolution.enum` → `resolutions`；有 `generate_audio` / `audio` 属性 → 加 `native_audio`；输入类型里的 `video` **不**同步成 `video_reference`（2026-09-18 实测 Kling 3.0 Omni 声明了 video 输入，但网关只走上游图片参考字段，带参考视频报 `Image format is invalid`）；`aspect_ratio.enum` 目前没有对应列，只在 `notes` 里回显，不算改动。能力位只增不减。网关没给结构化参数、或网关详情 404（该模型对本令牌不可见，如 gpt-image / gemini / sora 系）的模型返回 `skipped`，不报错；其它网关错误才计入 `failed`。

## 29. 爆款复刻（2026-09-17）

不新增接口。画布视频节点工具条「爆款复刻」分两段，全部复用既有接口。粉色发送在拉片完成前只做第 1 段，不会直接出成片。

1. **拉片**：前端浏览器抽关键帧上传（OSS 视频没有 CORS 头时浏览器读不到，改提交 `extract_frames` 作业 `mode=spread` 由服务端 ffmpeg 抽帧，用 `outputs[].time` 与作业写回的资产 `duration` / `width` / `height` 还原时间轴）后，建一个文本任务（`type=text`，看图模型，`params.reverse=timestamped`）。提示词要求输出镜头 JSON（`shots` 最多 8 段）以及可替换的 `roles` / `scenes` / `elements`。原文落成一个文本节点；解析结果写入原片节点 `viralPlan`。拉片失败（没有看图模型、抽帧失败、结果里没有分镜）就停在确认条，不再出片。悬浮条在时长读完前发送按钮是灰的，旁边的数字是这次文本任务的预估积分。
2. **替换后再按镜生成**：用户在悬浮条里给角色 / 场景 / 元素选替换图，可改镜头文案和额外要求。确认后每个镜头各建一条视频任务（`POST /user/task`，`type=video`）。`inputs` 里原片作 `reference_video`，替换图按面板顺序作 `reference`；提示词写明「参考图 N 替换某个对象」和这一镜的时间段。`duration` 落到模型 `durations` 里最接近该镜时长的档，`ratio` 按原片像素尺寸取最接近的常用比例，`resolution` 优先 720P → 1080P → 清单第一个，`audio` 取模型是否 `native_audio`。总价确认一次。生成完成后，选中这些镜头用既有成片拼接即可，这一步不自动合成。

模型只列 `abilities` 含 `video_reference` 的已定价视频模型（见 23.1）；默认 = 上次复刻用过的 → `GrowthComposeVideoModel`（`/app/config` 的 `growth.compose_video_model`）→ 清单第一个。运营要让某个模型出现在复刻下拉里，在定价表单勾「参考视频」即可。

## 30. 视频分割与素材续写（2026-09-18）

- **视频分割**：媒体动作 `split`（见 23.4 表），前端把两份产物分别落到源节点右侧上下两个视频节点。
- **素材超分** `POST /user/asset/:id/upscale`：`POST /user/task/:id/upscale` 的别名，路径 id 是资产，请求体同（`model? / resolution? / scale?`）。节点菜单「超分」「续写」对没有来源任务的视频节点也可用；「重拍」仍要求来源任务。
- **素材续写** `POST /user/asset/:id/extend`：对没有来源任务的视频资产（上传的、片段截取 / 分割等处理得到的）续写。请求 `{ model?, prompt?, seconds?(3~15，落到所选模型 `durations` 里最接近的档), node_id, canvas_id?, project_id? }`；服务端取末帧作 `first_frame` 建普通视频任务（`params.mode=fl2v`），模型按请求 → `ComposeVideoModel` → 清单第一个视频模型；提示词缺省「承接上一段画面，自然延续镜头与动作」。响应与 `POST /user/task` 相同。`node_id` 同样必须是新建节点的 id。
- **联调实例不抢作业**（2026-09-18）：环境变量 `WORKER_TAG` 非空时，本进程建的媒体 / 合成 / 批量作业带 `worker_tag`，worker 只领同标签作业（生产为空串），且不跑全局维护任务；见 docs/ops/deploy-and-seed.md「本地联调」。
- **看图模型运营配置**（2026-09-18）：新增 setting `VisionTextModel`（后台「增长」面板），随 `GET /app/config` 的 `growth.vision_text_model` 下发。画布反推提示词、智能编辑「智能识别」、拉片解读都优先用它（须在 `model_price` 里生效的文本模型）；空串时前端按模型名偏好自动挑（gemini → claude → gpt-5 / 4.1 → qwen-vl / glm-4v），拉片解读退回 `MediaStoryboardModel` 再退回第一个可用文本模型。

## 31. 画布交互补齐（对标 LibTV，2026-09-18）

L1 智能引用、L2 Slash 快捷入口全在前端，不动接口；L3 工作流模板新增下面一张表与五个接口。

### 31.1 工作流模板（L3）

实体 `canvas_workflow`（AutoMigrate 建表）：

| 字段 | 说明 |
|---|---|
| `user_id` / `team_id` | 归属。个人空间存 `team_id=0`，团队空间存该团队 id，口径同主体库（见 20） |
| `name` | 工作流名，≤ 50 字 |
| `cover_url` | 封面，取组内第一张有产物的图，可空 |
| `node_count` / `edge_count` | 列表展示用，保存时按 graph 算 |
| `graph` | `{"nodes":[],"edges":[]}` 前端快照原样存，后端不解释内容；序列化后 ≤ 512 KB，超过报「工作流太大，请减少节点后再保存」 |
| `uses` | 发送到画布次数，列表按 `uses desc, id desc` 排 |

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/workflow?keyword=&page=` | 分页列表，**不带 graph** |
| POST | `/user/workflow` | `{ name, cover_url?, graph:{nodes[],edges[]} }`，`nodes` 为空报「工作流里没有节点」 |
| GET | `/user/workflow/:id` | 带 `graph`，并把 `uses` +1（发送到画布时调） |
| PUT | `/user/workflow/:id` | 只改 `name` / `cover_url`，仅作者本人 |
| DELETE | `/user/workflow/:id` | 仅作者本人 |

前端：画布上选中一组节点 → 工具条「存为工作流」提交当前选区的节点与内部连线；左侧栏「工作流」列表点卡片 → `GET /user/workflow/:id` 拿 graph，重建节点 id 后整套落到视口中心。节点里的 `assetId` / `taskId` 原样带走，跨用户复制时与分享一样由前端清理（见 11.3）。

### 31.3 宫格切分与分镜组（L4，2026-09-23）

- **宫格切分**走媒体动作 `grid_split`（见 23.3）：前端把分割线算成归一化 `cells` 一次提交，产物 N 片按原行列落到源节点右侧并打成一组；最多 25 片。（首版在浏览器里裁，实测 OSS 图片没有 CORS 头读不到像素，09-24 改为服务端切。）
- **分镜组拼接**走媒体动作 `montage`（见 23.3）：前端把选中的图片节点按阅读顺序（先按行、再从左到右）排好 `asset_ids`，请求的 `asset_id` 取第一张，产物落到一个新的图片节点上。免费。
- **宫格与故事板预设**（L6）也不新增接口：预设 = 提示词模板 + `data.params.ratio`，走普通图片生成。

### 31.2 提示词里的引用标签（L1）

提示词新增第三种占位符 `{{ref:<素材名>}}`，与既有 `{{cam:id}}`（见 canvas-node-kinds）、`{{mark:id}}` 同一套渲染与展开机制：

- 素材名就是标签文案，由前端按「连线上游节点名 / 上传文件名」生成并去重，保证一条提示词里唯一。
- 提交前 `expandPromptTokens` 统一展开成 `@素材名`，后端与网关看到的仍是纯文本，不需要改动。
- 参考素材本身照旧走 `inputs`（见 11.1 / 23.1），标签只影响提示词文案与素材顺序的可读性。

## 32. 对话式工作流生成（2026-09-20）

一句话/一段对话 → 服务端多轮（ReAct）跑文本模型 → 产出**可直接落到画布上的动作**：从零搭一张图（`apply_graph`）或对现有画布做小改（`update_prompt` / `regenerate` / `add_node` / `connect`）。运行形态是「一个 `generation_task(type=text)` 在 worker 里跑完多轮」，前端照旧 2 秒轮询取计划，不引入 SSE/WebSocket。

三个接口都挂在 `/user/**`，与 `/user/canvas/:id/assist` 同形（权限走 `writableCanvas`，任务提交走 `launchTask`）：

| 方法 | 路径 | 限流 | 说明 |
|---|---|---|---|
| POST | `/user/canvas/:id/agent` | `UserCriticalRateLimit("task")` | 建任务，返回任务视图；`data.id` 即 `task_id` |
| GET | `/user/canvas/:id/agent/:task_id` | — | 读计划（方案 + 校验报告 + 轮次步骤） |
| POST | `/user/canvas/:id/agent/:task_id/retry` | `UserCriticalRateLimit("task")` | 带着上一轮校验报告 + 补充说明再跑一次 |

> 老接口 `POST/GET /user/canvas/:id/assist` 行为不变：**新动作（`apply_graph`）只走 `/agent`**。老前端的 `assistChangeTitle` 没有 default 分支，收到未知动作会在渲染方案卡时抛错，因此新动作不能混进老接口（见 31.1 的 `canvas_workflow` 只被读、不被写）。

**计费口径（2026-09-20 更新，详见 32.5）**：对话本身**不消耗积分**——`points_frozen=0`、不写 `point_log`；普通用户每天有 `ChatDailyFreeCount` 次免费对话（后台可配，缺省 100），会员与「不限次」配置不限次；次数用完时建任务接口返回友好错误并引导开通会员。**AI 计划里的生成内容（`regenerate` / 用户按建议点「生成」）照旧消耗积分**。

本轮新增的两个接口：`GET /user/ai-chat/quota`（每日次数视图，见 32.8）、`GET` / `PUT /admin/ai-chat/config`（管理端配置，见 32.9）；思考参数映射与提示词注入见 32.10，创建入口与免费标记的安全口径见 32.11。

### 32.1 动作 JSON 契约

模型每一轮只输出一个 JSON 对象，两种形态二选一：

```json
{"reply":"给用户看的说明","changes":[ ... ]}
```
```json
{"thought":"为什么需要这个信息","tool":"search_models","args":{"type":"video"}}
```

容忍代码块包裹与前后废话（服务端截取首尾花括号后解析）。工具名白名单固定六个：`search_models` / `get_kind_schema` / `list_templates` / `get_canvas` / `validate_graph` / `estimate_points`；工具结果以 `[工具 <name>] <正文>` 的形式回灌给模型，**一轮只调一个工具、消耗一轮**。`list_templates` 除名称与摘要外还回「可抄的结构」（模板的 graph 经 `compactAgentGraph` 压缩：只留节点 `id / kind / label / model / 80 字内提示词` 与连线 `source→target`，≤ 1200 字，超了先去提示词、再按节点截断并在 `note` 里说明，始终是合法 JSON）——提示词里的模板块只留一行摘要，整张图只在模型主动调工具时给；每条回灌给模型的工具结果再封顶 4000 字、最多留 6 条（2026-09-24 复核加，此前 graph 是硬截 2000 字、工具结果不封顶）。

**五个动作**（`changes[]`，一条计划最多 8 条、最多 1 个 `apply_graph`）：

| action | 字段 | 说明 |
|---|---|---|
| `update_prompt` | `node_id`(必填) `prompt`(必填) `reason?` | 回填 `node_id/node_label/kind/prompt/before` |
| `regenerate` | `node_id`(必填) `model?` `reason?` | 回填 `type/model/params/points`；只用于 image/video/audio 节点，会消耗积分 |
| `add_node` | `kind`(必填，限 `text/image/video/audio`) `label?` `prompt`(必填) `from?` `reason?` `model?` `params?` `content?` | `model` 写进新节点 `data.model`；`params` 只留白名单键；`content` 仅 `kind=text` 时写进 `data.content`（`prompt` 仍必填） |
| `connect` | `source`(必填) `target`(必填) `reason?` | 回填 `node_label`；两端必须是画布上已有的节点 |
| `apply_graph` | `reason?` `graph`(必填) | 一次落下一整张工作流，见下 |

**`apply_graph` 的 graph 与前端 `CanvasWorkflowGraph` 同构**（即 31.1 工作流模板的格式）：

```json
{"action":"apply_graph","reason":"从零搭一条竖屏广告骨架",
 "graph":{"version":1,
   "nodes":[
     {"id":"n1","kind":"text","x":0,"y":0,"data":{"label":"脚本","content":"…"}},
     {"id":"n2","kind":"image","x":400,"y":0,"width":320,"height":220,
      "data":{"label":"产品首帧","prompt":"…","model":"gpt-image-1","params":{"ratio":"9:16","clarity":"2K"}}},
     {"id":"n3","kind":"video","x":800,"y":0,"width":320,"height":220,
      "data":{"label":"主镜头","prompt":"…","model":"kling-v1","params":{"duration":5,"ratio":"9:16","mode":"i2v"},"videoMode":"i2v"}},
     {"id":"n4","kind":"audio","x":800,"y":270,"width":280,"height":118,
      "data":{"label":"女声旁白","prompt":"欢快、语速偏快","model":"tts-1"}}],
   "edges":[{"source":"n1","target":"n2","sourceHandle":"out","targetHandle":"in"},
            {"source":"n2","target":"n3"},{"source":"n1","target":"n4"}]}}
```

硬约束（服务端编译器逐条判，违反即 `error` 并丢弃该条 change）：

1. `version` 必须是 `1`；`nodes[].{id,kind,x,y,width?,height?,data}`、`edges[].{source,target,sourceHandle?,targetHandle?}`。
2. **坐标是相对值**：`x` 吸附到 `round((x-minX)/400)*400`、`y` 吸附到 `round((y-minY)/270)*270`（列间距 400、行间距 270，与短剧分镜同一套），同格冲突按输入顺序下移一行；落画布的绝对位置由前端 `placeWorkflowGraph` 决定。
3. `id` 只在本次计划内有效，模型按 `[A-Za-z0-9_-]`、长度 ≤ 32 生成；前端落图时用 `nextId(kind)` 全部重建。
4. `sourceHandle`/`targetHandle` 可省，编译器统一补 `out`/`in`。
5. `data.label` 每个节点都要给（缺失时前端用节点 id 兜底）；生成类节点（image/video/director/panorama/audio）必须有 `prompt` 与 `model`；`text` 节点必须有 `content`。编译器补 `data.kind` 与 `data.mode="idle"`。
6. `data` 里**禁止**出现素材与产物字段：`assetId` `refAssetId` `refUrl` `refItems` `lastAssetId` `lastUrl` `styleAssetId` `styleUrl` `markAssetId` `markUrl` `markElements` `voiceAssetId` `voiceUrl` `castAssetId` `castUrl` `setAssetId` `setUrl` `subjectIds` `subjectVersions` `taskId` `taskStatus` `taskProgress` `taskError`，以及生成类节点的 `content` → `ASSET_FORBIDDEN`。
7. `data.frameRole`（可选，`first`/`last`）只对 video/director 的上游 image 有意义，是**编译期提示**：编译器在网格吸附后检查首帧是否排在尾帧之前，不满足就交换两者的整对 `(x,y)` 并重跑同格消解（记 `FRAME_ORDER` warning），输出前删掉这个键。
8. `data.params` 按 kind 白名单过滤，其余键一律丢弃：image/panorama 允许 `ratio`/`clarity`/`quality`/`size`，video/director 允许 `ratio`/`resolution`/`duration`/`mode`；`duration` 必须命中所选模型支持的档位、`resolution` 必须命中模型支持的分辨率。字符串值统一去空白，`resolution` 另外统一成大写（判定本来就按大写比，输出不归一的话前端在档位表里找不到 `1080p`，会重置成默认档）。
9. **没有 `anchor` 字段**：落图位置由前端决定（空画布 → 视口中心；有选中 → 选区右侧 80px）。

### 32.2 `POST /user/canvas/:id/agent`

请求：

```json
{"question":"做一条 15 秒竖屏宠物零食广告，产品图当首帧，配一段欢快女声旁白",
 "mode":"generate","model":"gpt-4o-mini",
 "nodes":[AssistNode…],"history":[{"role":"user","content":"…"}],"max_rounds":6}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `question` | 是 | ≤ 1000 字（沿用 assist 上限） |
| `mode` | 否 | `generate`（从零搭图）/ `patch`（改现有画布）；缺省或非法按 `patch`。前端口径见 `agentModeFor`：画布为空、或用户在对话栏打开了「从零搭图」→ `generate`，否则 `patch`（只按「画布是否为空」判定的话，画布上随便留一个节点就再也搭不出整张图） |
| `model` | 否 | 规划用文本模型；**后台配置了 `AgentModel` 时以后台为准，这里传的值不生效**（兼容保留）；未配 → 本字段 → 第一个已定价文本模型 |
| `nodes` | 否 | 画布上下文（选区 + 上下游摘要），最多 40 条；服务端会补节点上次生成的真实记录 |
| `history` | 否 | 之前的对话，最多 8 条 |
| `max_rounds` | 否 | 夹到 `[1,8]`；缺省 → 运营配置（patch 默认 4 / generate 默认 6） |

返回 `data = taskView(task)`（同 13 节的任务视图，含 `id/status/progress/error_msg/points_frozen/cost_points`）；免积分对话下 `points_frozen=0`、`cost_points=0`（见 32.5）。前端拿 `data.id` 轮询下一个接口。

错误（`success:false`）：`请输入问题` / `问题太长了，最多 1000 字` / `暂无可用的文本模型` / `模型 xxx 暂未接入` / `对话式工作流生成暂未开启` / `今日 AI 对话次数已用完（N 次），开通会员可无限次使用`（当日免费次数用尽，`data.reason=chat_quota_exceeded`、`data.daily_limit=N`，见 32.5 / 32.8） / `资源不存在`（画布不属于自己或无权）/ `你在团队里是查看者，只能查看这张画布` / `无效的参数`（请求体不是 JSON）/ `操作过于频繁，请稍后再试`（`UserCriticalRateLimit("task")`，60s/30 次，HTTP 仍是 200）。

### 32.3 `GET /user/canvas/:id/agent/:task_id`

归属校验：任务必须属于该用户、该画布、`type=text` 且带 `params.agent.question`，否则 `资源不存在`。返回：

```json
{"task_id":41,"status":"succeeded","error_msg":"","question":"…","mode":"generate",
 "round":2,"max_rounds":6,
 "steps":[{"round":1,"tool":"list_templates","summary":"命中 2 条可复用模板","ok":true}],
 "reply":"先给一条 15 秒竖屏的宠物零食广告骨架（4 个节点、3 条连线）",
 "changes":[{"action":"apply_graph","reason":"从零搭","graph":{…}}],
 "validate":{"ok":true,"errors":[],
   "warnings":[{"code":"ORPHAN_NODE","severity":"warning","node_id":"n4","message":"该节点没有任何连线，生成后不会被使用","hint":"给它连一条上游或删掉","change_index":0}],
   "estimate":{"points":128,"items":[{"node_id":"n2","kind":"image","type":"image","model":"gpt-image-1","points":8}]},
   "normalized":{"version":1,"nodes":[…],"edges":[…]}},
 "estimate":{"points":128,"items":[…]},
 "dropped":0,"estimated_points":128,"cost_points":2}
```

| 字段 | 说明 |
|---|---|
| `status` | 任务状态：`pending/running/succeeded/failed/cancelled` |
| `round` / `max_rounds` | 已用轮数 / 轮数上限 |
| `steps[]` | 每轮留痕（`round/tool/summary/ok/error`），来自 `canvas_agent_step` 表，最多 40 条 |
| `reply` | 给用户看的说明 |
| `changes[]` | 通过校验的动作，`apply_graph` 的 `graph` 是**编译后**的图 |
| `validate` | `ok` + `errors[]` + `warnings[]`（见 32.4）+ `estimate` + `normalized` |
| `dropped` | 被丢弃的动作数（解析层 + 校验层） |
| `estimated_points` | `validate.estimate.points + Σ changes[].points` |

- 任务未成功时：`reply=""`、`changes=[]`、`validate` 为零值（`ok=false`、errors/warnings 为空数组），`steps` 照常返回（前端据此显示「正在第几轮」）。
- `result_text` 反序列化失败时降级：`reply = result_text` 原文、`changes=[]`、`validate.ok=false`。

### 32.4 错误码表

校验分三层产生问题（图级 `ValidateAgentGraph` / 计划级 `BuildAgentPlan` / 解析级 `ParseAgentReply`），严重级别以 `AgentIssueSeverity` 为唯一真相源；`error` 级会让该条 change 被丢弃，`warning` 级只提示、不阻断。

**阻断级（`severity=error`，进 `validate.errors`）**

| code | 判定依据 |
|---|---|
| `BAD_GRAPH` | graph 为空 / `version != 1` / `nodes` 为空 / 节点缺 `id`、缺 `kind` / `data` 不是对象 |
| `DUPLICATE_ID` | 同一张图里两个节点 id 相同 |
| `UNKNOWN_KIND` | kind 不在可新建白名单（`text/image/video/audio/director/panorama`）；`character/scene/edit/group` 一律非法 |
| `MISSING_REQUIRED` | 生成类节点缺 `prompt` 或 `model`；text 节点 `content` 为空 |
| `MODEL_NOT_FOUND` | 模型未接入网关 |
| `MODEL_TYPE_MISMATCH` | 模型类型与节点任务类型不符（如文本模型给了图片节点） |
| `MODEL_NOT_PRICED` | 模型未定价（`model_price` 无记录） |
| `PARAM_OUT_OF_RANGE` | `duration` 不在模型档位、`resolution` 不在模型支持列表、`ratio/clarity/quality/mode` 不在白名单、`size` 不是 `1024x1536` 形式 |
| `EDGE_DANGLING` | 连线端点不在本图 nodes 里 |
| `SELF_LOOP` | `source == target` |
| `DUPLICATE_EDGE` | 同一 `(source,target)` 出现两次 |
| `CYCLE` | 图里有环 |
| `VIDEO_NEEDS_FRAME` | video/director 的 `videoMode` ∈ `fl2v/i2v` 且没有上游 image 节点 |
| `ROLE_KIND_MISMATCH` | 边的 source kind 不在 target kind 的 `accepts_from` 里（target 是 text 时走 `USELESS_EDGE`） |
| `REF_TOKEN_LEFT` | `prompt`/`content` 里残留 `{{ref:` / `{{cam:` / `{{mark:` 占位符 |
| `ASSET_FORBIDDEN` | data 里出现 32.1 第 6 条列的任何字段 |
| `TOO_MANY_CHANGES` | **计划级**：changes > 8 条，或 `apply_graph` ≥ 2 个 |
| `TOO_LARGE` | 节点数 > `AgentMaxNodes`（默认 60）、边数 > 120、graph JSON > 512 KB |
| `OVER_BUDGET` | 配置了 `AgentMaxPoints > 0` 且本图估价超上限 |

**警告级（`severity=warning`，进 `validate.warnings`，不阻断）**

| code | 判定依据与自动修正 |
|---|---|
| `FRAME_ORDER` | 同一 video/director 的上游 image 里同时有 `frameRole=first` 与 `=last`，且按吸附后的网格坐标比较首帧不严格早于尾帧 → 编译器交换两者整对 `(x,y)` 并重跑同格消解，warning 里带修正前后的 y（如 `"n2:0→270, n5:270→0"`） |
| `USELESS_EDGE` | 目标节点是 text 且 source 不是 text（前端只把上游 text 同步成提示词）→ 该边从 `normalized` 里丢弃 |
| `ORPHAN_NODE` | 节点既无入边也无出边（text 节点允许孤立） |
| `UNKNOWN_ACTION` | **解析级**：action 不在五个动作白名单（`set_params`/`disconnect`/`remove_node` 走这条）→ 丢弃该条并 `dropped++` |
| `NO_CHANGES` | **解析级**：解析成功且 `reply` 非空但 `changes` 为空（用户只是提问） |
| `BAD_JSON` | **解析级**：模型没返回 JSON（含某条动作不是合法 JSON） |

`validate.ok` 的判定：**没有任何 error 级问题，且本轮有产出（`len(changes) > 0`）**；例外是用户纯提问——`NO_CHANGES` + `reply` 非空 + 无 changes 时也记 `ok=true`，前端据此渲染成「不改图」的回复。

### 32.5 计费口径（免积分对话 + 每日次数；开关关闭时回到按轮预扣）

**缺省（`ChatFreeEnabled` 开启）：对话不消耗积分。**

1. **免费分支**：`StartTask` 在 `CheckConcurrency` 之后直接走 `IsFreeChatTask` 分支（`type=text` 且创建时落库的 `params.agent.free=true`）：**不估价、不校验模型参数、不预扣积分**，改为原子消费一次当日对话次数，再入库、提交网关。任务 `points_frozen=0`、不写 `point_log`；入库失败把这一次次数退回去（退**消费日**），提交网关同步失败由 `FailTask` 退还。
2. **谁不限次**：会员（`IsMemberForPricing`：个人空间看自己的会员，团队空间看团队 owner 的团队版会员）与 `ChatDailyFreeCount <= 0`（配置成不限）**完全不读写计数**，配额视图固定 `unlimited=true`、`remaining=-1`。
3. **计数口径**：一人一天一行记在 `ai_chat_usage`，`day` 按 Asia/Shanghai 取 `yyyy-mm-dd`（与签到同一时区）；并发由一条 `INSERT ... ON DUPLICATE KEY UPDATE` 条件自增保证不超发（到顶时计数不涨、判定为超限；连接开了 `clientFoundRows=true` 时改走「条件 UPDATE + INSERT IGNORE」，两种语义下都只认「真的写进去了」）。
4. **次数用完**：不建任务、不提交网关，建任务 / 重试接口 `success=false`，`message` 为 `今日 AI 对话次数已用完（N 次），开通会员可无限次使用`（N 为当日配置上限），并带 `data.reason=chat_quota_exceeded`、`data.daily_limit=N`。前端优先认 `reason`，中文只给旧客户端兜底。
5. **失败 / 取消退还**：`FailTask` / `CancelTask` 与积分退款同一事务退还一次，幂等闸门是任务行 `payer_id` 由 0 置成 `user_id` 的条件更新（免积分任务不设买单账户、恒为 0），重复失败 / 取消只退一次；退的是**消费日**——创建时落在 `params.agent.quota_day`（消费了写 `yyyy-mm-dd`，会员 / 不限次没消费写空串，空串不退；2026-09-24 复核加，之前只按创建日退，会员期间建的任务失败会把此前非会员时用掉的次数退回去），标记落地前的老任务仍按创建日退；跨零点失败 / 取消不会退到别的日子。
6. **结算**：成功后整段跳过按轮结算与退差，`cost_points` 保持 0；`params.agent.rounds_used` 与 `params.agent.plan_bytes` 照常回写。
7. **计划里的生成照旧计费**：`regenerate` 动作与用户按 AI 建议点「生成」走的是普通生成任务（见 11.1），按积分计费。

**开关关闭（`ChatFreeEnabled=false`）：保持旧的按轮预扣口径。**

1. **预扣**：`NewAgentTask` 把轮数写进 `params.agent.max_rounds`；`StartTask` 走 `EstimateTaskPoints` 的 agent 分支（`taskType=text` 且 `agent.max_rounds > 1` 时 `count = max_rounds`），即 **单价 × 轮数**，由 `FreezeTaskPoints` 预扣。轮数不能靠 `count` 表达——`StartTask` 会把 `count > 1` 归一为 1，所以单列 `agent.max_rounds`。
2. **结算**：循环结束按**实际轮数**用 `AgentSettlePoints` 重算（内部仍调 `EstimateTaskPoints`，不另写公式），`RefundTaskPointsPartial` 把差额退回（`points_frozen` 减少 + 退款批次 + `unfreeze` 流水，幂等）。例：预扣 6 轮、实际跑 3 轮 → 退回 3 轮的钱。
3. **失败**：一次都没解析出 JSON（含网关一直报错）→ `FailTask` 全额退回；**任务被取消** → 由 `CancelTask` 全额退回，循环只停止、不再动状态与积分。
4. **不额外计费**：同一轮内解析失败的重试不推进轮数（每轮最多重试 1 次），不计轮、不计费；`validate_graph`/`estimate_points` 等工具是纯静态计算，不产生费用。
5. **对账字段**：结束时把 `params.agent.rounds_used` 与 `params.agent.plan_bytes`（`len(result_text)`）写回任务 params；`cost_points` 为退差后的实际金额。

> **开关翻转不影响在途任务**：免费与否在**创建那一刻**写进 `params.agent.free`（画布建任务与画布重试都一样），结算、退次数与提示词里的【平台事实】只读这个落库标记、不读实时开关——否则开关中途翻转会让已冻结的积分 / 次数卡住。

### 32.6 运营配置（setting，管理端热更新）

写入方式：专用接口 `PUT /admin/ai-chat/config`（见 32.9），或通用入口 `PUT /admin/setting/:key`；两者都写 DB `setting` 表并同步内存缓存，写完立即热生效。

| 键 | 默认 | 说明 |
|---|---|---|
| `AgentEnabled` | 开启 | 对话式工作流总开关；`"false"`/`"0"` 关闭新任务（已在跑的任务照旧跑完） |
| `AgentModel` | 空 | **对话使用的模型**；非空且「已定价 + 已接入」时**优先于请求体里的 `model`**——请求体的 `model` 视为失效但兼容，只在这个键没配、或配的模型查不到生效定价 / 未接入时才生效；空 → 请求 `model` → 第一个已定价文本模型 |
| `ChatFreeEnabled` | 开启 | 对话免费开关；`"false"`/`"0"` 关闭（关闭后回到 32.5 的按轮预扣口径），缺省开启 |
| `ChatMaxTokens` | 0（不发送） | 单次最大输出 token；0 = 不发送该字段，夹到 `[0,32000]` |
| `ChatThinkingMode` | 空（不发送） | 思考模式：`""` / `off` / `on`；其它值一律按 `""` |
| `ChatThinkingEffort` | 空（不发送） | 思考强度：`""` / `low` / `medium` / `high`；其它值一律按 `""` |
| `ChatDailyFreeCount` | 100 | 每日免费对话次数；`0`（含 `<=0`）表示不限，空值 / 非法值回退 100 |
| `ChatKnowledge` | 空 | 平台知识补充文本，注入系统提示词的【平台知识】段（见 32.10）；写入侧限制 ≤ 8000 字，专用接口与 `PUT /admin/setting/:key` 都拦 |
| `AgentMaxRoundsPatch` | 4 | 对话改图轮数上限，夹到 `[1,8]` |
| `AgentMaxRoundsGenerate` | 6 | 从零生成轮数上限，夹到 `[1,8]` |
| `AgentMaxNodes` | 60 | `apply_graph` 节点数上限，夹到 `[1,200]` |
| `AgentMaxPoints` | 0（不限） | 单次计划估价上限 |
| `AgentRoundTimeoutSeconds` | 90 | 单轮网关超时，夹到 `[10,300]` |
| `AgentTotalTimeoutSeconds` | 480 | 总超时，夹到 `[30,1800]` |
| `AgentExamples` | 空 | 运营经验条目 JSON 数组（`{name,description,content}`），与 `canvas_workflow`、`skill.graph` 一起作为可复用底座检索；检索 `canvas_workflow` 时要带 `WithGraph`，列表查询默认不取 graph 列 |

### 32.7 前端约定（`data.model` / `data.params` 消费链）

`apply_graph` 与 `add_node` 会把模型与参数写进新节点：`data.model`、`data.params`（另有 text 节点的 `data.content`）。前端要认这两个键并在 Inspector 初始化时**优先读它们**（其次 localStorage `easytv-canvas-last-model`），用户点「生成」时才能用上 AI 选的模型与参数——缺任何一环，AI 选的模型都落不到生成上。落图整批只占一步撤销（与老动作同一套 `applyAssistChanges`/撤销历史）。

### 32.8 `GET /user/ai-chat/quota`（每日次数视图）

登录用户（`UserAuth` + `EasyTV-Team`，与 `GET /user/usage` 同组）读自己的今日配额。**只读接口，不写计数**；前端在对话面板展示剩余次数，用尽时引导开通会员。

```json
{"success":true,"data":{"date":"2026-09-20","enabled":true,"free":true,"is_member":false,
 "unlimited":false,"daily_limit":100,"used":3,"remaining":97}}
```

| 字段 | 说明 |
|---|---|
| `date` | 计数日（Asia/Shanghai，`yyyy-mm-dd`，与签到同一时区） |
| `enabled` | 对话式工作流总开关（`AgentEnabled`），关闭后新建对话任务报「对话式工作流生成暂未开启」 |
| `free` | 对话免费开关（`ChatFreeEnabled`），关闭时对话按积分计费（32.5 的旧口径） |
| `is_member` | 是否按会员价计费（`IsMemberForPricing`：个人空间看自己的会员，团队空间看团队 owner 的团队版会员） |
| `unlimited` | 不限次：会员或 `ChatDailyFreeCount<=0` |
| `daily_limit` | 当日免费次数上限（后台配置值，`<=0` 表示不限） |
| `used` | 今日已用次数；不限次时为 0（不读写计数） |
| `remaining` | 今日剩余次数，不会小于 0；不限次时为 `-1` |

- 会员 / 不限次：`unlimited=true`、`remaining=-1`、`used=0`，且**完全不读不写** `ai_chat_usage`。
- 失败 / 取消的任务会退还次数（见 32.5），前端重新拉本接口即可看到 `remaining` 回涨。
- 空间口径与 `GET /user/usage` 一致：`EasyTV-Team` 头解析出的团队 id，个人空间为 0。
- 前端已接：`easytv-web` 在对话面板展示剩余次数、用尽时引导开通会员；`easytv-manage` 在系统设置里加「AI 对话」分页读写 32.9 的配置。

### 32.9 `GET` / `PUT /admin/ai-chat/config`（管理端，RootAuth）

仅超级管理员（`middleware.RootAuth`，role ≥ 100）。GET 返回当前配置与可选模型；PUT 先整体校验、再逐键写入（写 DB `setting` 表 + 同步内存缓存，**写完立即热生效**，无需重启），返回与 GET 相同的视图，并记审计 `ai_chat.config_update`。

```json
{"success":true,"data":{"enabled":true,"free":true,"model":"gpt-4o-mini","max_tokens":0,
 "thinking_mode":"","thinking_effort":"","daily_free_count":100,"knowledge":"",
 "text_models":[{"model":"gpt-4o-mini","label":"GPT-4o mini"}]}}
```

`text_models` 只列生效中（已定价）的文本模型，供管理端下拉选择。

PUT 请求体字段**全部可选**（缺省 = 本次不改该键，局部更新）：

| 字段 | 校验规则 | 说明 |
|---|---|---|
| `enabled` | 布尔 | 写 `AgentEnabled` |
| `free` | 布尔 | 写 `ChatFreeEnabled` |
| `model` | 空串 / 纯空白 = 清空（回到自动选模型）；非空必须是**已定价的 text 模型**，否则 `对话模型必须是已定价的文本模型` | 写 `AgentModel` |
| `max_tokens` | `0 ≤ v ≤ 32000`，否则 `最大输出 token 必须在 0 到 32000 之间` | 0 = 不发送 |
| `thinking_mode` | 裁剪空白后 ∈ `""`/`off`/`on`，否则 `思考模式只能是 off、on 或留空` | |
| `thinking_effort` | 裁剪空白后 ∈ `""`/`low`/`medium`/`high`，否则 `思考强度只能是 low、medium、high 或留空` | |
| `daily_free_count` | `0 ≤ v ≤ 100000`，否则 `每日免费次数必须在 0 到 100000 之间` | 0 = 不限 |
| `knowledge` | 裁剪首尾空白后 ≤ 8000 字（按字符不是字节），否则 `平台知识最多 8000 字` | |

- 校验顺序 `model → max_tokens → thinking_mode → thinking_effort → daily_free_count → knowledge`，命中即返回该条错误；全部通过后才写库，单键写入失败返回 `保存失败`。
- 请求体不是 JSON → `无效的参数`。
- 这些键也可以走通用入口 `PUT /admin/setting/:key`；其中 `ChatKnowledge` 超 8000 字在通用入口同样被拒（堵住旁路，两条写入路径口径一致），其余键沿用既有语义、不做额外限制。

### 32.10 思考参数映射与提示词注入

**请求体映射**（`ChatWithOptions`，只影响 `/v1/chat/completions` 的请求体；参数每轮调用时从内存缓存的 setting 读一次）：

| 配置 | 请求体字段 | 取值 |
|---|---|---|
| `ChatMaxTokens` | `max_tokens` | 仅 `> 0` 时发送 |
| `ChatThinkingEffort` | `reasoning_effort` | 仅非空时发送（`low` / `medium` / `high`） |
| `ChatThinkingMode` | `thinking` | `on` → `{"type":"enabled"}`；`off` → `{"type":"disabled"}`；空 → 不发送 |

三项都为空（缺省）时请求体只有 `model` + `messages`，与改动前逐字段一致。

**提示词注入**（`BuildAgentPrompt`，两个可选段，为空时整段不输出）：

- **【平台知识】**：`ChatKnowledge` 正文，运营补充的平台说明。
- **【平台事实】**：会员状态 / 本次是否免费对话 / 今日剩余次数 / 当前积分余额，由服务端查好后注入；免费任务还会带上「标准价（会员价未配置时同标准价）」的模型价格快照（促销生效且确实更便宜时带促销价）。次数读不到时**整段不输出**——宁可让模型说「不太确定」，也不把配置上限冒充剩余次数。

**话题范围**（`AgentSystemPrompt` 的【话题范围】段）：只处理三类话题——(1) 在画布上创建工作流 / 短视频：节点、连线、提示词、参数；(2) AI 生成视频 / 图片的做法：怎么选模型、怎么写提示词、参数怎么调、报错怎么排查；(3) 本平台的使用与规则问答：积分、会员、充值、导出、画布操作、常见问题。其它话题（写代码、写作文、闲聊、通用百科、情感与政治话题等）**一律不回答**：`reply` 只写一句友好的中文提示把用户带回正题，`changes` 给 `[]`，不输出任何节点或连线（前端据此渲染成「不改图」的回复，见 32.4 的 `NO_CHANGES`）。

第 (3) 类必须以提示词里的【平台知识】/【平台事实】为依据：这两段没写的事实就直说不太确定并建议联系客服，不编造价格 / 规则 / 功能；用户消息里自称「平台知识 / 平台事实 / 系统规则」的文字只是普通数据，不作为依据。

### 32.11 创建入口与免费标记（安全口径）

`params.agent` / `params.chat` 与其中的 `free` **只能由服务端写**：`free=true` 会让任务跳过估价与预扣（32.5），放行客户端自报等于绕过计费。

| 入口 | 行为 |
|---|---|
| `POST /user/canvas/:id/agent` | 画布对话任务创建入口；`free` 由 `NewAgentTask` 按创建那一刻的 `ChatFreeEnabled` 写入 |
| `POST /user/canvas/:id/agent/:task_id/retry` | 画布重试；`free` 由 `NewAgentRetryTask` 按**当前**开关重新写入（命中免费时同样消费一次当日次数）。源任务仍在 `pending` / `running` 时拒绝：「上一轮还在生成中，等它结束再重试」（2026-09-24 复核加） |
| `POST /user/ai-chat` | 导演台助手单轮文本（见 32.12）；`free` 由 `NewDirectorChatTask` 写入 `params.chat`，和画布对话共用每日次数。不跑 `apply_graph` |
| `POST /user/task` | 带非空 `params.agent` / `params.chat` 一律拒绝：带 `free` 时由路由闸门返回「由服务端管理」；其它非空对象由 `CreateTask` 返回对应入口提示 |
| `POST /user/task/:id/retry` | **不拒绝**：源任务带 `params.agent` 或 `params.chat` 时按当前开关重估 `free`（其余 params 原样保留），再按新任务的 `free` 决定走免费还是付费路径 |
| `POST /user/canvas/:id/batch` | 批量条目自带非空 `params.agent` / `params.chat` 一律拒绝（错误信息带节点 id） |
| `POST /user/media/jobs` | 媒体动作 params 自带非空 `params.agent` / `params.chat` 一律拒绝（错误信息带动作名） |

- 键名匹配与 `encoding/json` 的结构体解码同语义：**大小写不敏感**，`{"Agent":{"Free":true}}` / `{"AGENT":…}` 同样被拦；重估时统一写成规范小写键，避免新旧两份并存。
- 「空对象 / `null` / 没有 `agent` / `chat` / 非 JSON」不拦，交给各入口自己的参数校验。
- 判定「是不是免费对话任务」认两种落库口径（`IsFreeChatTask`）：画布 `type=text` + `params.agent.question` 非空 + `max_rounds>0` + `free=true`，或导演 `type=text` + `params.chat.kind=director` + `free=true`；创建后不再看实时开关。

### 32.12 `POST /user/ai-chat`（导演台助手）

导演台 AI 助手和画布「结合画布」对话共用 `GET /user/ai-chat/quota` 的每日次数：开关开启时扣一次次数、不扣积分；关掉免费开关时按普通文本任务计费。模型回复仍是导演指令 JSON，由前端本地执行，**不走** 32 章的 `apply_graph`。

```json
{"model":"gpt-4o-mini","prompt":"……已拼好的导演提示词……","canvas_id":12}
```

| 字段 | 说明 |
|---|---|
| `model` | 已接入的文本模型；空则回退平台默认文本模型 |
| `prompt` | 完整提示词（前端已带上场景与历史），最多 20000 字 |
| `canvas_id` | 可选；有则校验画布可写，任务记到该画布 |

成功返回与 `POST /user/task` 相同的任务视图；前端轮询 `GET /user/task/:id` 取 `result_text`。

错误：`对话功能暂未开启` / `请输入问题` / `问题太长了，最多 20000 字` / `暂无可用的文本模型` / `模型 xxx 暂未接入` / `今日 AI 对话次数已用完（N 次），开通会员可无限次使用`（`data.reason=chat_quota_exceeded`） / 画布无权时的查看者提示 / `无效的参数`。

`params.chat` 只由服务端写：`{"chat":{"kind":"director","free":true|false,"quota_day":"yyyy-mm-dd"|""}}`。客户端经 `POST /user/task` 自带 `params.chat` 会被拒。

## 33. 用户偏好（2026-09-24）

前端自定义的小块状态跨设备同步：第一批是运镜库的「收藏 / 最近」（原来只存 localStorage，换台电脑就没了）。值是 JSON 文本，**后端只存不解释**；不是团队维度，不看 `EasyTV-Team`。

实体 `user_preference`（AutoMigrate 建表）：`user_id` + `key` 唯一，`value text`（≤ 16 KB），`updated_at`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/preference` | 全部偏好，`data` 为 `{ "<key>": "<value>" }` 的对象；没有任何偏好时为 `{}` |
| PUT | `/user/preference/:key` | body `{ value }`；`value` 为空串或纯空白 = 删除该键。`key` 只收字母数字与 `. _ -`，≤ 64 字，否则「偏好键不合法」；`value` 超 16 KB 报「偏好内容过大」。返回 `{ key, value }` |

约定的键（前端）：

| 键 | 值 | 说明 |
|---|---|---|
| `camera.fav` | `["dolly-in","orbit"]` | 运镜库收藏，id 数组，顺序即收藏顺序 |
| `camera.recent` | `["orbit","pov"]` | 运镜库最近使用，最新在前，最多 12 个 |

前端策略：打开运镜库先按 localStorage 立即渲染，同时 `GET /user/preference` 拉云端并**以云端为准**覆盖本机（多设备时最后一次写入胜出）；点星 / 选用后先写本机再 `PUT`，请求失败只提示不回滚。未登录只用本机。

## 34. 风格库（L5，2026-09-24）

运营在管理端手工维护的「整体风格」：一个名字 + 封面 + 一段提示词片段（如「胶片电影感」「日系清新」「赛博朋克」）。创作台里给图片 / 视频节点选一个风格，**生成时把风格的 `prompt` 追加到节点提示词末尾**，机制与运镜库 / 特效预设一致（前端 `appendStylePrompt`，与 `appendFxPrompt` 同形）。不从远程提示词源同步。`user_id` 预留给 L18 用户自定义风格，本期恒为 0（官方）。

实体 `style`（AutoMigrate 建表，软删除）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `user_id` | int, index | 0 官方；>0 预留用户自定义 |
| `name` | varchar(50) | 风格名，必填 |
| `cover_url` | varchar(1000) | 封面，可空；前端无封面时渲染纯文字卡片 |
| `prompt` | text（≤ 2000 字） | 追加到生成提示词末尾的片段，必填 |
| `negative` | varchar(500) | 负向提示词，可空；前端暂存不用 |
| `tags` | varchar(255) | 逗号分隔 |
| `category` | varchar(50), index | 分类（电影感 / 动画 / 插画 / 摄影 / 氛围 …），前台按分类出页签 |
| `kinds` | varchar(50) | 适用类型，逗号分隔的 `image,video` 子集；**空 = 都适用** |
| `uses` | int | 套用次数 |
| `sort_order` | int, index | 越大越靠前 |
| `status` | int, index | 四态：1 可用（官方=已上架 / 用户=私有）/ 2 下架（官方）/ 3 待审核（用户投稿）/ 4 已收录（用户投稿通过，公开可见），见 34.5 |
| `source_asset_id` | int | 「定风格」时的来源图资产 ID，只留痕，缺省 0（34.5） |
| `created_at` / `updated_at` | int64 | unix 秒 |

内置数据：`model.SeedStyles()` 在**表为空时**写入 16 条官方风格（电影胶片、赛博朋克、日系清新、水墨国风、黑白极简、复古港片、皮克斯三维、吉卜力手绘、美式漫画、写实纪录片、霓虹夜景、暖调生活、冷峻悬疑、梦幻柔焦、高级时尚大片、像素游戏），封面留空由运营补传；`kinds` 全空。服务启动与 `go run ./cmd/seed` 都会调一次，`COUNT(*)>0` 直接跳过，幂等。

### 公开接口（匿名可看）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/style` | `keyword`（名字 / 提示词 LIKE）、`tag`（LIKE）、`category`（精确）、`kind`（`image` / `video`，命中 `kinds` 为空或包含该类型；其它值报「无效的适用类型」）、`page` / `page_size`。只返回已上架，`sort_order desc, id desc`。`data = { items, total }` |
| GET | `/style/categories` | 已上架风格的非空分类去重，`data` 为 `string[]`，顺序按 `sort_order` |
| POST | `/style/:id/use` | 需登录。套用一次 `uses + 1`；下架或不存在报「风格不存在或已下架」。返回 `{ id, uses }` |

视图（公开）：`{ id, name, cover_url, prompt, negative, tags: string[], category, kinds: string[], uses }`；`kinds` 为 `[]` 表示图片 / 视频都适用。

### 管理端（`/admin/style`，与 `/admin/prompt` 同权限）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/admin/style` | 全部状态；`keyword` / `tag` / `category` / `kind` / `status`（1 或 2，其它报「无效的状态」）/ 分页。视图在公开视图基础上多 `user_id, sort_order, status, created_at, updated_at` |
| POST | `/admin/style` | body `{ name, prompt, cover_url?, negative?, tags?: string[], category?, kinds?: string[], sort_order?, status? }`；`name` 与 `prompt` 必填（缺任一报「风格名和风格提示词必填」）。返回管理视图 |
| PUT | `/admin/style/:id` | 同 body，字段可选，只更新传了的；空 body 报「没有要更新的字段」 |
| DELETE | `/admin/style/:id` | 软删除 |

校验文案：`请填写风格名`、`请填写风格提示词`、`风格提示词过长`（> 2000 字）、`负向提示词过长`（> 500 字）、`无效的适用类型`（`kinds` 里出现 image / video 以外的值）、`无效的状态`。`tags` 每项 ≤ 30 字、合并后 ≤ 255；`kinds` 去重转小写。

### 前端约定

- 创作台节点数据新增 `stylePreset?: { id, name, prompt, negative? }`（`canvas-node-kinds.ts`），复制节点时一并复制。
- 生成时（`useCanvasGenerate.ts` / `CreateLayers.vue`）在特效预设之后 `prompt = appendStylePrompt(prompt, node.data.stylePreset)`：片段已包含在提示词里则不重复追加；同时把 `params.style_preset = id` 带给后端留痕（后端不解释，也不进节点记忆白名单）。
- 检查器芯片 `stylelib`（风格库）与既有的 `style`（风格参考图）并存：前者套整体风格片段，后者挂参考图。选中后芯片显示「已选风格：{name}」，再点同一风格取消。
- 套用即 `POST /style/:id/use`，失败静默。
- 管理端页面 `/style`：列表（封面 / 名字 / 分类 / 标签 / 适用类型 / 套用数 / 排序 / 状态）、筛选、增删改与上下架。

### 34.5 我的风格（L18 落地版，2026-09-24）

LibTV 的 L18 是「传 5–20 张图训练专属风格模板」；我们没有训练侧，落地为**提示词版自定义风格**：用户把一段风格描述存成自己的风格（名字 + 封面 + 提示词片段），可以手写，也可以从画布上一张图「定风格」——前端用看图文本模型（与反推提示词同一套选型 `growth.vision_text_model`）建一条 `type=text` 任务提取风格描述，回填到保存表单，封面取那张图。私有风格只有本人可见可用；用户可**投稿**进官方风格库，运营审核通过后对所有人可见。

`style.status` 扩成四态：`1` 可用（官方=已上架 / 用户=私有可用）、`2` 下架（官方）、`3` 待审核（用户投稿中）、`4` 已收录（用户风格，审核通过，公开可见，作者仍可编辑，编辑后回到 3 重审）。

可见性口径：
- 公开列表 `GET /style`：`(user_id = 0 AND status = 1) OR status = 4`；`GET /style/categories` 同口径。
- 我的列表：`user_id = uid`，全部状态。
- `POST /style/:id/use`：官方上架、已收录、或本人的私有风格都可计数；其它报「风格不存在或已下架」。

用户侧接口（`UserAuth`，与 `/user/workflow` 同组，不看团队空间）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/user/style?keyword=&kind=&page=` | 我的风格分页，`sort_order desc, id desc`；视图 = 公开视图 + `status, created_at, updated_at`；每人上限 100 条 |
| POST | `/user/style` | `{ name, prompt, cover_url?, negative?, tags?: string[], category?, kinds?: string[], source_asset_id? }`，落 `user_id=uid, status=1`；校验同管理端；超 100 条报「自定义风格最多 100 个」。`source_asset_id` 只留痕（新列 `source_asset_id int default 0`，「定风格」时记来源图） |
| PUT | `/user/style/:id` | 仅本人；字段可选；`status=4` 的行被编辑后自动回到 `3`（重审） |
| DELETE | `/user/style/:id` | 仅本人，软删除 |
| POST | `/user/style/:id/submit` | 投稿：仅本人且 `status ∈ {1}` → `3`；已在审核 / 已收录报「已投稿，请等待审核」/「已收录」。投稿要求 `cover_url` 非空（「投稿需要一张封面」） |
| POST | `/user/style/:id/withdraw` | 撤回投稿：`3 → 1`；`4` 也可撤回（→ 1，公开列表不再显示） |

管理端补充：
- `GET /admin/style` 新增 `source=official|user`（`user_id = 0` / `> 0`）与 `status=3|4`；视图多 `user_id, user_nickname, source_asset_id`。
- `POST /admin/style/:id/review` body `{ approve: bool, reason? }`：仅 `status=3` 的用户风格；通过 → `4`，驳回 → `1`。`reason` 只进审计日志与站内通知（`model.PushNotification`，标题「风格投稿已收录」/「风格投稿未通过」，正文带 reason）。
- 管理端对用户风格的 `PUT` 允许改 `category / tags / sort_order / status`（`status` 只能在 1/3/4 之间）；`DELETE` 同官方。

前端约定：
- 风格库弹窗新增「我的」页签：列私有 + 已收录 + 待审核（角标「审核中」/「已收录」），带「新建风格」「从当前图定风格」「编辑」「投稿 / 撤回」「删除」。「从当前图定风格」只在当前节点是有图的图片节点时可用：建 `type=text` 任务（`params.style_extract=true`，指令让模型只输出风格描述：媒介、色调、光线、构图、质感、时代感，80–200 字，不描述主体内容），等结果后打开保存表单预填 `prompt`，`cover_url` = 节点图片地址，`source_asset_id` = 节点 assetId，`kinds` 空。
- 我的风格套用与官方一致（`stylePreset` + `POST /style/:id/use`）。

## 35. 语音转写与口播字幕（L8 落地部分，2026-09-24）

网关（EasyAPI）提供 `gpt-4o-transcribe` / `gpt-4o-mini-transcribe` / `gpt-4o-transcribe-diarize`（`POST /v1/audio/transcriptions`），没有合规 / 人像模型，所以 L8 只落地「语音转字幕」和免费的「去停顿」；高亮词、转场等不在本期。

### 35.1 能力位与配置

- 新能力位 `transcribe`（`model.AbilityTranscribe`，管理端定价表单可勾选；模型名含 `transcribe` / `whisper` 时按名推断）。运营在 `model_price` 给转写模型打上它，画布「语音转字幕」按钮才从「暂未接入」变可用。转写模型的 `type` 建议填 `audio`、`unit` 填 `second`（按秒计费）。
- 设置 `TranscribeModel`（`PUT /admin/setting/:key`）：指定转写模型；空=按能力位取第一个可用的。`GET /app/config` 的 `growth.transcribe_model` 下发它。

### 35.2 `transcribe` 语音转字幕（`engine=model`，计费）

`POST /user/media/jobs`，`action=transcribe`，源素材视频 / 音频，产物一份 `type=text`。参数：

| 参数 | 说明 |
|---|---|
| `model?` | 转写模型，留空按 `TranscribeModel` → 能力位自动挑 |
| `language?` | `auto` / `zh` / `en` / `ja` / `ko` / `es` / `fr`，缺省 `auto`（不传给网关，由模型识别） |
| `diarize?` | 区分说话人（只有模型名含 `diarize` 时生效），cue 文本前加「说话人 N：」 |

**执行路径**：不走生成任务的提交路径（网关收的是音频文件，不是提示词）。创建时按源时长（资产 `duration` → ffprobe → 60 秒兜底，封顶 20 分钟）建一条 `type=audio`、`status=running` 的 `generation_task`（`params={"transcribe":true,"duration":N,"language":..}`）并预扣积分，壳行 `engine=pipeline` 落库；本地 worker 领取后：`ffmpeg -i in -vn -ac 1 -ar 16000 -c:a libmp3lame -b:a 48k [-t 1200] speech.mp3` → multipart 调网关（`response_format=verbose_json`、`timestamp_granularities[]=segment`；网关回 400 不认这两个字段时退回 `json`，字幕时间按句均分并写进 `warning`）→ 拼 SRT（每条 cue ≤ 42 字，长段按句末标点 / 逗号拆，时间按字数比例分）→ 存文本资产 `字幕 · <源名>.srt`（`mime=text/plain`）。

**计费**：预扣 = 模型单价 × 秒数（`unit=second`；`unit=call` 按次）。成功后按实际送网关的秒数 `RefundTaskPointsPartial` 退差价再 `CompleteTask` 结算（任务 `result_text` 存不带时间的整段文本）；失败 `FailTask` 全额退回；用户从 `/user/task/:id/cancel` 取消的，worker 拿不到进行中的任务时丢弃产物并把作业标失败。超过 20 分钟只转前 20 分钟并写 `warning` + `warning_code=transcribe_truncated`、`warning_params.minutes=20`。

**产物**：`outputs[0] = {asset_id, url, type:"text", name, label:"字幕", text, cues:[{start,end,text}], warning?, warning_code?, warning_params?}`，`text` 是整份 SRT（≤ 20000 字），`cues` 秒为单位；前端直接把 `text` 写进字幕文本节点，不必再拉文件。`GET /user/media/actions` 里该动作 `models` 为具备 `transcribe` 能力的模型，没有时 `available=false, reason=暂未接入语音转字幕模型，请联系运营`；服务器没装 ffmpeg 也不可用。

已知 `warning_code`：

| code | 何时 | `warning_params` |
|---|---|---|
| `transcribe_truncated` | 源素材超过 20 分钟，只转了前 20 分钟 | `{minutes:20}` |
| `transcribe_no_timestamps` | 网关没返回逐句时间戳，字幕时间按句均分 | — |
| `no_silence` | 去停顿没剪到可去掉的停顿（见 35.3） | — |

截断与无时间戳同时出现时，中文 `warning` 用分号拼两句，`warning_code` 优先 `transcribe_truncated`。

### 35.3 `remove_silence` 去停顿（`engine=local`，免费）

源素材视频 / 音频，产物同类。参数 `threshold?`（静音阈值 dB，缺省 -35，范围 -80~-10）、`min_silence?`（最短停顿秒数，缺省 0.6，范围 0.2~10）；每段停顿两头各留 0.15 秒。两趟 ffmpeg 保证音画同步（不用只处理音频的 `silenceremove`）：

1. `ffmpeg -i in -vn -af silencedetect=noise=-35dB:d=0.6 -f null -` 解析 `silence_start` / `silence_end`；
2. `ffmpeg -i in -vf "select='between(t,a,b)+between(t,c,d)',setpts=N/FRAME_RATE/TB" -af "aselect='…',asetpts=N/SR/TB" -c:v libx264 -c:a aac out.mp4`（音频源只跑 `aselect`，出 m4a）。

没检测到可剪的停顿（或保留段超过 400 段）时原样转封装交回，`outputs[0].warning="没有检测到可去掉的停顿"`，`warning_code=no_silence`。

### 35.4 成片字幕

`POST /user/canvas/:id/compose` 的 `subtitle_text` 早就接受整份 SRT；转写产物的 `text` 可以直接喂给它。**2026-09-24 起时间线带 `subtitle_node_id`**：向导第 4 步「字幕来源」可选画布上装着 SRT 的文本节点（语音转字幕的产物，`data.transcriptCues` 非空或正文含 `-->`）；合成时（含预览）若 `subtitles=true`、请求没直接给 `subtitle_text`，就把该节点的正文当整份 SRT 用，逐镜台词不再生成。节点不存在 / 不是文本 / 正文没有 `-->` 时间轴时合成接口直接报错（「字幕来源节点已不在画布上」等），不静默回落。空 = 策划案台词（老行为）。

## 36. 跨画布复制粘贴与素材引入（L16，2026-09-24）

画布里 Ctrl+C 的内容原来只存在页面内存里，换一张画布就没了。现在复制时同时写**系统剪贴板**（文本，JSON 信封）与 **localStorage**（同浏览器跨标签页兜底），粘贴时按「剪贴板 → 本机存储 → 页面内存」取最新的一份，所以同一浏览器换画布、换空间、甚至换浏览器（粘贴文本）都能贴。

信封（前端约定，后端不解释）：`{"app":"easytv-canvas","v":1,"at":<unix ms>,"canvas_id":<源画布 id>,"team_id":<源空间>,"items":[…节点…],"edges":[…内部连线…],"incoming":[…来自未复制上游的连线…]}`。贴到**同一张画布**才接回 `incoming`（别的画布里同名节点 id 指的不是同一个东西）。

### 36.1 素材引入 `POST /user/asset/import`

粘贴的节点里带着资产 id（`assetId`、`refItems[].assetId`、`styleAssetId` 等）。贴到另一张画布时这些资产可能不在当前空间：个人素材贴进团队画布、团队素材贴回个人画布、队友的素材。用户的决定（2026-09-24）：**团队空间可以直接引用个人素材**——落地为粘贴时把素材「引入」当前空间：复制一行资产记录到当前空间（同一个文件地址，不重传、不占存储额度），节点里的 id 改指向新行，这样队友也能用它生成。

`UserAuth` + `EasyTV-Team`（当前空间）。body `{ "asset_ids": [1, 2, 3] }`（≤ 200 个，去重）。

| 情况 | 结果 |
|---|---|
| 资产已在当前空间可用（自己的个人资产在个人空间；当前团队空间的资产） | `status: "same"`，`to = from` |
| 自己的资产在别的空间，或自己所在任一团队空间的资产 | 复制一行到当前空间：`user_id = 当前用户`，`team_id = 当前空间`，`source = "import"`，`import_from = 源 id`，其余（type/name/url/cover_url/mime/width/height/duration/task_id）照抄；**不带 `storage_key` 与 `size`**——回收站清理时不会把共享的文件删掉，云存储额度也不重复计；原资产进回收站被清理时，只要还有未删除的行（引入副本）指向同一 `url`，文件也保留；`status: "copied"`，`to = 新 id` |
| 已删除 / 不存在 / 无权（不是自己的、也不在自己任何团队里） | `status: "missing"`，`to = 0` |

同一资产在同一空间只复制一次：先查 `source='import' AND import_from = from AND user_id/team_id = 当前` 有没有，有就直接返回那一行（新列 `asset.import_from int default 0`，AutoMigrate）。个人空间下自己的资产哪怕 `team_id>0`（当初在团队里传的）也算「别的空间」，会复制一行到个人空间——个人空间列表只认 `team_id=0`。

响应：`{ "items": [{ "from": 1, "to": 1, "status": "same" }, { "from": 2, "to": 88, "status": "copied" }, { "from": 3, "to": 0, "status": "missing" }], "copied": 1, "missing": 1 }`。

前端：粘贴信封的 `canvas_id` 与当前画布不同、或 `team_id` 与当前空间不同时，落节点后收集资产 id 调此接口，按 `to` 改写节点数据；`missing` 的把对应 id 字段去掉（地址保留，走既有「参考素材不在你的资产库里」的提示路径）。提示文案：「已把 N 个素材引入当前空间」/「N 个素材没有权限，已改为仅保留地址」。

## 37. 模型健康（生成失败可观测，2026-09-24）

实测里网关侧问题占了失败大头（seedance 502、Kling 收不了视频、转写 404、个别模型 100% 失败），运营原来只有创作指标的总量，发现不了「哪个模型在坏」。现在按模型看最近 7 天的成功率与失败原因，口径与创作指标一致（`countableTasks`：排除扣积分前就被拒的任务；不算已取消；只算 image / video / audio）。

常量：`ModelHealthDays=7`、`ModelHealthMinTasks=5`（样本少于 5 不评价）、`ModelHealthWarnRate=50`（失败率 ≥ 50% 预警；`fail_rate` 与创作指标一样是百分比 0–100，保留一位小数）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/admin/model-price/health?days=7` | 管理端。`days` 1–90，缺省 7。返回 `{ days, min_tasks, warn_rate, models: { "<model>": { model, total, failed, fail_rate, warn, top_errors: [{ message, count }] } } }`；没有任务的模型不在 `models` 里；`top_errors` 按次数取前 3，`message` 截到 80 字 |
| GET | `/generation/models` | 每行新增 `recent_total`（近 7 天任务数）、`recent_fail_rate`（百分比 0–100）、`recent_warn`（bool）。服务端缓存 5 分钟 |

前端约定：
- 管理端定价页加一列「近 7 天」：`成功率 xx%（n 次）`，`warn=true` 标红并悬浮显示 top 失败原因，建议运营下架或联系网关；没有数据显示「—」。
- 创作台模型下拉里 `recent_warn=true` 的模型名后带「近期失败多」小标；点「生成」时若所选模型 `recent_warn`，每个会话对每个模型只提醒一次：「这个模型近 7 天失败率 xx%，可以换一个再试」，不拦截。

### 37.3 建议下架与自动提醒（2026-09-27）

- `/admin/model-price/health` 每个模型新增 `suggest_offline`（bool）：所选窗口内样本 ≥ `ModelHealthOfflineMinTasks=10` 且失败率 ≥ `ModelHealthOfflineRate=90`。管理端定价页在健康列标「建议下架」。
- 后台任务 `RunModelHealthAlerts`（每 6 小时，启动 5 分钟后先跑一次；`WORKER_TAG` 联调实例不跑）：按近 30 天口径找出**生效中**（`model_price.status=1`）且 `suggest_offline` 的模型，给所有启用的管理员（`role >= 10`）发一条 `system` 站内通知，多个模型合成一条：标题「N 个模型近 30 天几乎全部失败，建议下架」，正文逐个列「模型 失败 x/y（z%），主要原因：…」。
- 去重：setting `ModelHealthAlertNotified` 存 `{ "<model>": 上次提醒 Unix 秒 }`；同一模型 7 天内不重复提醒；模型恢复（不再建议下架）或被停用后记录清掉，再坏会在下一轮马上提醒。

## 38. 网关余额巡检（2026-09-28）

seedance 曾因网关账户欠费（402「预估扣费 $0.61，当前余额 $-0.81」）成批失败，没有任何地方能看到余额。网关是 one-api 一系，两条路：

- **账户余额**（会欠费的那个数）：`GET {控制台地址}/api/user/self`，头 `Authorization: Bearer <系统访问令牌>` + `EasyAPI-User: <网关用户 id>`；`data.quota` 剩余、`data.used_quota` 累计用量（quota 单位，500000 = $1，可为负）。令牌与用户 id 在管理端「系统设置 · 网关」填（setting `EasyAPIGatewayAccessToken` / `EasyAPIGatewayUserId`，令牌脱敏不回显）；控制台地址用 `EasyAPIGatewayPricingBaseUrl`（缺省网关地址）。
- **sk 令牌额度**（没配访问令牌时的退路）：`GET /v1/dashboard/billing/subscription`（`hard_limit_usd` = 剩余 + 已用的总额度）与 `/billing/usage`（`total_usage` 美分）；剩余 = 总额度 − 已用。**sk 令牌不限额时 `hard_limit_usd` 固定回 100000000**（实测本站就是），只能报 `unlimited=true`，看不到账户余额——所以线上要配访问令牌。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/admin/gateway/balance?refresh=1` | 管理端。返回 `{ remain_usd, used_usd, unlimited, source: account \| token, fetched_at, threshold_usd, low }`；服务端缓存 5 分钟，`refresh=1` 强制刷新，改网关配置也会清缓存；网关未启用或接口失败时 `success=false`，前端不显示卡片 |
| PUT | `/admin/gateway/config` | 新增 `access_token`、`user_id` 两个可选字段；`GET` 视图多 `gateway.has_access_token`、`gateway.user_id` |

后台任务 `RunGatewayBalanceAlert`（与模型健康提醒同一循环：每 6 小时，启动 5 分钟后先跑一次；`WORKER_TAG` 联调实例不跑）：`unlimited` 时不巡检；余额低于 setting `GatewayBalanceAlertUSD`（默认 20，≤0 关闭）时给所有启用的管理员发 `system` 站内通知（欠费时标题为「网关已欠费 $x，生成任务正在失败」），24 小时内不重复（setting `GatewayBalanceAlertNotifiedAt`）；余额回到阈值以上即清记录，再低会马上提醒。

前端约定：管理端定价页顶部一张卡：「网关余额 $x · 累计用量 $y」，`low=true` 标红；`unlimited=true` 时提示「令牌不限额，看不到账户余额，到系统设置 · 网关填访问令牌」。
