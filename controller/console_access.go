package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

// 后台「进入控制台」（参照 EasyAPI 后台）：管理员给目标用户签一张 60 秒一次性 ticket，
// 创作台打开 /console?ticket=… 时兑换成该用户的登录态（middleware/console-auth.global.ts）。
// ticket 存 Redis；Redis 不可用时退回进程内存（单实例部署够用）。

const (
	consoleAccessTicketPrefix = "ticket:console:"
	consoleAccessTicketTTL    = 60 * time.Second
)

type consoleAccessTicket struct {
	UserId    int   `json:"user_id"`
	AdminId   int   `json:"admin_id"`
	CreatedAt int64 `json:"created_at"`
}

var consoleAccessMemory = struct {
	sync.Mutex
	items map[string]consoleAccessMemoryItem
}{items: map[string]consoleAccessMemoryItem{}}

type consoleAccessMemoryItem struct {
	ticket    consoleAccessTicket
	expiresAt time.Time
}

func saveConsoleAccessTicket(t consoleAccessTicket) (string, error) {
	code := common.GetRandomString(32)
	data, err := common.Marshal(t)
	if err != nil {
		return "", err
	}
	if common.RedisClient != nil {
		if err := common.RedisSetEX(consoleAccessTicketPrefix+code, string(data), consoleAccessTicketTTL); err != nil {
			return "", err
		}
		return code, nil
	}
	consoleAccessMemory.Lock()
	defer consoleAccessMemory.Unlock()
	now := time.Now()
	for k, v := range consoleAccessMemory.items {
		if now.After(v.expiresAt) {
			delete(consoleAccessMemory.items, k)
		}
	}
	consoleAccessMemory.items[code] = consoleAccessMemoryItem{ticket: t, expiresAt: now.Add(consoleAccessTicketTTL)}
	return code, nil
}

// consumeConsoleAccessTicket 取出并作废 ticket（一次性）；不存在或过期返回 nil。
func consumeConsoleAccessTicket(code string) *consoleAccessTicket {
	if code == "" {
		return nil
	}
	if common.RedisClient != nil {
		// 事务里 GET + DEL：线上阿里云 Redis 低于 6.2，不支持 GETDEL
		key := consoleAccessTicketPrefix + code
		var get *redis.StringCmd
		_, err := common.RedisClient.TxPipelined(common.RedisCtx, func(p redis.Pipeliner) error {
			get = p.Get(common.RedisCtx, key)
			p.Del(common.RedisCtx, key)
			return nil
		})
		raw := ""
		if get != nil {
			raw = get.Val()
		}
		if raw == "" {
			if err != nil && err != redis.Nil {
				common.SysError("consume console access ticket: " + err.Error())
			}
			return nil
		}
		var t consoleAccessTicket
		if common.UnmarshalJsonStr(raw, &t) != nil {
			return nil
		}
		return &t
	}
	consoleAccessMemory.Lock()
	defer consoleAccessMemory.Unlock()
	item, ok := consoleAccessMemory.items[code]
	delete(consoleAccessMemory.items, code)
	if !ok || time.Now().After(item.expiresAt) {
		return nil
	}
	return &item.ticket
}

// AdminGenerateConsoleAccess POST /admin/user/:id/console_access
// 返回 { ticket, path, expires_in }，管理端拼上创作台域名后新窗口打开。
func AdminGenerateConsoleAccess(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的用户 ID")
		return
	}
	target, err := model.GetUserById(id)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	// 与启停一致：普通管理员不能进管理员账号的控制台
	if target.Role >= constant.RoleAdminUser && c.GetInt("role") < constant.RoleRootUser {
		common.ApiErrorMsg(c, "无权限进入管理员账号的控制台")
		return
	}
	if target.Status != constant.UserStatusEnabled {
		common.ApiErrorMsg(c, "该用户已被禁用，无法进入控制台")
		return
	}
	adminId := c.GetInt("id")
	code, err := saveConsoleAccessTicket(consoleAccessTicket{UserId: target.Id, AdminId: adminId, CreatedAt: common.GetTimestamp()})
	if err != nil {
		common.ApiErrorMsg(c, "生成访问凭证失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d generated console access ticket for user %d", adminId, target.Id))
	common.ApiSuccess(c, gin.H{
		"ticket":     code,
		"path":       "/console?ticket=" + url.QueryEscape(code),
		"expires_in": int(consoleAccessTicketTTL.Seconds()),
	})
}

// ExchangeConsoleAccess POST /user/console_access/exchange  body: { ticket }
// 兑换成目标用户的 access_token。不更新用户的最近登录时间（不是本人登录）；
// 响应带 console_access=true，创作台据此在退出时只清本地会话、不作废用户本人的令牌。
func ExchangeConsoleAccess(c *gin.Context) {
	var req struct {
		Ticket string `json:"ticket"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || req.Ticket == "" {
		common.ApiErrorMsg(c, "无效的访问凭证")
		return
	}
	t := consumeConsoleAccessTicket(req.Ticket)
	if t == nil {
		common.ApiErrorMsg(c, "访问凭证无效或已过期")
		return
	}
	user, err := model.GetUserById(t.UserId)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	if user.Status != constant.UserStatusEnabled {
		common.ApiErrorMsg(c, "该用户已被禁用")
		return
	}
	accessToken, err := model.EnsureUserAccessToken(user.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.SysLog(fmt.Sprintf("console access ticket consumed for user %d (issued by admin %d)", user.Id, t.AdminId))
	recordConsoleAccessExchange(c, t.AdminId, user.Id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":             user.Id,
			"username":       user.Username,
			"nickname":       user.Nickname,
			"role":           user.Role,
			"status":         user.Status,
			"group":          user.Group,
			"access_token":   accessToken,
			"console_access": true,
		},
	})
}

// recordConsoleAccessExchange 控制台代登兑换成功的安全事件：操作者记为签发 ticket 的管理员，参数带目标用户。
// 管理员已被删除时仍按 admin_id 记录，角色置 0。
func recordConsoleAccessExchange(c *gin.Context, adminId, targetUserId int) {
	ev := model.AuditEvent{
		UserId:   adminId,
		Category: model.AuditCategorySecurity,
		Action:   model.AuditActionConsoleAccessExchange,
		Content:  fmt.Sprintf("管理员 #%d 进入用户 #%d 的控制台", adminId, targetUserId),
		Params:   map[string]any{"admin_id": adminId, "target_user_id": targetUserId},
	}
	if admin, err := model.GetUserById(adminId); err == nil && admin != nil {
		ev.Username, ev.ActorRole = admin.Username, admin.Role
	}
	model.RecordAuditEvent(c, ev)
}
