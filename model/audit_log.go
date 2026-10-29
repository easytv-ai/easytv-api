package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"easytv-api/common"

	"github.com/gin-gonic/gin"
)

// 审计日志分类。
const (
	AuditCategoryLogin     = "login"     // 登录、注册
	AuditCategoryUsage     = "usage"     // 用户功能使用：签到、充值、模型调用、开票
	AuditCategoryOperation = "operation" // 管理端写操作（middleware.AdminAudit 兜底）
	AuditCategorySecurity  = "security"  // 安全事件：管理接口鉴权拒绝、登录失败、控制台代登兑换
)

// 审计动作（新增埋点优先复用这里的常量，管理端按 action 筛选）。
const (
	AuditActionAdminAccessDenied     = "admin.access_denied"
	AuditActionLoginFailed           = "user.login_failed"
	AuditActionSmsLoginFailed        = "user.sms_login_failed"
	AuditActionConsoleAccessExchange = "user.console_access_exchange"
	AuditActionInvoiceApply          = "invoice.apply"
)

// AuditLog 使用日志，独立于用量日志及其清理/TTL 策略保留。
// 记录用户关键操作（登录、注册、签到、充值、模型调用）以及管理操作。
type AuditLog struct {
	Id        int        `json:"id"`
	EventId   string     `json:"event_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId    int        `json:"user_id" gorm:"index:idx_audit_user_time,priority:1"`
	Username  string     `json:"username" gorm:"type:varchar(64);index"`
	ActorRole int        `json:"actor_role"` // 事件发生时操作者的角色快照；匿名事件为 0
	CreatedAt int64      `json:"created_at" gorm:"type:bigint;index:idx_audit_user_time,priority:2;index:idx_audit_category_time,priority:2;index"`
	Category  string     `json:"category" gorm:"type:varchar(24);index;index:idx_audit_category_time,priority:1"`
	Action    string     `json:"action" gorm:"type:varchar(128);index"`
	Ip        string     `json:"ip" gorm:"type:varchar(64)"`
	UserAgent string     `json:"user_agent" gorm:"type:varchar(512)"`
	Method    string     `json:"method" gorm:"type:varchar(16)"`
	Route     string     `json:"route" gorm:"type:varchar(255)"`
	Status    int        `json:"status"`
	Success   bool       `json:"success"`
	RequestId string     `json:"request_id" gorm:"type:varchar(64);index"`
	Content   string     `json:"content" gorm:"type:text"`
	Other     AuditOther `json:"other" gorm:"type:text"`
}

func (AuditLog) TableName() string {
	return "audit_log"
}

// AuditOther 审计事件的结构化元数据，存储为 JSON。
type AuditOther struct {
	Op      *AuditOperation `json:"op,omitempty"`
	Device  string          `json:"device,omitempty"`
	Payload AuditFields     `json:"payload,omitempty"`
}

// AuditOperation 操作标识与参数。
type AuditOperation struct {
	Action string      `json:"action"`
	Params AuditFields `json:"params,omitempty"`
}

// AuditFields 保存动作参数。读取时保留原始编码值，避免嵌套整数经 float64 往返丢精度。
type AuditFields map[string]any

func (fields *AuditFields) UnmarshalJSON(data []byte) error {
	var values map[string]json.RawMessage
	if err := common.Unmarshal(data, &values); err != nil {
		return err
	}
	if values == nil {
		*fields = nil
		return nil
	}
	decoded := make(AuditFields, len(values))
	for key, value := range values {
		decoded[key] = value
	}
	*fields = decoded
	return nil
}

func (other AuditOther) Value() (driver.Value, error) {
	data, err := common.Marshal(other)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (other *AuditOther) Scan(value any) error {
	var data []byte
	switch v := value.(type) {
	case nil:
		*other = AuditOther{}
		return nil
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return fmt.Errorf("unsupported audit metadata type %T", value)
	}
	var decoded AuditOther
	if len(data) > 0 {
		if err := common.Unmarshal(data, &decoded); err != nil {
			return err
		}
	}
	*other = decoded
	return nil
}

// AuditLogFilter 审计日志查询过滤条件。
type AuditLogFilter struct {
	UserId         int
	Username       string // 精确匹配
	Operator       string // 操作者用户名前缀匹配
	Category       string
	Action         string
	Ip             string
	Keyword        string // 模糊匹配 route / content / action
	StartTimestamp int64
	EndTimestamp   int64
	Success        *bool
}

// RecordAuditLog 记录一条审计日志。
// 只采集安全的请求元数据；原始 URL、query、凭证与请求体一律不得入表。
// 请求元数据在调用时同步取出（gin.Context 请求结束后会被复用），落库由后台协程异步完成，
// 队列满时丢弃并记 SysError，不阻塞业务请求；需要立即读到结果的场景（测试）调 FlushAuditLogs。
func RecordAuditLog(c *gin.Context, entry AuditLog) {
	if c != nil && c.Request != nil {
		entry.RequestId = c.GetString(common.RequestIdKey)
		entry.Ip = c.ClientIP()
		entry.UserAgent = c.Request.UserAgent()
		entry.Method = c.Request.Method
		entry.Route = c.FullPath()
		if entry.Status == 0 {
			entry.Status = c.Writer.Status()
		}
	}
	if entry.CreatedAt == 0 {
		entry.CreatedAt = common.GetTimestamp()
	}
	if entry.EventId == "" {
		entry.EventId = common.NewRequestId()
	}
	if entry.RequestId == "" {
		entry.RequestId = common.NewRequestId()
	}
	// 校验角色；匿名事件（未登录的安全事件）角色为 0，不算异常
	switch entry.ActorRole {
	case common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser:
	default:
		if entry.UserId != 0 {
			common.SysError(fmt.Sprintf("audit actor role unavailable (request_id=%s, actor_role=%d)", entry.RequestId, entry.ActorRole))
		}
		entry.ActorRole = 0
	}
	if ua := []rune(entry.UserAgent); len(ua) > 512 {
		entry.UserAgent = string(ua[:512])
	}
	if u := []rune(entry.Username); len(u) > 64 {
		entry.Username = string(u[:64])
	}
	enqueueAuditLog(&entry)
}

// AuditEvent 业务埋点入参：默认成功，Failed=true 记为失败。
type AuditEvent struct {
	UserId    int
	Username  string
	ActorRole int
	Category  string
	Action    string
	Content   string
	Failed    bool
	Params    map[string]any
}

// RecordAuditEvent 记录一条业务审计事件，自动补设备信息。c 可为 nil（查单补单等无请求上下文的路径）。
func RecordAuditEvent(c *gin.Context, ev AuditEvent) {
	other := AuditOther{
		Op: &AuditOperation{Action: ev.Action, Params: ev.Params},
	}
	// 查单补单等路径没有请求上下文（c 为 nil），gin.Context.GetString 会空指针
	other.Device = readDevice(c)
	// 有请求上下文时状态码取响应实际状态；无上下文按成败给 200 / 500
	status := 0
	if c == nil {
		status = 200
		if ev.Failed {
			status = 500
		}
	}
	RecordAuditLog(c, AuditLog{
		UserId: ev.UserId, Username: ev.Username, ActorRole: ev.ActorRole,
		Category: ev.Category, Action: ev.Action, Content: ev.Content,
		Status: status, Success: !ev.Failed, Other: other,
	})
}

// RecordOperationAudit 记录一条成功的操作审计（适用于用户功能使用和管理操作）。
// actorRole 为操作者角色，content 为人类可读描述，params 为结构化参数（不得放明文邮箱 / 手机号 / 凭证）。
func RecordOperationAudit(c *gin.Context, userId int, username string, actorRole int, category string, action string, content string, params map[string]interface{}) {
	RecordAuditEvent(c, AuditEvent{
		UserId: userId, Username: username, ActorRole: actorRole,
		Category: category, Action: action, Content: content, Params: params,
	})
}

// RecordSimpleAudit 极简记录（从上下文自动获取用户信息，未登录跳过）。
func RecordSimpleAudit(c *gin.Context, category string, action string, content string, params map[string]interface{}) {
	if c == nil {
		return
	}
	userId := c.GetInt("id")
	if userId == 0 {
		return
	}
	RecordOperationAudit(c, userId, c.GetString("username"), c.GetInt("role"), category, action, content, params)
}

// GetAuditLogs 查询审计日志，按查看者角色做裁剪：
// 非 root 看不到操作者 role=root 的事件。
func GetAuditLogs(filter AuditLogFilter, start, limit, viewerRole int) ([]*AuditLog, int64, error) {
	query := DB.Model(&AuditLog{})
	if viewerRole < common.RoleRootUser {
		query = query.Where("actor_role <> ?", common.RoleRootUser)
	}
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Username != "" {
		query = query.Where("username = ?", filter.Username)
	}
	if filter.Operator != "" {
		query = query.Where("username LIKE ?", escapeLike(filter.Operator)+"%")
	}
	if filter.Category != "" {
		query = query.Where("category = ?", filter.Category)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.Ip != "" {
		query = query.Where("ip = ?", filter.Ip)
	}
	if filter.Keyword != "" {
		kw := "%" + escapeLike(filter.Keyword) + "%"
		query = query.Where("(route LIKE ? OR content LIKE ? OR action LIKE ?)", kw, kw, kw)
	}
	if filter.StartTimestamp > 0 {
		query = query.Where("created_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp > 0 {
		query = query.Where("created_at <= ?", filter.EndTimestamp)
	}
	if filter.Success != nil {
		query = query.Where("success = ?", *filter.Success)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	logs := make([]*AuditLog, 0)
	if err := query.Order("created_at DESC").Order("id DESC").Offset(start).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// escapeLike 转义 LIKE 通配符，关键词里的 % _ 按字面匹配。
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ValidAuditCategory 检查分类是否合法。
func ValidAuditCategory(category string) bool {
	return category == "" ||
		category == AuditCategoryLogin ||
		category == AuditCategoryUsage ||
		category == AuditCategoryOperation ||
		category == AuditCategorySecurity
}

// AuditLogPurgeBatchSize 保留期清理每批删除条数，避免大事务长时间锁表。
const AuditLogPurgeBatchSize = 5000

// PurgeAuditLogsBefore 分批删除 created_at 早于 cutoff 的审计日志，返回删除总数。
// 先按条件取一批 id 再按 id 删除（MySQL / 其他库通用），批间短暂停顿让出写入。
func PurgeAuditLogsBefore(cutoff int64, batchSize int) (int64, error) {
	if cutoff <= 0 {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = AuditLogPurgeBatchSize
	}
	var total int64
	for {
		ids := make([]int, 0, batchSize)
		if err := DB.Model(&AuditLog{}).Where("created_at < ?", cutoff).Limit(batchSize).Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		res := DB.Where("id IN ?", ids).Delete(&AuditLog{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if len(ids) < batchSize {
			return total, nil
		}
		time.Sleep(auditPurgePause)
	}
}

// auditPurgePause 批间停顿（测试置 0）。
var auditPurgePause = 200 * time.Millisecond

// readDevice 从请求上下文取设备标识；无上下文返回空。
func readDevice(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetString("device")
}
