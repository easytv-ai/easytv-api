package constant

// 请求头名称。
const (
	HeaderAuthorization  = "Authorization"
	HeaderEasyTVUser     = "EasyTV-User" // 与前端 auth-user.ts 发送的 EasyAPI-User 头对齐（品牌名 EasyTV）
	HeaderAcceptLanguage = "Accept-Language"
)

// ContextKey gin.Context 中存储的键。
type ContextKey string

const (
	ContextKeyId        ContextKey = "id"
	ContextKeyUsername  ContextKey = "username"
	ContextKeyRole      ContextKey = "role"
	ContextKeyStatus    ContextKey = "status"
	ContextKeyGroup     ContextKey = "group"
	ContextKeyRequestId ContextKey = "request_id"
	ContextKeyLanguage  ContextKey = "language"

	// ContextKeyAuditLogged 标记请求已在 handler 内手动记录审计，让中间件兜底跳过。
	ContextKeyAuditLogged ContextKey = "audit_logged"
)

// 角色常量（与 easyapi 对齐）。
const (
	RoleGuestUser  = 0
	RoleCommonUser = 1
	RoleAdminUser  = 10
	RoleRootUser   = 100
)

// 用户状态。
const (
	UserStatusEnabled  = 1
	UserStatusDisabled = 2
)
