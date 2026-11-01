package setting

import (
	"strconv"
	"strings"
)

// 增长机制配置（DB setting 表，PascalCase 键，管理端「增长」面板可热更新）。
// 迭代一（T7 / T10 / T12 / T15 / T18）共用这一组键名，数值一律不写死在业务代码里。
// 积分发放统一走 service.GiftPoints，reason 使用下方 GrowthReason* 常量，便于流水筛选与后台冻结。
const (
	// ---- 新人礼包与新手任务（T7）----
	GrowthSignupPoints    = "GrowthSignupPoints"    // 注册赠送积分，默认 200；0 关闭
	GrowthSignupValidDays = "GrowthSignupValidDays" // 注册赠送有效天数，默认 30
	GrowthTaskFirstCanvas = "GrowthTaskFirstCanvas" // 首个画布，默认 50
	GrowthTaskFirstVideo  = "GrowthTaskFirstVideo"  // 首条视频（视频任务首次成功），默认 50
	GrowthTaskFirstWork   = "GrowthTaskFirstWork"   // 首次发布作品，默认 100
	GrowthTaskFirstInvite = "GrowthTaskFirstInvite" // 邀请首位好友生效，默认 100
	GrowthTaskBindWeChat  = "GrowthTaskBindWeChat"  // 绑定微信（已下线展示，保留键兼容旧 setting）
	GrowthTaskValidDays   = "GrowthTaskValidDays"   // 任务奖励有效天数，默认 30

	// ---- 签到（T10）----
	GrowthCheckinPoints           = "GrowthCheckinPoints"           // 每日签到积分，默认 10
	GrowthCheckinValidDays        = "GrowthCheckinValidDays"        // 签到积分有效天数，默认 7
	GrowthCheckinStreakBonus      = "GrowthCheckinStreakBonus"      // 连续 7/14/21/28 天额外积分，默认 50
	GrowthCheckinMonthBonus       = "GrowthCheckinMonthBonus"       // 当月满勤额外积分，默认 200
	GrowthCheckinMemberMultiplier = "GrowthCheckinMemberMultiplier" // 会员倍数，默认 2

	// ---- 双向邀请（T12）----
	GrowthInviteInviteePoints = "GrowthInviteInviteePoints" // 被邀请人完成首条视频后得，默认 100
	GrowthInviteInviterPoints = "GrowthInviteInviterPoints" // 邀请人同时得，默认 100
	GrowthInviteValidDays     = "GrowthInviteValidDays"     // 邀请积分有效天数，默认 30
	GrowthInviteRechargeRate  = "GrowthInviteRechargeRate"  // 好友首充返邀请人比例，默认 0.10
	GrowthInviteRechargeCap   = "GrowthInviteRechargeCap"   // 每个邀请人累计返利积分上限，默认 2000
	GrowthInviteDailyIpLimit  = "GrowthInviteDailyIpLimit"  // 同 IP 每日可绑定邀请的新注册数，默认 3
	GrowthSignupDailyIpLimit  = "GrowthSignupDailyIpLimit"  // 同 IP 每日新注册上限（0 不限），默认 10
	GrowthInviteBaseUrl       = "GrowthInviteBaseUrl"       // 邀请链接站点前缀，留空则回退 ServerAddress

	// ---- 口令红包（T15）----
	GrowthBonusDefaultRechargeRate = "GrowthBonusDefaultRechargeRate" // 首充加赠默认比例，默认 0.20
	GrowthBonusDefaultCapPoints    = "GrowthBonusDefaultCapPoints"    // 首充加赠默认封顶积分，默认 2000
	GrowthBonusDefaultValidDays    = "GrowthBonusDefaultValidDays"    // 首充权益默认有效天数，默认 30

	// ---- 促销条与新模型首发价（T18）----
	GrowthPromoBannerEnabled = "GrowthPromoBannerEnabled" // 全站顶部倒计时条开关，"true"/"1" 开（与 model.SettingKeyPromoBannerEnabled 同键，/admin/billing/promo 的 banner_enabled 也写这里）
	GrowthPromoBannerLink    = "GrowthPromoBannerLink"    // 倒计时条按钮链接，默认 /pricing（同上，banner_link）
	GrowthNewModelPromoRate  = "GrowthNewModelPromoRate"  // 网关导入新模型时自动首发价比例（0 关闭），默认 0.5
	GrowthNewModelPromoDays  = "GrowthNewModelPromoDays"  // 首发价持续天数，默认 7
	GrowthNewModelPromoLabel = "GrowthNewModelPromoLabel" // 首发标签文案，默认「首发 5 折」

	// ---- 积分换算 ----
	GrowthPointsPerYuan = "GrowthPointsPerYuan" // 人民币与积分汇率，默认 20（返利/加赠/技能收益提现，与 EasyAIPricingPointsRate 一致）

	// ---- 迭代二：技能分成 / 分享回流 / 站外投稿 / 成片（T26 / T30 / T31 / T24）----
	GrowthSkillAuthorShare   = "GrowthSkillAuthorShare"   // 技能被调用时作者分成比例（按扣分数），默认 0.20
	GrowthShareOpenPoints    = "GrowthShareOpenPoints"    // 作品被分享打开一次给作者的积分，默认 5；0 关闭
	GrowthShareDailyCap      = "GrowthShareDailyCap"      // 每个作者每日分享回流积分上限，默认 50
	GrowthShareValidDays     = "GrowthShareValidDays"     // 分享回流积分有效天数，默认 30
	GrowthWorkClonePoints    = "GrowthWorkClonePoints"    // 开源作品被做同款一次给作者的积分，默认 10；0 关闭
	GrowthWorkCloneDailyCap  = "GrowthWorkCloneDailyCap"  // 每个作者每日做同款奖励上限，默认 100
	GrowthWorkCloneValidDays = "GrowthWorkCloneValidDays" // 做同款奖励积分有效天数，默认 30
	GrowthUgcPoints30        = "GrowthUgcPoints30"        // 站外投稿 30 秒内档返积分，默认 100
	GrowthUgcPoints60        = "GrowthUgcPoints60"        // 60 秒档，默认 300
	GrowthUgcPoints120       = "GrowthUgcPoints120"       // 120 秒及以上档，默认 500
	GrowthUgcViralLikes      = "GrowthUgcViralLikes"      // 点赞达到此数翻倍，默认 1000
	GrowthUgcMonthlyCap      = "GrowthUgcMonthlyCap"      // 每人每月投稿返积分上限，默认 3000
	GrowthUgcMonthlyEntries  = "GrowthUgcMonthlyEntries"  // 每人每月可提交条数，默认 20
	GrowthUgcValidDays       = "GrowthUgcValidDays"       // 投稿积分有效天数，默认 30
	ComposeWatermarkEnabled  = "ComposeWatermarkEnabled"  // 免费用户成片加水印，默认开（空值视为开）
	ComposePlanPrompt        = "ComposePlanPrompt"        // 策划案 system prompt，空用内置默认
	ComposePlanModel         = "ComposePlanModel"         // 策划案用的文本模型，空取 model_price 里第一个生效的文本模型
	// 成片向导默认选中的模型（空取 model_price 里第一个生效的同类型模型）。
	// 加这三个键是因为默认值原来只能靠定价行的 id 顺序，
	// 而某个模型的某条通路在网关坏掉时（例如 seedance 图生视频回 500），
	// 运营需要能立刻把默认换成可用的那个，不等发版。
	ComposeImageModel = "ComposeImageModel"
	ComposeVideoModel = "ComposeVideoModel"
	ComposeAudioModel = "ComposeAudioModel"
	// VisionTextModel 看图文本模型：画布反推提示词 / 智能识别 / 拉片解读都用它（API.md 30）。
	// 空 = 前端按模型名偏好自动挑（gemini → claude → gpt-5 / 4.1 → qwen-vl），后端拉片解读取第一个可用文本模型。
	VisionTextModel = "VisionTextModel"

	// ---- 迭代三：渠道伙伴 / 召回 / 会员日（M12 / M13 / M7）----
	GrowthPartnerRebateRate      = "GrowthPartnerRebateRate"      // 伙伴充值返佣比例（人民币），默认 0.15
	GrowthPartnerRebateMonths    = "GrowthPartnerRebateMonths"    // 返佣有效月数（自归属起），默认 12
	GrowthLifecycleEnabled       = "GrowthLifecycleEnabled"       // 生命周期触达总开关（回归礼 / 续费提醒 / 积分过期提醒），默认关，运营在后台打开
	GrowthRecallInactiveDays     = "GrowthRecallInactiveDays"     // 多少天未登录发回归礼，默认 30；0 关闭
	GrowthRecallPoints           = "GrowthRecallPoints"           // 回归礼积分，默认 100
	GrowthRecallValidDays        = "GrowthRecallValidDays"        // 回归礼积分有效天数，默认 30
	GrowthRecallMemberDays       = "GrowthRecallMemberDays"       // 回归礼赠送会员天数，默认 3
	GrowthRenewRemindDays        = "GrowthRenewRemindDays"        // 会员到期前几天提醒，默认 7；0 关闭
	GrowthPointsExpireRemindDays = "GrowthPointsExpireRemindDays" // 积分过期前几天提醒，默认 3；0 关闭
	GrowthMemberDayOfMonth       = "GrowthMemberDayOfMonth"       // 每月会员日（几号），默认 11；0 关闭
	GrowthMemberDayPackDiscount  = "GrowthMemberDayPackDiscount"  // 会员日积分包折扣，默认 0.8
)

// GiftPoints 的 reason 固定键（写入 point_log，前端与后台按此筛选）。
const (
	GrowthReasonSignupGift    = "signup_gift"
	GrowthReasonTaskPrefix    = "task_" // 拼任务键：task_first_canvas / task_first_video / task_first_work / task_first_invite
	GrowthReasonCheckin       = "checkin"
	GrowthReasonCheckinStreak = "checkin_streak"
	GrowthReasonCheckinMonth  = "checkin_month"
	GrowthReasonInviteInvitee = "invite_invitee"
	GrowthReasonInviteInviter = "invite_inviter"
	GrowthReasonInviteRebate  = "invite_rebate"
	GrowthReasonBonusCode     = "bonus_code"
	GrowthReasonBonusRecharge = "bonus_recharge"
	GrowthReasonShareOpen     = "share_open"
	GrowthReasonWorkClone     = "work_clone"
	GrowthReasonUgc           = "ugc_reward"
	GrowthReasonCampaign      = "campaign_prize"
	GrowthReasonSkillExchange = "skill_exchange"
	GrowthReasonRecall        = "recall_gift"
)

// 新手任务键（user_task.task_key）。
const (
	GrowthTaskKeyFirstCanvas = "first_canvas"
	GrowthTaskKeyFirstVideo  = "first_video"
	GrowthTaskKeyFirstWork   = "first_work"
	GrowthTaskKeyFirstInvite = "first_invite"
	GrowthTaskKeyBindWeChat  = "bind_wechat" // 历史完成记录仍可能存在，不再列入展示
)

// GrowthTaskKeys 任务展示顺序（4 条；绑定微信已下线）。
var GrowthTaskKeys = []string{
	GrowthTaskKeyFirstCanvas,
	GrowthTaskKeyFirstVideo,
	GrowthTaskKeyFirstWork,
	GrowthTaskKeyFirstInvite,
}

// GrowthConfig 一次性读出的增长参数快照（/app/config 只下发展示需要的字段）。
type GrowthConfig struct {
	SignupPoints    int            `json:"signup_points"`
	SignupValidDays int            `json:"signup_valid_days"`
	TaskPoints      map[string]int `json:"task_points"`
	TaskValidDays   int            `json:"task_valid_days"`

	CheckinPoints           int `json:"checkin_points"`
	CheckinValidDays        int `json:"checkin_valid_days"`
	CheckinStreakBonus      int `json:"checkin_streak_bonus"`
	CheckinMonthBonus       int `json:"checkin_month_bonus"`
	CheckinMemberMultiplier int `json:"checkin_member_multiplier"`

	InviteInviteePoints int     `json:"invite_invitee_points"`
	InviteInviterPoints int     `json:"invite_inviter_points"`
	InviteValidDays     int     `json:"invite_valid_days"`
	InviteRechargeRate  float64 `json:"invite_recharge_rate"`
	InviteRechargeCap   int     `json:"invite_recharge_cap"`
	InviteDailyIpLimit  int     `json:"-"`
	InviteBaseUrl       string  `json:"-"`
	SignupDailyIpLimit  int     `json:"-"`

	BonusDefaultRechargeRate float64 `json:"-"`
	BonusDefaultCapPoints    int     `json:"-"`
	BonusDefaultValidDays    int     `json:"-"`

	PromoBannerEnabled bool    `json:"promo_banner_enabled"`
	PromoBannerLink    string  `json:"promo_banner_link"`
	NewModelPromoRate  float64 `json:"-"`
	NewModelPromoDays  int     `json:"-"`
	NewModelPromoLabel string  `json:"-"`

	PointsPerYuan float64 `json:"-"`

	// 迭代二
	SkillAuthorShare   float64 `json:"skill_author_share"`
	ShareOpenPoints    int     `json:"share_open_points"`
	ShareDailyCap      int     `json:"share_daily_cap"`
	ShareValidDays     int     `json:"-"`
	WorkClonePoints    int     `json:"work_clone_points"`
	WorkCloneDailyCap  int     `json:"-"`
	WorkCloneValidDays int     `json:"-"`
	UgcPoints30        int     `json:"ugc_points_30"`
	UgcPoints60        int     `json:"ugc_points_60"`
	UgcPoints120       int     `json:"ugc_points_120"`
	UgcViralLikes      int     `json:"ugc_viral_likes"`
	UgcMonthlyCap      int     `json:"ugc_monthly_cap"`
	UgcMonthlyEntries  int     `json:"ugc_monthly_entries"`
	UgcValidDays       int     `json:"-"`
	ComposeWatermark   bool    `json:"compose_watermark"`
	// 成片向导默认模型；空串表示没配置，前端回落到清单里第一个
	ComposeImageModel string `json:"compose_image_model"`
	ComposeVideoModel string `json:"compose_video_model"`
	ComposeAudioModel string `json:"compose_audio_model"`
	// 看图文本模型；空串表示没配置，前端按偏好自动挑
	VisionTextModel string `json:"vision_text_model"`
	// 语音转字幕模型（API.md 35）；空串表示按 transcribe 能力位自动挑
	TranscribeModel string `json:"transcribe_model"`

	// 迭代三
	PartnerRebateRate      float64 `json:"partner_rebate_rate"`
	PartnerRebateMonths    int     `json:"-"`
	LifecycleEnabled       bool    `json:"-"`
	RecallInactiveDays     int     `json:"-"`
	RecallPoints           int     `json:"-"`
	RecallValidDays        int     `json:"-"`
	RecallMemberDays       int     `json:"-"`
	RenewRemindDays        int     `json:"-"`
	PointsExpireRemindDays int     `json:"-"`
	MemberDayOfMonth       int     `json:"member_day_of_month"`
	MemberDayPackDiscount  float64 `json:"member_day_pack_discount"`
}

// GetGrowthConfig 读取增长参数（setting 优先，非法回退默认）。
func GetGrowthConfig() GrowthConfig {
	return GrowthConfig{
		SignupPoints:    getIntSetting(GrowthSignupPoints, 200),
		SignupValidDays: getIntSetting(GrowthSignupValidDays, 30),
		TaskPoints: map[string]int{
			GrowthTaskKeyFirstCanvas: getIntSetting(GrowthTaskFirstCanvas, 50),
			GrowthTaskKeyFirstVideo:  getIntSetting(GrowthTaskFirstVideo, 50),
			GrowthTaskKeyFirstWork:   getIntSetting(GrowthTaskFirstWork, 100),
			GrowthTaskKeyFirstInvite: getIntSetting(GrowthTaskFirstInvite, 100),
		},
		TaskValidDays: getIntSetting(GrowthTaskValidDays, 30),

		CheckinPoints:           getIntSetting(GrowthCheckinPoints, 10),
		CheckinValidDays:        getIntSetting(GrowthCheckinValidDays, 7),
		CheckinStreakBonus:      getIntSetting(GrowthCheckinStreakBonus, 50),
		CheckinMonthBonus:       getIntSetting(GrowthCheckinMonthBonus, 200),
		CheckinMemberMultiplier: getIntSetting(GrowthCheckinMemberMultiplier, 2),

		InviteInviteePoints: getIntSetting(GrowthInviteInviteePoints, 100),
		InviteInviterPoints: getIntSetting(GrowthInviteInviterPoints, 100),
		InviteValidDays:     getIntSetting(GrowthInviteValidDays, 30),
		InviteRechargeRate:  getRateSetting(GrowthInviteRechargeRate, 0.10),
		InviteRechargeCap:   getIntSetting(GrowthInviteRechargeCap, 2000),
		InviteDailyIpLimit:  getIntSetting(GrowthInviteDailyIpLimit, 3),
		InviteBaseUrl:       getStringSettingDefault(GrowthInviteBaseUrl, strings.TrimRight(GetSettingString(KeyServerAddress), "/")),
		SignupDailyIpLimit:  getIntSetting(GrowthSignupDailyIpLimit, 10),

		BonusDefaultRechargeRate: getRateSetting(GrowthBonusDefaultRechargeRate, 0.20),
		BonusDefaultCapPoints:    getIntSetting(GrowthBonusDefaultCapPoints, 2000),
		BonusDefaultValidDays:    getIntSetting(GrowthBonusDefaultValidDays, 30),

		PromoBannerEnabled: GetSettingBool(GrowthPromoBannerEnabled),
		PromoBannerLink:    getStringSettingDefault(GrowthPromoBannerLink, "/pricing"),
		NewModelPromoRate:  getRateSetting(GrowthNewModelPromoRate, 0.5),
		NewModelPromoDays:  getIntSetting(GrowthNewModelPromoDays, 7),
		NewModelPromoLabel: getStringSettingDefault(GrowthNewModelPromoLabel, "首发 5 折"),

		PointsPerYuan: getFloatSetting(GrowthPointsPerYuan, 20),

		SkillAuthorShare:   getRateSetting(GrowthSkillAuthorShare, 0.20),
		ShareOpenPoints:    getIntSetting(GrowthShareOpenPoints, 5),
		ShareDailyCap:      getIntSetting(GrowthShareDailyCap, 50),
		ShareValidDays:     getIntSetting(GrowthShareValidDays, 30),
		WorkClonePoints:    getIntSetting(GrowthWorkClonePoints, 10),
		WorkCloneDailyCap:  getIntSetting(GrowthWorkCloneDailyCap, 100),
		WorkCloneValidDays: getIntSetting(GrowthWorkCloneValidDays, 30),
		UgcPoints30:        getIntSetting(GrowthUgcPoints30, 100),
		UgcPoints60:        getIntSetting(GrowthUgcPoints60, 300),
		UgcPoints120:       getIntSetting(GrowthUgcPoints120, 500),
		UgcViralLikes:      getIntSetting(GrowthUgcViralLikes, 1000),
		UgcMonthlyCap:      getIntSetting(GrowthUgcMonthlyCap, 3000),
		UgcMonthlyEntries:  getIntSetting(GrowthUgcMonthlyEntries, 20),
		UgcValidDays:       getIntSetting(GrowthUgcValidDays, 30),
		ComposeWatermark:   GetSettingString(ComposeWatermarkEnabled) == "" || GetSettingBool(ComposeWatermarkEnabled),
		ComposeImageModel:  GetSettingString(ComposeImageModel),
		ComposeVideoModel:  GetSettingString(ComposeVideoModel),
		ComposeAudioModel:  GetSettingString(ComposeAudioModel),
		VisionTextModel:    strings.TrimSpace(GetSettingString(VisionTextModel)),
		TranscribeModel:    GetTranscribeModel(),

		PartnerRebateRate:      getRateSetting(GrowthPartnerRebateRate, 0.15),
		PartnerRebateMonths:    getIntSetting(GrowthPartnerRebateMonths, 12),
		LifecycleEnabled:       GetSettingBool(GrowthLifecycleEnabled),
		RecallInactiveDays:     getIntSetting(GrowthRecallInactiveDays, 30),
		RecallPoints:           getIntSetting(GrowthRecallPoints, 100),
		RecallValidDays:        getIntSetting(GrowthRecallValidDays, 30),
		RecallMemberDays:       getIntSetting(GrowthRecallMemberDays, 3),
		RenewRemindDays:        getIntSetting(GrowthRenewRemindDays, 7),
		PointsExpireRemindDays: getIntSetting(GrowthPointsExpireRemindDays, 3),
		MemberDayOfMonth:       getIntSetting(GrowthMemberDayOfMonth, 11),
		MemberDayPackDiscount:  getRateSetting(GrowthMemberDayPackDiscount, 0.8),
	}
}

// MemberDayActive 今天是否会员日（按 Asia/Shanghai 的号数）。
func (c GrowthConfig) MemberDayActive(day int) bool {
	return c.MemberDayOfMonth > 0 && c.MemberDayPackDiscount > 0 && c.MemberDayPackDiscount < 1 && day == c.MemberDayOfMonth
}

// UgcPointsForTier 站外投稿按时长档位取积分（30 / 60 / 120 秒）。
func (c GrowthConfig) UgcPointsForTier(tier int) int {
	switch {
	case tier >= 120:
		return c.UgcPoints120
	case tier >= 60:
		return c.UgcPoints60
	default:
		return c.UgcPoints30
	}
}

// YuanToPoints 人民币金额换算成积分（向下取整，负数按 0）。
func (c GrowthConfig) YuanToPoints(yuan float64) int {
	if yuan <= 0 || c.PointsPerYuan <= 0 {
		return 0
	}
	return int(yuan * c.PointsPerYuan)
}

// getIntSetting 与 getFloatSetting 不同：允许显式配置 0（用于关闭某项奖励），仅空值或非法值回退默认。
func getIntSetting(key string, def int) int {
	v := GetSettingString(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < 0 {
		return def
	}
	return int(f)
}

// getRateSetting 比例类配置，允许显式 0 关闭，超过 1 或非法回退默认。
func getRateSetting(key string, def float64) float64 {
	v := GetSettingString(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < 0 || f > 1 {
		return def
	}
	return f
}

func getStringSettingDefault(key, def string) string {
	if v := GetSettingString(key); v != "" {
		return v
	}
	return def
}
