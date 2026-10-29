package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 创作者分成（T26）：技能被调用并结算后，按比例把积分数记给作者；作者可把收益兑成积分或提现人民币。

var creatorSkillBalanceTypes = []int{
	model.PromoterBillTypeSkillRevenue,
	model.PromoterBillTypeSkillExchange,
	model.PromoterBillTypeSkillCashOut,
	model.PromoterBillTypeSkillCashRefund,
}

// RecordSkillRevenue 任务结算后调用（CompleteTask）。作者自用、未定价、非上架技能不计。幂等：同一任务只记一次。
func RecordSkillRevenue(t *model.GenerationTask) {
	if t == nil || t.SkillId <= 0 || t.CostPoints <= 0 {
		return
	}
	s, err := model.GetSkillById(t.SkillId)
	if err != nil || s.UserId <= 0 || s.ReviewStatus != model.SkillReviewPublished {
		return
	}
	payer := t.PayerId
	if payer == 0 {
		payer = t.UserId
	}
	if s.UserId == t.UserId || s.UserId == payer {
		return
	}
	rate := s.RevenueShare
	if rate <= 0 {
		rate = setting.GetGrowthConfig().SkillAuthorShare
	}
	points := int(float64(t.CostPoints) * rate)
	if points <= 0 {
		return
	}
	var dup int64
	model.DB.Model(&model.PromoterBill{}).Where("biz_type = ? AND ref_type = ? AND ref_id = ?", model.PromoterBillTypeSkillRevenue, model.PromoterBillRefTypeTask, t.Id).Count(&dup)
	if dup > 0 {
		return
	}
	bill := &model.PromoterBill{
		UserId: s.UserId, BizType: model.PromoterBillTypeSkillRevenue, Amount: float64(points),
		FromUserId: t.UserId, SourceNo: t.TaskNo, SourceAmount: float64(t.CostPoints), Rate: rate,
		RefType: model.PromoterBillRefTypeTask, RefId: t.Id, Status: model.PromoterBillStatusValid,
		CreatedAt: common.GetTimestamp(), Remark: fmt.Sprintf("技能「%s」被调用分成", truncate(s.Title, 40)),
	}
	if err := model.DB.Create(bill).Error; err != nil {
		common.SysError(fmt.Sprintf("skill revenue bill failed task=%d: %v", t.Id, err))
		return
	}
	model.IncreaseSkillRevenue(s.Id, points)
	Track(s.UserId, "skill_revenue", map[string]any{"skill_id": s.Id, "task_id": t.Id, "points": points})
}

// CreatorWithdrawable 有效分成合计 − 已兑换 − 提现冻结 + 提现退回。
func CreatorWithdrawable(userId int) int {
	return creatorWithdrawable(model.DB, userId)
}

func creatorWithdrawable(db *gorm.DB, userId int) int {
	var v struct{ Sum float64 }
	db.Model(&model.PromoterBill{}).Select("COALESCE(SUM(amount),0) AS sum").
		Where("user_id = ? AND biz_type IN ? AND status = ?", userId, creatorSkillBalanceTypes, model.PromoterBillStatusValid).
		Scan(&v)
	if v.Sum < 0 {
		return 0
	}
	return int(v.Sum)
}

// CreatorPointsPerYuan 技能收益兑人民币汇率（积分/元），非法时回退 20。
func CreatorPointsPerYuan() float64 {
	rate := setting.GetGrowthConfig().PointsPerYuan
	if rate <= 0 {
		return 20
	}
	return rate
}

// CreatorCashYuan 可提现积分按当前汇率换成人民币（截到分）。
func CreatorCashYuan(points int) float64 {
	return CreatorCashYuanAt(points, CreatorPointsPerYuan())
}

// CreatorCashYuanAt 按指定汇率把积分换成人民币（截到分）。
func CreatorCashYuanAt(points int, rate float64) float64 {
	if points <= 0 || rate <= 0 {
		return 0
	}
	return decimal.NewFromInt(int64(points)).Div(decimal.NewFromFloat(rate)).Truncate(2).InexactFloat64()
}

// CreatorPointsForCash 提现人民币需要扣的积分数（四舍五入到整数）。
func CreatorPointsForCash(yuan float64) int {
	return CreatorPointsForCashAt(yuan, CreatorPointsPerYuan())
}

// CreatorPointsForCashAt 按指定汇率把人民币换成积分数。
func CreatorPointsForCashAt(yuan, rate float64) int {
	if yuan <= 0 || rate <= 0 {
		return 0
	}
	n := decimal.NewFromFloat(yuan).Mul(decimal.NewFromFloat(rate)).Round(0).IntPart()
	if n <= 0 {
		return 0
	}
	return int(n)
}

// CreatorSkillRow / CreatorRecord / CreatorSummary GET /user/creator/summary。
type CreatorSkillRow struct {
	Id           int    `json:"id"`
	Title        string `json:"title"`
	ReviewStatus int    `json:"review_status"`
	Calls        int    `json:"calls"`
	Stars        int    `json:"stars"`
	Uses         int    `json:"uses"`
	Revenue      int    `json:"revenue"`
}

type CreatorRecord struct {
	Id         int    `json:"id"`
	SkillId    int    `json:"skill_id"`
	SkillTitle string `json:"skill_title"`
	FromUser   string `json:"from_user"`
	Points     int    `json:"points"`
	BizType    int    `json:"biz_type"`
	CreatedAt  int64  `json:"created_at"`
}

type CreatorSummary struct {
	Skills        []CreatorSkillRow `json:"skills"`
	CallsTotal    int               `json:"calls_total"`
	RevenueTotal  int               `json:"revenue_total"`
	Withdrawable  int               `json:"withdrawable"`
	CashAvailable float64           `json:"cash_available"`
	PointsPerYuan float64           `json:"points_per_yuan"`
	ShareRate     float64           `json:"share_rate"`
	Records       []CreatorRecord   `json:"records"`
}

func GetCreatorSummary(userId int) (*CreatorSummary, error) {
	out := &CreatorSummary{Skills: []CreatorSkillRow{}, Records: []CreatorRecord{}, ShareRate: setting.GetGrowthConfig().SkillAuthorShare}
	var skills []*model.Skill
	if err := model.DB.Where("user_id = ?", userId).Order("revenue_total desc, id desc").Limit(100).Find(&skills).Error; err != nil {
		return nil, err
	}
	titles := map[int]string{}
	for _, s := range skills {
		titles[s.Id] = s.Title
		out.Skills = append(out.Skills, CreatorSkillRow{Id: s.Id, Title: s.Title, ReviewStatus: s.ReviewStatus, Calls: s.CallsTotal, Stars: s.Stars, Uses: s.Uses, Revenue: s.RevenueTotal})
		out.CallsTotal += s.CallsTotal
		out.RevenueTotal += s.RevenueTotal
	}
	out.Withdrawable = CreatorWithdrawable(userId)
	out.PointsPerYuan = CreatorPointsPerYuan()
	out.CashAvailable = CreatorCashYuan(out.Withdrawable)
	var bills []*model.PromoterBill
	model.DB.Where("user_id = ? AND biz_type IN ?", userId, creatorSkillBalanceTypes).
		Order("id desc").Limit(50).Find(&bills)
	fromIds := []int{}
	for _, b := range bills {
		if b.FromUserId > 0 {
			fromIds = append(fromIds, b.FromUserId)
		}
	}
	names := model.GetUserNicknames(fromIds)
	for _, b := range bills {
		r := CreatorRecord{Id: b.Id, Points: int(b.Amount), BizType: b.BizType, CreatedAt: b.CreatedAt}
		if b.RefType == model.PromoterBillRefTypeTask && b.RefId > 0 {
			var t model.GenerationTask
			if model.DB.Select("skill_id").Where("id = ?", b.RefId).First(&t).Error == nil {
				r.SkillId = t.SkillId
				r.SkillTitle = titles[t.SkillId]
			}
		}
		if b.FromUserId > 0 {
			r.FromUser = maskNickname(names[b.FromUserId])
		}
		out.Records = append(out.Records, r)
	}
	return out, nil
}

func maskNickname(n string) string {
	r := []rune(n)
	if len(r) <= 1 {
		return "**"
	}
	if len(r) == 2 {
		return string(r[0]) + "*"
	}
	return string(r[0]) + "**" + string(r[len(r)-1])
}

// ExchangeCreatorRevenue 收益兑换成 90 天积分。
func ExchangeCreatorRevenue(userId, points int) error {
	if points <= 0 {
		return errors.New("请输入要兑换的积分数")
	}
	unlock, ok := LockCreatorPayout(userId)
	if !ok {
		return errors.New("操作进行中，请稍后再试")
	}
	defer unlock()
	if CreatorWithdrawable(userId) < points {
		return errors.New("可兑换收益不足")
	}
	now := common.GetTimestamp()
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if creatorWithdrawable(tx, userId) < points {
			return errors.New("可兑换收益不足")
		}
		if err := tx.Create(&model.PromoterBill{
			UserId: userId, BizType: model.PromoterBillTypeSkillExchange, Amount: -float64(points),
			Status: model.PromoterBillStatusValid, CreatedAt: now, Remark: fmt.Sprintf("收益兑换 %d 积分", points),
		}).Error; err != nil {
			return err
		}
		_, err := model.GrantPoints(tx, userId, model.LotSourceGift, points, now+90*86400, 0, 0,
			model.PointLogGrant, model.PointsBizSystem, setting.GrowthReasonSkillExchange, fmt.Sprintf("创作者收益兑换 %d 积分", points), 0)
		return err
	})
}

// CreatorCashWithdrawRequest 技能收益提现。
type CreatorCashWithdrawRequest struct {
	Amount      float64 `json:"amount"`
	Way         string  `json:"way"`
	AccountInfo string  `json:"account_info"`
}

// NormalizeCreatorCashWithdraw 校验金额、方式和收款账户 JSON。
func NormalizeCreatorCashWithdraw(req *CreatorCashWithdrawRequest) error {
	if req == nil {
		return errors.New("无效的参数")
	}
	req.Amount = decimal.NewFromFloat(req.Amount).Round(2).InexactFloat64()
	if req.Amount <= 0 {
		return errors.New("提现金额必须大于 0")
	}
	req.Way = strings.TrimSpace(req.Way)
	if req.Way == "" {
		req.Way = "alipay"
	}
	switch req.Way {
	case "alipay", "wechat", "bank":
	default:
		return errors.New("不支持的提现方式")
	}
	req.AccountInfo = strings.TrimSpace(req.AccountInfo)
	if req.AccountInfo == "" {
		return errors.New("请填写收款账户信息")
	}
	var accountCheck map[string]any
	if err := json.Unmarshal([]byte(req.AccountInfo), &accountCheck); err != nil {
		return errors.New("收款账户信息格式不正确")
	}
	return nil
}

// ApplyCreatorCashWithdraw 从技能分成冻结积分并建提现单（审核驳回 / 打款失败后退回积分）。
func ApplyCreatorCashWithdraw(userId int, req CreatorCashWithdrawRequest) (string, error) {
	if err := NormalizeCreatorCashWithdraw(&req); err != nil {
		return "", err
	}
	points := CreatorPointsForCash(req.Amount)
	if points <= 0 {
		return "", errors.New("提现金额过小")
	}
	unlock, ok := LockCreatorPayout(userId)
	if !ok {
		return "", errors.New("操作进行中，请稍后再试")
	}
	defer unlock()

	cash := CreatorCashYuan(CreatorWithdrawable(userId))
	if decimal.NewFromFloat(req.Amount).GreaterThan(decimal.NewFromFloat(cash)) {
		return "", errors.New("可提现收益不足")
	}

	withdrawNo := fmt.Sprintf("WD%s%d", common.GetRandomString(8), time.Now().Unix())
	now := common.GetTimestamp()
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if creatorWithdrawable(tx, userId) < points {
			return errors.New("可提现收益不足")
		}
		w := &model.Withdraw{
			WithdrawNo:   withdrawNo,
			OwnerType:    model.WithdrawOwnerTypeCreator,
			OwnerId:      userId,
			Amount:       req.Amount,
			Fee:          0,
			ActualAmount: req.Amount,
			Way:          req.Way,
			AccountInfo:  req.AccountInfo,
			Status:       model.WithdrawStatusPending,
			CreatedAt:    now,
		}
		if err := tx.Create(w).Error; err != nil {
			return err
		}
		return tx.Create(&model.PromoterBill{
			UserId:       userId,
			BizType:      model.PromoterBillTypeSkillCashOut,
			Amount:       -float64(points),
			SourceNo:     withdrawNo,
			SourceAmount: req.Amount,
			Rate:         CreatorPointsPerYuan(),
			RefType:      model.PromoterBillRefTypeWithdraw,
			RefId:        w.Id,
			Status:       model.PromoterBillStatusValid,
			CreatedAt:    now,
			Remark:       fmt.Sprintf("技能收益提现冻结 %d 积分（¥%.2f）", points, req.Amount),
		}).Error
	})
	if err != nil {
		return "", err
	}
	return withdrawNo, nil
}

// RefundCreatorCashWithdraw 审核驳回 / 打款失败：把冻结积分退回可兑换余额。已退过则跳过。
func RefundCreatorCashWithdraw(w *model.Withdraw, remark string) error {
	if w == nil || w.OwnerType != model.WithdrawOwnerTypeCreator || w.WithdrawNo == "" {
		return nil
	}
	var frozen model.PromoterBill
	if err := model.DB.Where("user_id = ? AND biz_type = ? AND source_no = ? AND status = ?",
		w.OwnerId, model.PromoterBillTypeSkillCashOut, w.WithdrawNo, model.PromoterBillStatusValid).
		Order("id desc").First(&frozen).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("找不到提现冻结流水")
		}
		return err
	}
	points := -int(frozen.Amount)
	if points <= 0 {
		return nil
	}
	var dup int64
	model.DB.Model(&model.PromoterBill{}).
		Where("user_id = ? AND biz_type = ? AND source_no = ? AND status = ?",
			w.OwnerId, model.PromoterBillTypeSkillCashRefund, w.WithdrawNo, model.PromoterBillStatusValid).
		Count(&dup)
	if dup > 0 {
		return nil
	}
	note := strings.TrimSpace(remark)
	if note == "" {
		note = "技能收益提现退回"
	}
	return model.DB.Create(&model.PromoterBill{
		UserId:       w.OwnerId,
		BizType:      model.PromoterBillTypeSkillCashRefund,
		Amount:       float64(points),
		SourceNo:     w.WithdrawNo,
		SourceAmount: w.Amount,
		RefType:      model.PromoterBillRefTypeWithdraw,
		RefId:        w.Id,
		Status:       model.PromoterBillStatusValid,
		CreatedAt:    common.GetTimestamp(),
		Remark:       fmt.Sprintf("%s %d 积分", note, points),
	}).Error
}

var (
	creatorPayoutLockTTL = 30 * time.Second
	creatorPayoutMu      sync.Mutex
	creatorPayoutLocal   = map[int]bool{}
)

const creatorPayoutUnlockScript = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`

// LockCreatorPayout 兑换 / 提现互斥，避免同一笔收益被花两次。
func LockCreatorPayout(userId int) (func(), bool) {
	if common.RedisClient != nil {
		key := fmt.Sprintf("easytv:creatorlock:%d", userId)
		token := common.GetRandomString(16)
		ok, err := common.RedisClient.SetNX(common.RedisCtx, key, token, creatorPayoutLockTTL).Result()
		if err == nil {
			if !ok {
				return nil, false
			}
			return func() {
				if err := common.RedisClient.Eval(common.RedisCtx, creatorPayoutUnlockScript, []string{key}, token).Err(); err != nil {
					common.SysError("creator payout unlock failed: " + err.Error())
				}
			}, true
		}
		common.SysError("creator payout lock failed, fallback to local: " + err.Error())
	}
	creatorPayoutMu.Lock()
	defer creatorPayoutMu.Unlock()
	if creatorPayoutLocal[userId] {
		return nil, false
	}
	creatorPayoutLocal[userId] = true
	return func() {
		creatorPayoutMu.Lock()
		delete(creatorPayoutLocal, userId)
		creatorPayoutMu.Unlock()
	}, true
}

// LeaderboardRow GET /creator/leaderboard。
type LeaderboardRow struct {
	UserId   int    `json:"user_id"`
	Nickname string `json:"nickname"`
	Calls    int64  `json:"calls"`
	Revenue  int    `json:"revenue"`
	Skills   int64  `json:"skills"`
}

func GetCreatorLeaderboard(period string) []LeaderboardRow {
	days := 7
	if period == "month" {
		days = 30
	}
	since := common.GetTimestamp() - int64(days)*86400
	type row struct {
		UserId  int
		Calls   int64
		Revenue float64
	}
	var rows []row
	model.DB.Model(&model.PromoterBill{}).Select("user_id, COUNT(*) AS calls, COALESCE(SUM(amount),0) AS revenue").
		Where("biz_type = ? AND status = ? AND created_at >= ?", model.PromoterBillTypeSkillRevenue, model.PromoterBillStatusValid, since).
		Group("user_id").Order("revenue desc").Limit(20).Scan(&rows)
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserId)
	}
	names := model.GetUserNicknames(ids)
	out := make([]LeaderboardRow, 0, len(rows))
	for _, r := range rows {
		var skills int64
		model.DB.Model(&model.Skill{}).Where("user_id = ? AND review_status = ?", r.UserId, model.SkillReviewPublished).Count(&skills)
		out = append(out, LeaderboardRow{UserId: r.UserId, Nickname: maskNickname(names[r.UserId]), Calls: r.Calls, Revenue: int(r.Revenue), Skills: skills})
	}
	return out
}
