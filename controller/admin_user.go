package controller

import (
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// ===== 用户管理（管理侧） =====

// AdminGetUsers 用户列表（GET /admin/user?keyword=&status=&deleted=&page=&page_size=）。
// deleted=1 查看已注销（软删）账号，此时用 Unscoped() 查 deleted_at 有值的记录；缺省/其他值仍只查活跃用户。
func AdminGetUsers(c *gin.Context) {
	page := parsePageInfo(c)
	keyword := c.Query("keyword")
	status := c.Query("status")
	deletedOnly := c.Query("deleted") == "1"

	var users []*model.User
	var total int64
	query := model.DB.Model(&model.User{})
	if deletedOnly {
		query = query.Unscoped().Where("deleted_at IS NOT NULL")
	}
	if keyword != "" {
		query = query.Where("username LIKE ? OR nickname LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	// 增长域筛选（T14）：邀请人 / 来源 / 口令
	if v := queryInt(c, "inviter_id", 0); v > 0 {
		query = query.Where("inviter_id = ?", v)
	}
	if v := strings.TrimSpace(c.Query("source")); v != "" {
		query = query.Where("source = ?", v)
	}
	if v := strings.TrimSpace(c.Query("bonus_code")); v != "" {
		query = query.Where("bonus_code = ?", strings.ToUpper(v))
	}
	if err := query.Count(&total).Error; err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	if err := query.Order("id desc").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Find(&users).Error; err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}

	// 邀请人昵称（T14）
	inviterIds := make([]int, 0, len(users))
	for _, u := range users {
		if u.InviterId > 0 {
			inviterIds = append(inviterIds, u.InviterId)
		}
	}
	inviterNames := model.GetUserNicknames(inviterIds)

	// 脱敏返回（与详情共用字段，另附列表邀请人昵称 / 软删时间）
	items := make([]gin.H, 0, len(users))
	for _, u := range users {
		deletedAt := int64(0)
		if u.DeletedAt.Valid {
			deletedAt = u.DeletedAt.Time.Unix()
		}
		item := adminUserView(u)
		item["inviter_nickname"] = inviterNames[u.InviterId]
		item["deleted_at"] = deletedAt
		items = append(items, item)
	}
	common.ApiSuccess(c, gin.H{
		"items": items,
		"total": total,
	})
}

// AdminUpdateUserStatus 启用/禁用用户（PUT /admin/user/:id/status）。
func AdminUpdateUserStatus(c *gin.Context) {
	var req struct {
		Status int `json:"status"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Status != constant.UserStatusEnabled && req.Status != constant.UserStatusDisabled {
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	var id int
	fmt.Sscanf(c.Param("id"), "%d", &id)
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的用户 ID")
		return
	}
	target, err := model.GetUserById(id)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	// 普通管理员不可修改管理员账号
	if target.Role >= constant.RoleAdminUser && c.GetInt("role") < constant.RoleRootUser {
		common.ApiErrorMsg(c, "无权限修改管理员账号")
		return
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", id).Update("status", req.Status).Error; err != nil {
		common.ApiErrorMsg(c, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// adminLoadEditableUser 加载目标用户；普通管理员不能改管理员账号。
func adminLoadEditableUser(c *gin.Context) (*model.User, bool) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的用户 ID")
		return nil, false
	}
	target, err := model.GetUserById(id)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return nil, false
	}
	if target.Role >= constant.RoleAdminUser && c.GetInt("role") < constant.RoleRootUser {
		common.ApiErrorMsg(c, "无权限修改管理员账号")
		return nil, false
	}
	return target, true
}

// AdminUpdateUserIdentity 设置运营身份（PUT /admin/user/:id/identity）。
// identity 为空 / __none__ 表示去掉身份；只允许白名单值。
func AdminUpdateUserIdentity(c *gin.Context) {
	var req struct {
		Identity string `json:"identity"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	identity, ok := model.NormalizeUserIdentity(req.Identity)
	if !ok {
		common.ApiErrorMsg(c, "无效的身份")
		return
	}
	target, ok := adminLoadEditableUser(c)
	if !ok {
		return
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", target.Id).Update("identity", identity).Error; err != nil {
		common.ApiErrorMsg(c, "更新失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d set user %d identity=%q", c.GetInt("id"), target.Id, identity))
	common.ApiSuccess(c, gin.H{
		"identity": identity,
		"tags":     model.UserIdentityTags(identity),
	})
}

// AdminRebindUser 超级管理员换绑（PUT /admin/user/:id/bind）。
// 可改手机号、解绑微信；至少保留一种登录方式。不走短信验证。
func AdminRebindUser(c *gin.Context) {
	var req struct {
		Mobile       *string `json:"mobile"`
		WeChatUnbind bool    `json:"wechat_unbind"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Mobile == nil && !req.WeChatUnbind {
		common.ApiErrorMsg(c, "请指定要换绑的字段")
		return
	}
	target, ok := adminLoadEditableUser(c)
	if !ok {
		return
	}

	fields := map[string]any{}
	nextMobile := target.Mobile
	nextUsername := target.Username
	nextWeChat := target.WeChatId

	if req.Mobile != nil {
		mobile := strings.TrimSpace(*req.Mobile)
		if mobile == "" {
			if model.IsMobileFormat(target.Username) {
				common.ApiErrorMsg(c, "短信注册账号不能清空手机号")
				return
			}
			nextMobile = ""
			fields["mobile"] = ""
		} else {
			if !model.IsMobileFormat(mobile) {
				common.ApiErrorMsg(c, "手机号格式不正确")
				return
			}
			if mobile != target.Mobile && mobile != target.Username && model.IsMobileTakenByOther(mobile, target.Id) {
				common.ApiErrorMsg(c, "该手机号已被其他账号绑定")
				return
			}
			nextMobile = mobile
			fields["mobile"] = mobile
			if model.IsMobileFormat(target.Username) || strings.HasPrefix(target.Username, "wx_") {
				nextUsername = mobile
				fields["username"] = mobile
			}
		}
	}

	if req.WeChatUnbind {
		nextWeChat = ""
		fields["wechat_id"] = ""
		fields["wechat_nickname"] = ""
	}

	hasLogin := model.IsEmailFormat(nextUsername) || model.IsMobileFormat(nextUsername) || model.IsMobileFormat(nextMobile) || nextWeChat != ""
	if !hasLogin {
		common.ApiErrorMsg(c, "至少保留一种登录方式")
		return
	}
	if len(fields) == 0 {
		common.ApiSuccess(c, gin.H{
			"mobile":    target.Mobile,
			"username":  target.Username,
			"wechat_id": target.WeChatId,
		})
		return
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", target.Id).Updates(fields).Error; err != nil {
		common.ApiErrorMsg(c, "换绑失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d rebound user %d mobile=%q wechat_unbind=%v", c.GetInt("id"), target.Id, nextMobile, req.WeChatUnbind))
	common.ApiSuccess(c, gin.H{
		"mobile":    nextMobile,
		"username":  nextUsername,
		"wechat_id": nextWeChat,
	})
}

// AdminAdjustUserQuota 后台调整用户额度（POST /admin/user/:id/quota，delta 可正可负）。
func AdminAdjustUserQuota(c *gin.Context) {
	var id int
	fmt.Sscanf(c.Param("id"), "%d", &id)
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的用户 ID")
		return
	}
	var req struct {
		Delta  int    `json:"delta"`
		Remark string `json:"remark"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Delta == 0 {
		common.ApiErrorMsg(c, "调整额度不能为 0")
		return
	}
	adminId := c.GetInt("id")
	if req.Delta > 0 {
		if err := model.IncreaseUserQuota(id, req.Delta); err != nil {
			common.ApiErrorMsg(c, "调整失败")
			return
		}
	} else {
		if err := model.DecreaseUserQuota(id, -req.Delta); err != nil {
			common.ApiErrorMsg(c, "调整失败")
			return
		}
	}
	remark := fmt.Sprintf("后台调整（管理员 %d）", adminId)
	if req.Remark != "" {
		remark = req.Remark
	}
	balance, _ := model.GetUserQuota(id)
	bill := &model.PromoterBill{
		UserId:    id,
		BizType:   model.PromoterBillTypeConvertQuota, // 转额度出账（后台调整）
		Amount:    decimal.NewFromInt(int64(req.Delta)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).InexactFloat64(),
		Balance:   decimal.NewFromInt(int64(balance)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).InexactFloat64(),
		SourceNo:  "",
		CreatedAt: common.GetTimestamp(),
		Remark:    remark,
	}
	_ = bill.Insert()
	common.SysLog(fmt.Sprintf("admin %d adjusted user %d quota by %d", adminId, id, req.Delta))
	common.ApiSuccess(c, nil)
}

// AdminGetRecharges 管理端充值记录（GET /admin/recharge?status=&page=&page_size=）。
func AdminGetRecharges(c *gin.Context) {
	page := parsePageInfo(c)
	status := c.Query("status")
	list, total, err := model.GetAllRecharges(status, page)
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"items": list,
		"total": total,
	})
}
