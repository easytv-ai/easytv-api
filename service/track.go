package service

import (
	"fmt"

	"easytv-api/common"
	"easytv-api/model"
)

// Track 服务端事件写入（T1）：业务流程里直接调用，失败只记日志不影响主流程。
// 来源四列从用户表回填，这样漏斗按渠道分组时不需要再 join。
func Track(userId int, name string, props map[string]any) {
	trackWithClient(userId, name, props, "", "")
}

// trackWithClient 带客户端 IP 与设备指纹的写入（注册事件用，供邀请防刷按 ip / device 查）。
func trackWithClient(userId int, name string, props map[string]any, ip, device string) {
	if userId <= 0 || !model.TrackEventNameValid(name) {
		return
	}
	if len(device) > 64 {
		device = device[:64]
	}
	ev := &model.TrackEvent{
		UserId:    userId,
		Name:      name,
		Props:     model.PropsJSON(props),
		Ip:        ip,
		Device:    device,
		CreatedAt: common.GetTimestamp(),
	}
	var u struct {
		Source    string
		Medium    string
		Campaign  string
		BonusCode string
		InviterId int
	}
	if err := model.DB.Model(&model.User{}).Select("source, medium, campaign, bonus_code, inviter_id").Where("id = ?", userId).Scan(&u).Error; err == nil {
		ev.Source, ev.Medium, ev.Campaign, ev.BonusCode = u.Source, u.Medium, u.Campaign, u.BonusCode
		if u.InviterId > 0 {
			var code string
			model.DB.Model(&model.User{}).Select("aff_code").Where("id = ?", u.InviterId).Scan(&code)
			ev.AffCode = code
		}
	}
	if err := model.InsertTrackEvents([]*model.TrackEvent{ev}); err != nil {
		common.SysError("track event insert failed: " + err.Error())
	}
}

// TrackOnce 只在用户从未产生过该事件时写入，返回是否写入了（first_* 系列事件用）。
func TrackOnce(userId int, name string, props map[string]any) bool {
	if userId <= 0 || model.HasUserEvent(userId, name) {
		return false
	}
	Track(userId, name, props)
	onFirstEvent(userId, name)
	return true
}

// AfterRegister 注册成功后的增长钩子（所有注册入口都应调用：邮箱 / 微信 / 短信）。
// 关联匿名事件、绑定邀请人（T12）、写 register 事件、发新人礼包（T7）、自动领取口令（T15）。
func AfterRegister(user *model.User, attr model.Attribution, ip, device string) {
	if user == nil || user.Id <= 0 {
		return
	}
	model.AttachAnonEventsToUser(attr.AnonId, user.Id)
	inviterId := bindInviterOnRegister(user, attr, ip, device)
	trackWithClient(user.Id, model.TrackEventRegister, map[string]any{
		"ref":        attr.AffCode,
		"inviter_id": inviterId,
	}, ip, device)
	GrantSignupGift(user.Id)
	BindPartnerOnRegister(user.Id, attr.PartnerCode)
	if attr.BonusCode != "" {
		if _, err := ClaimBonusCode(user.Id, attr.BonusCode); err != nil {
			common.SysLog(fmt.Sprintf("bonus code auto claim skipped user=%d code=%s: %v", user.Id, attr.BonusCode, err))
		}
	}
}
