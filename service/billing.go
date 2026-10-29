package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"

	"gorm.io/gorm"
)

// ===== 报价 / 下单（API.md 6.1 计价公式）=====

// QuoteRequest 报价与下单共用的商品参数。
type QuoteRequest struct {
	Kind     string `json:"kind"`      // membership / points
	PlanCode string `json:"plan_code"` // 会员单
	PackCode string `json:"pack_code"` // 积分包单
	Edition  string `json:"edition"`   // creator / team
	Cycle    string `json:"cycle"`     // month / quarter / year
	Seats    int    `json:"seats"`
	AddOn    bool   `json:"add_on"`
}

// Quote 报价结果。
type Quote struct {
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Origin      float64 `json:"origin"`
	Price       float64 `json:"price"`
	Points      int     `json:"points"`
	Months      int     `json:"months"`
	Seats       int     `json:"seats"`
	ValidDays   int     `json:"valid_days"`
	AddOnPoints int     `json:"add_on_points"`
	AddOnPrice  float64 `json:"add_on_price"`
	OffLabel    string  `json:"off_label"`
	IsMember    bool    `json:"is_member"`
	// MemberDay 会员日折扣说明（M7 剩余项）；未命中会员日为空
	DiscountLabel string `json:"discount_label"`

	plan *model.MembershipPlan
	pack *model.PointPack
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func offLabel(price, origin float64) string {
	if origin <= 0 || price >= origin {
		return ""
	}
	off := math.Round(price/origin*100) / 10
	if off == math.Trunc(off) {
		return fmt.Sprintf("限时 %d 折", int(off))
	}
	return fmt.Sprintf("限时 %.1f 折", off)
}

func editionLabel(edition string) string {
	if edition == model.EditionTeam {
		return "团队版会员"
	}
	return "创作会员"
}

func cycleLabel(cycle string) string {
	switch cycle {
	case model.CycleYear:
		return "按年购买"
	case model.CycleQuarter:
		return "按季购买"
	}
	return "按月购买"
}

func validityLabel(days int) string {
	switch {
	case days >= 730:
		return "有效期 2 年"
	case days >= 365:
		return "有效期 1 年"
	}
	return fmt.Sprintf("有效期 %d 天", days)
}

// BuildQuote 服务端计价；userId 用于积分包会员价（0 = 匿名）。
func BuildQuote(req QuoteRequest, userId int) (*Quote, error) {
	promo := model.GetBillingPromo()
	// 买会员 / 积分包都进个人钱包，会员价只看自己的会员（API.md 20）
	isMember := userId > 0 && IsMemberForPricing(userId, 0)
	switch req.Kind {
	case model.OrderKindMembership:
		plan, err := model.GetMembershipPlanByCode(strings.TrimSpace(req.PlanCode))
		if err != nil || plan.Status != model.BillingStatusOn {
			return nil, errors.New("会员计划不存在或已下架")
		}
		months := model.CycleMonths(req.Cycle)
		if months == 0 {
			return nil, errors.New("无效的购买周期")
		}
		edition := req.Edition
		if edition == "" {
			edition = model.EditionCreator
		}
		if edition != model.EditionCreator && edition != model.EditionTeam {
			return nil, errors.New("无效的版本")
		}
		seats := 1
		teamRate := 1.0
		if edition == model.EditionTeam {
			seats = req.Seats
			if seats < promo.TeamSeatMin || seats > promo.TeamSeatMax {
				return nil, fmt.Errorf("团队席位需在 %d 到 %d 之间", promo.TeamSeatMin, promo.TeamSeatMax)
			}
			teamRate = promo.TeamSeatRate
		}
		origin := plan.MonthlyOrigin * float64(months) * float64(seats)
		price := math.Round(plan.MonthlyPrice * float64(months) * promo.EffectiveCycleRate(req.Cycle) * float64(seats) * teamRate)
		addOnPoints := plan.AddOnPoints * seats
		addOnPrice := math.Round(plan.AddOnPrice * float64(seats) * teamRate)
		points := plan.MonthlyPoints * seats
		if req.AddOn {
			price += addOnPrice
			origin += addOnPrice
			points += addOnPoints
		}
		return &Quote{
			Kind:        req.Kind,
			Title:       fmt.Sprintf("%s · %s · %s", editionLabel(edition), plan.Name, cycleLabel(req.Cycle)),
			Origin:      round2(origin),
			Price:       round2(price),
			Points:      points,
			Months:      months,
			Seats:       seats,
			AddOnPoints: addOnPoints,
			AddOnPrice:  addOnPrice,
			OffLabel:    offLabel(price, origin),
			IsMember:    isMember,
			plan:        plan,
		}, nil
	case model.OrderKindPoints:
		pack, err := model.GetPointPackByCode(strings.TrimSpace(req.PackCode))
		if err != nil || pack.Status != model.BillingStatusOn {
			return nil, errors.New("积分包不存在或已下架")
		}
		price := pack.Origin
		if isMember && pack.MemberPrice > 0 {
			price = pack.MemberPrice
		}
		// 会员日：积分包按折扣再打一次，不与会员价叠加两次（取折后更低的那个）
		discountLabel := ""
		if md := MemberDayInfo(); md.Active {
			dayPrice := round2(price * md.PackDiscount)
			if dayPrice < price {
				price = dayPrice
				discountLabel = fmt.Sprintf("会员日 %s", discountText(md.PackDiscount))
			}
		}
		return &Quote{
			Kind:          req.Kind,
			Title:         fmt.Sprintf("%d 积分 · %s", pack.Points, validityLabel(pack.ValidDays)),
			Origin:        round2(pack.Origin),
			Price:         round2(price),
			Points:        pack.Points,
			Seats:         1,
			ValidDays:     pack.ValidDays,
			OffLabel:      offLabel(price, pack.Origin),
			IsMember:      isMember,
			DiscountLabel: discountLabel,
			pack:          pack,
		}, nil
	}
	return nil, errors.New("无效的商品类型")
}

// CreateOrder 按报价创建待支付订单。
func CreateOrder(userId int, req QuoteRequest) (*model.Order, *Quote, error) {
	q, err := BuildQuote(req, userId)
	if err != nil {
		return nil, nil, err
	}
	if q.Price < 0.01 {
		return nil, nil, errors.New("订单金额过低")
	}
	o := &model.Order{
		UserId:    userId,
		Kind:      q.Kind,
		Title:     q.Title,
		Origin:    q.Origin,
		Price:     q.Price,
		Points:    q.Points,
		Months:    q.Months,
		Seats:     q.Seats,
		AddOn:     req.AddOn && q.Kind == model.OrderKindMembership,
		ValidDays: q.ValidDays,
	}
	if q.plan != nil {
		o.PlanId = q.plan.Id
		o.Edition = req.Edition
		if o.Edition == "" {
			o.Edition = model.EditionCreator
		}
		o.Cycle = req.Cycle
	}
	if q.pack != nil {
		o.PackId = q.pack.Id
	}
	if err := o.Insert(); err != nil {
		return nil, nil, err
	}
	return o, q, nil
}

// ===== 履约 =====

// FulfillOrder 支付成功后履约（幂等：仅 pending 可履约）。
func FulfillOrder(orderId int, paymentMethod string) error {
	o := &model.Order{}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", orderId).First(o).Error; err != nil {
			return err
		}
		if o.Status != model.OrderStatusPending {
			return model.ErrOrderStatus
		}
		if err := model.SetOrderPaid(tx, o.Id, paymentMethod); err != nil {
			return err
		}
		switch o.Kind {
		case model.OrderKindMembership:
			plan, err := model.GetMembershipPlanById(o.PlanId)
			if err != nil {
				return fmt.Errorf("会员计划不存在：%d", o.PlanId)
			}
			renewed, m, err := model.UpsertMembership(tx, o.UserId, plan, o.Edition, o.Seats, o.Months, o.Id)
			if err != nil {
				return err
			}
			if o.Edition == model.EditionTeam {
				if err := EnsureTeamOnFulfill(tx, o.UserId, o.Seats); err != nil {
					return err
				}
			}
			reason := fmt.Sprintf("开通%s到账", o.Title)
			if renewed {
				reason = fmt.Sprintf("续费%s", o.Title)
			} else {
				// 新开/换计划：立即发首月积分
				if _, err := model.GrantPoints(tx, o.UserId, model.LotSourceMembershipGrant, m.MonthlyPoints,
					common.GetTimestamp()+model.LotTTLMembershipGrant, o.Id, 0, model.PointLogGrant, model.PointsBizOrder, o.OrderNo, reason, 0); err != nil {
					return err
				}
			}
			if o.AddOn {
				addOn := plan.AddOnPoints * o.Seats
				if _, err := model.GrantPoints(tx, o.UserId, model.LotSourceAddOn, addOn, m.ExpiresAt, o.Id, 0,
					model.PointLogGrant, model.PointsBizOrder, o.OrderNo, fmt.Sprintf("加购 %d 积分", addOn), 0); err != nil {
					return err
				}
			}
		case model.OrderKindPoints:
			expires := common.GetTimestamp() + int64(o.ValidDays)*86400
			if _, err := model.GrantPoints(tx, o.UserId, model.LotSourcePack, o.Points, expires, o.Id, 0,
				model.PointLogGrant, model.PointsBizOrder, o.OrderNo, fmt.Sprintf("购买 %s", o.Title), 0); err != nil {
				return err
			}
		default:
			return errors.New("未知订单类型")
		}
		common.SysLog(fmt.Sprintf("order fulfilled: %s user=%d kind=%s price=%.2f", o.OrderNo, o.UserId, o.Kind, o.Price))
		return nil
	})
	if err == nil {
		// 事务提交后再写事件与奖励：邀请首充返利（T12）、口令首充加赠（T15）
		Track(o.UserId, model.TrackEventPaySuccess, map[string]any{
			"order_no": o.OrderNo, "amount": o.Price, "kind": o.Kind, "plan_id": o.PlanId, "payment_method": paymentMethod,
		})
		onInviteePaid(o)
		onBonusRecharge(o)
		onPartnerPaid(o)
	}
	return err
}

// RefundOrder 管理端退款：标记 refunded，回收未消耗积分，会员来源于此单则失效。
func RefundOrder(orderId int, reason string, adminId int) (revoked int, err error) {
	o := &model.Order{}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", orderId).First(o).Error; err != nil {
			return err
		}
		if err := model.SetOrderRefunded(tx, o.Id, reason); err != nil {
			return err
		}
		n, err := model.RevokeOrderLots(tx, o.UserId, o.Id, "订单退款回收积分："+reason, adminId)
		if err != nil {
			return err
		}
		revoked = n
		if o.Kind == model.OrderKindMembership {
			return model.TerminateMembershipByOrder(tx, o.Id)
		}
		return nil
	})
	if err == nil {
		Track(o.UserId, model.TrackEventRefund, map[string]any{"order_no": o.OrderNo, "amount": o.Price, "revoked": revoked})
		revokeInviteRebateOnRefund(o.OrderNo)
		revokePartnerRebate(o)
	}
	return revoked, err
}

// AdjustPoints 后台调整：正数入账新批次，负数按最早过期扣减。
func AdjustPoints(userId, delta int, reason string, validDays int, adminId int) error {
	if delta == 0 {
		return errors.New("调整数量不能为 0")
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		bizNo := strconv.Itoa(adminId)
		if delta > 0 {
			ttl := int64(validDays) * 86400
			if ttl <= 0 {
				ttl = model.LotTTLAdmin
			}
			_, err := model.GrantPoints(tx, userId, model.LotSourceAdmin, delta, common.GetTimestamp()+ttl, 0, 0,
				model.PointLogAdjust, model.PointsBizAdmin, bizNo, reason, adminId)
			return err
		}
		return model.ConsumePoints(tx, userId, -delta, model.PointLogAdjust, model.PointsBizAdmin, bizNo, reason, adminId)
	})
}

// GiftPoints 系统赠送（seed / 新用户）。
func GiftPoints(userId, points, validDays int, reason string) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := model.GrantPoints(tx, userId, model.LotSourceGift, points, common.GetTimestamp()+int64(validDays)*86400, 0, 0,
			model.PointLogGrant, model.PointsBizSystem, "gift", reason, 0)
		return err
	})
}

// ===== 任务计费 =====

// EstimateTaskPoints 按模型定价与参数估算积分；未定价返回 ErrNotFound。
func EstimateTaskPoints(taskType, modelName, paramsJSON string, isMember bool) (points int, price *model.ModelPrice, err error) {
	price, err = model.GetModelPrice(modelName)
	if err != nil {
		return 0, nil, err
	}
	if price.Type != taskType {
		return 0, nil, fmt.Errorf("模型 %s 不是 %s 类型", modelName, taskType)
	}
	// 会员价 → 促销价（T18）：有效期内取 min(标准价/会员价, 促销价)
	unitPrice := price.EffectiveUnitPoints(isMember, common.GetTimestamp())
	var params map[string]any
	_ = json.Unmarshal([]byte(paramsJSON), &params)
	count := paramInt(params, "count", 1)
	if count < 1 {
		count = 1
	}
	// 分段配音（API.md 25.1）：每段各调一次 TTS，按段数计费
	if taskType == model.TaskTypeAudio {
		if segs, ok := params["segments"].([]any); ok && len(segs) > count {
			count = len(segs)
		}
	}
	// 对话式工作流生成（API.md 32）：一个任务跑多轮文本调用，按轮预扣。
	// StartTask 会把 count>1 归一为 1（task_start.go:40-42），所以轮数不能靠 count 表达，单独走 agent.max_rounds。
	if taskType == model.TaskTypeText {
		if n := agentRoundsFromMap(params); n > 1 {
			count = n
		}
	}
	units := count
	if price.Unit == model.PriceUnitSecond {
		seconds := paramInt(params, "duration", 5)
		if seconds < 1 {
			seconds = 5
		}
		units = seconds * count
	}
	return unitPrice * units, price, nil
}

// CheckModelDuration 提交前校验时长。各家模型允许的秒数不一样，
// 网关只会在提交时回 400 unsupported duration，用户白等一轮还得靠退款兜；
// model_price.durations 声明了就在这里先挡下来。
func CheckModelDuration(price *model.ModelPrice, paramsJSON string) error {
	if price == nil {
		return nil
	}
	allowed := price.AllowedDurations()
	if allowed == nil {
		return nil
	}
	var params map[string]any
	_ = json.Unmarshal([]byte(paramsJSON), &params)
	if _, ok := params["duration"]; !ok {
		return nil
	}
	seconds := paramInt(params, "duration", 0)
	if seconds <= 0 || price.DurationAllowed(seconds) {
		return nil
	}
	parts := make([]string, 0, len(allowed))
	for _, n := range allowed {
		parts = append(parts, strconv.Itoa(n))
	}
	return fmt.Errorf("%s 只支持 %s 秒，当前请求 %d 秒", price.Model, strings.Join(parts, " / "), seconds)
}

// CheckModelResolution 与 CheckModelDuration 同理：各模型收的分辨率不一样
// （hailuo-02 是 512P/768P/1080P，不收 720P），声明了就在提交前挡下来。
func CheckModelResolution(price *model.ModelPrice, paramsJSON string) error {
	if price == nil {
		return nil
	}
	allowed := price.AllowedResolutions()
	if allowed == nil {
		return nil
	}
	var params map[string]any
	_ = json.Unmarshal([]byte(paramsJSON), &params)
	res, _ := params["resolution"].(string)
	if strings.TrimSpace(res) == "" || price.ResolutionAllowed(res) {
		return nil
	}
	return fmt.Errorf("%s 只支持 %s，当前请求 %s", price.Model, strings.Join(allowed, " / "), res)
}

// CheckModelParams 提交前的参数能力校验（时长 + 分辨率）。
func CheckModelParams(price *model.ModelPrice, paramsJSON string) error {
	if err := CheckModelDuration(price, paramsJSON); err != nil {
		return err
	}
	return CheckModelResolution(price, paramsJSON)
}

// ImageInputModeOf 图片模型接收参考图的方式：有定价行按声明 / 推断，没有按模型名推断。
func ImageInputModeOf(modelName string) string {
	if price, err := model.GetModelPrice(modelName); err == nil {
		return price.EffectiveImageInput()
	}
	return model.InferImageInput(modelName)
}

// TaskResultCount 请求里的 count。网关调用固定 n=1、一次只落一个产物，count>1 会按多份扣分却只交付一份，
// 创建接口据此拒绝；批量由客户端拆成多条任务提交（画布就是这么做的）。
func TaskResultCount(paramsJSON string) int {
	var params map[string]any
	_ = json.Unmarshal([]byte(paramsJSON), &params)
	return paramInt(params, "count", 1)
}

// WithSingleCount 把参数里的 count 改成 1。
func WithSingleCount(paramsJSON string) string {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(paramsJSON), &m)
	m["count"] = 1
	b, _ := json.Marshal(m)
	return string(b)
}

func paramInt(params map[string]any, key string, def int) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(strings.TrimRight(strings.TrimSpace(v), "sS"))
		if err == nil {
			return n
		}
	}
	return def
}

// FreezeTaskPoints 创建任务时预扣（一期：直接从批次扣减，失败退回）。
// 买单账户按任务存下的 team_id 定（API.md 20）：个人空间扣自己，团队空间扣 owner 的积分池。
func FreezeTaskPoints(task *model.GenerationTask, points int) error {
	payer, err := PayerForTeam(task.UserId, task.TeamId)
	if err != nil {
		return err
	}
	task.PayerId = payer
	if points <= 0 {
		return model.DB.Model(&model.GenerationTask{}).Where("id = ?", task.Id).Update("payer_id", payer).Error
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if payer != task.UserId {
			// 团队成员花团队积分池时校验月度上限（F7）。锁住成员行串行化同一成员的预扣，
			// 已用 = 已结算 + 进行中任务的预扣，否则前一个任务没跑完时后一个也能通过。
			q, err := model.LockMemberQuota(tx, task.UserId)
			if err != nil {
				return err
			}
			if q.Limit > 0 {
				used := q.Used + model.SumMemberReservedPoints(tx, task.UserId)
				if used+points > q.Limit {
					return fmt.Errorf("本月团队额度不足（上限 %d 积分，已用及进行中 %d，本次需要 %d），请联系团队管理员", q.Limit, used, points)
				}
			}
		}
		reason := fmt.Sprintf("%s生成 %s", taskTypeLabel(task.Type), task.Model)
		if payer != task.UserId {
			reason += fmt.Sprintf("（团队成员 #%d）", task.UserId)
		}
		if err := model.ConsumePoints(tx, payer, points, model.PointLogFreeze, model.PointsBizTask, task.TaskNo, reason, 0); err != nil {
			var insufficient model.ErrInsufficientPoints
			if payer != task.UserId && errors.As(err, &insufficient) {
				return fmt.Errorf("团队积分不足，还需 %d 积分", insufficient.Need)
			}
			return err
		}
		return tx.Model(&model.GenerationTask{}).Where("id = ?", task.Id).Updates(map[string]any{"points_frozen": points, "payer_id": payer}).Error
	})
}

// CompleteTask 任务成功：结算（预扣即结算）并写结果。文本任务与无资产产物用它；
// 有资产产物的走 CompleteTaskWithAsset，资产和任务状态一起提交。
func CompleteTask(taskId, assetId int, resultUrl, resultText string) error {
	return completeTask(taskId, func(tx *gorm.DB, t *model.GenerationTask) error {
		return model.SetTaskSucceededTx(tx, taskId, assetId, resultUrl, resultText, t.PointsFrozen)
	})
}

// CompleteTaskWithAsset 媒体任务成功：资产入库与任务置成功在同一事务里。
// 任务已经不在进行中（用户取消了 / 超时判失败，积分已退）时整体回滚并返回 model.ErrTaskStatus，
// 调用方负责删掉已经存进存储的文件——不能让退了积分的结果照样进资产库。
func CompleteTaskWithAsset(taskId int, asset *model.Asset) error {
	return completeTask(taskId, func(tx *gorm.DB, t *model.GenerationTask) error {
		if err := asset.InsertTx(tx); err != nil {
			return err
		}
		return model.SetTaskSucceededTx(tx, taskId, asset.Id, asset.Url, "", t.PointsFrozen)
	})
}

func completeTask(taskId int, succeed func(tx *gorm.DB, t *model.GenerationTask) error) error {
	t, err := model.GetTaskById(taskId)
	if err != nil {
		return err
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := succeed(tx, t); err != nil {
			return err
		}
		// 团队成员月度已用额度（F7）：和任务离开进行中同时生效，额度校验才不会有空窗
		if t.PayerId > 0 && t.PayerId != t.UserId && t.PointsFrozen > 0 {
			return model.AddMemberPointsUsedTx(tx, t.UserId, t.PointsFrozen)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if t.Type == model.TaskTypeVideo {
		// 首条视频：新手任务与邀请奖励的触发点（API.md 7.4）
		TrackOnce(t.UserId, model.TrackEventFirstVideo, map[string]any{"task_no": t.TaskNo, "model": t.Model})
	}
	// 技能作者分成（API.md 8.3）：结算成功后按实际扣分记账
	RecordSkillRevenue(t)
	if t.PointsFrozen > 0 {
		payer := t.PayerId
		if payer == 0 {
			payer = t.UserId
		}
		_ = model.DB.Transaction(func(tx *gorm.DB) error {
			return tx.Create(&model.PointLog{
				UserId:       payer,
				Type:         model.PointLogSettle,
				Amount:       0,
				BalanceAfter: model.GetPointsBalance(tx, payer),
				BizType:      model.PointsBizTask,
				BizNo:        t.TaskNo,
				Reason:       fmt.Sprintf("生成完成，结算 %d 积分", t.PointsFrozen),
				CreatedAt:    common.GetTimestamp(),
			}).Error
		})
	}
	// 记录模型调用完成审计
	recordTaskAudit(t, "model.api_call.complete", "生成任务完成", t.PointsFrozen, true)
	if _, ok := subjectNodePrefix(t.NodeId); ok {
		ContinueSubjectViews(t.Id)
	}
	return nil
}

// recordTaskAudit 记录任务相关的使用日志（service 层无 gin context）。
func recordTaskAudit(t *model.GenerationTask, action, content string, costPoints int, success bool) {
	status := 200
	if !success {
		status = 500
	}
	user, _ := model.GetUserById(t.UserId)
	username := ""
	role := common.RoleCommonUser
	if user != nil {
		username = user.Username
		role = user.Role
	}
	model.RecordAuditLog(nil, model.AuditLog{
		UserId: t.UserId, Username: username, ActorRole: role,
		CreatedAt: common.GetTimestamp(),
		Category:  model.AuditCategoryUsage, Action: action, Content: content,
		Status: status, Success: success,
		Other: model.AuditOther{
			Op: &model.AuditOperation{Action: action, Params: model.AuditFields{
				"task_id": t.Id, "model": t.Model, "type": t.Type, "provider": t.Provider,
				"task_no": t.TaskNo, "cost_points": costPoints,
			}},
		},
	})
}

// FailTask 任务失败：置失败、退回预扣积分、免积分对话任务再退一次每日次数，全部同一事务提交。
// 退次数的幂等闸门是任务行上的条件更新（refundChatQuotaTx，payer_id 0→user_id），
// 失败 / 取消 / 补偿扫描重复调用只退一次。
func FailTask(taskId int, msg string) error {
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.SetTaskFailedTx(tx, taskId, msg); err != nil {
			return err
		}
		t, err := refundTaskPointsTx(tx, taskId, "生成失败退回")
		if err != nil {
			return err
		}
		return refundChatQuotaTx(tx, t)
	})
	if err != nil {
		return err
	}
	// 记录模型调用失败审计
	if t, loadErr := model.GetTaskById(taskId); loadErr == nil {
		recordTaskAudit(t, "model.api_call.complete", "生成任务失败", 0, false)
		AbortWaitingSubjectViews(t)
	}
	return nil
}

// CancelTask 用户取消：置取消、退回预扣积分、免积分对话任务再退一次每日次数，全部同一事务提交。
func CancelTask(userId, taskId int) error {
	// 退分会把 points_frozen 清零，先记下金额，审计里才看得到退了多少
	frozen := 0
	if t, err := model.GetTaskById(taskId); err == nil {
		frozen = t.PointsFrozen
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.CancelUserTaskTx(tx, userId, taskId); err != nil {
			return err
		}
		t, err := refundTaskPointsTx(tx, taskId, "取消任务退回")
		if err != nil {
			return err
		}
		return refundChatQuotaTx(tx, t)
	})
	if err != nil {
		return err
	}
	// 取消也要留痕：任务中心里「这条是谁取消的、退了多少分」得查得到
	if t, loadErr := model.GetTaskById(taskId); loadErr == nil {
		recordTaskAudit(t, "model.api_call.cancel", "用户取消生成任务", frozen, true)
		AbortWaitingSubjectViews(t)
	}
	return nil
}

// refundTaskPointsTx 退回任务预扣积分。幂等：先把 points_frozen 清零，只有本次清零成功才退。
// 返回任务当前行（读不到返回 nil, err），调用方据此判断是不是免积分对话任务、要不要退次数。
func refundTaskPointsTx(tx *gorm.DB, taskId int, reason string) (*model.GenerationTask, error) {
	t := &model.GenerationTask{}
	if err := tx.Where("id = ?", taskId).First(t).Error; err != nil {
		return nil, err
	}
	if t.PointsFrozen <= 0 {
		return t, nil
	}
	payer := t.PayerId
	if payer == 0 {
		payer = t.UserId
	}
	result := tx.Model(&model.GenerationTask{}).Where("id = ? AND points_frozen > 0", taskId).Update("points_frozen", 0)
	if result.Error != nil || result.RowsAffected == 0 {
		return t, result.Error
	}
	_, err := model.GrantPoints(tx, payer, model.LotSourceRefund, t.PointsFrozen, common.GetTimestamp()+model.LotTTLRefund, 0, t.Id,
		model.PointLogUnfreeze, model.PointsBizTask, t.TaskNo, reason, 0)
	return t, err
}

// refundChatQuotaTx 免积分对话任务的退次数：把任务行的 payer_id 从 0 置成 user_id，
// 只有本次条件更新成功（RowsAffected=1）才在同一事务里扣减 ai_chat_usage 的消费日次数。
//
// 与 refundTaskPointsTx 同一幂等思路（先读、再条件更新、赢家才退），幂等性挂在列上而不是 params 文本上：
// 免积分任务创建时跳过预扣、不设买单账户，payer_id 恒为 0（付费任务这里因 IsFreeChatTask 直接返回，
// 永不执行本更新）；params 里的 free 由 JSON 解析判定，客户端可以带空格（{"free": true}）或在
// question 里写字面量，用 LIKE/REPLACE 做闸门会既漏退又可能误判。
//
// 扣减留在本事务里：次数计数在 ai_chat_usage 表、没有事务版接口（model.DecrChatUsage 走全局 DB），
// 这里用同一事务的 tx.Exec 执行与它逐字一致的语句。提交成功 = 状态 / 标记 / 次数一起生效，
// 提交失败 = 三者一起回滚、任务仍可被超时扫描重试，不存在「标记已落库、次数却没退」又无人补偿的中间态。
//
// 退的是消费日而不是退款日（与 RefundChatQuotaOn 同一口径）：任务一创建就消费，CreatedAt 即消费日；
// 拿不到创建时间就整段跳过、连标记都不落，宁可少退一次也不许把别的日子减错。
func refundChatQuotaTx(tx *gorm.DB, t *model.GenerationTask) error {
	if t == nil || !IsFreeChatTask(t) {
		return nil
	}
	// 消费日优先读创建时落的标记（params.agent.quota_day）：标记为空 = 创建时是会员 / 不限次、没消费过，不退；
	// 老任务没有标记才按创建时间算消费日（老口径）。
	day, tagged := ChatQuotaDayOf(t)
	if tagged && day == "" {
		return nil
	}
	if !tagged {
		if t.CreatedAt <= 0 {
			common.SysError(fmt.Sprintf("task: chat quota refund skipped %s: created_at unknown", t.TaskNo))
			return nil
		}
		day = model.ChatUsageDay(time.Unix(t.CreatedAt, 0))
	}
	res := tx.Model(&model.GenerationTask{}).
		Where("id = ? AND payer_id = 0", t.Id).
		Update("payer_id", t.UserId)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	// 与 model.DecrChatUsage 逐字一致的条件扣减：count > 0 保证计数不为负，没有记录时 0 行也不报错
	return tx.Exec(
		"UPDATE ai_chat_usage SET count = count - 1, updated_at = ? WHERE user_id = ? AND day = ? AND count > 0",
		common.GetTimestamp(), t.UserId, day,
	).Error
}

// agentRoundsFromMap 读 params.agent.max_rounds，夹到 [0, AgentMaxRoundsHardLimit]；缺失或非法返回 0。
func agentRoundsFromMap(params map[string]any) int {
	agent, _ := params["agent"].(map[string]any)
	if agent == nil {
		return 0
	}
	n := paramInt(agent, "max_rounds", 0)
	if n < 0 {
		n = 0
	}
	if n > AgentMaxRoundsHardLimit {
		n = AgentMaxRoundsHardLimit
	}
	return n
}

// AgentRoundsInParams 读 params.agent.max_rounds，夹到 [0, AgentMaxRoundsHardLimit]；缺失或非法返回 0。
func AgentRoundsInParams(paramsJSON string) int {
	var params map[string]any
	if json.Unmarshal([]byte(paramsJSON), &params) != nil {
		return 0
	}
	return agentRoundsFromMap(params)
}

// AgentSettlePoints 按实际轮数算这次 agent 任务的结算积分：
// 用 {"agent":{"max_rounds":rounds}} 重新调 EstimateTaskPoints（不另写计价公式）。
func AgentSettlePoints(taskType, modelName string, rounds int, isMember bool) (int, error) {
	if rounds < 1 {
		rounds = 1
	}
	if rounds > AgentMaxRoundsHardLimit {
		rounds = AgentMaxRoundsHardLimit
	}
	points, _, err := EstimateTaskPoints(taskType, modelName, fmt.Sprintf(`{"agent":{"max_rounds":%d}}`, rounds), isMember)
	return points, err
}

// RefundTaskPointsPartial 按实际用量退回部分预扣积分：points_frozen 减 refund（不小于 0），
// 差额入退款批次并写 unfreeze 流水。幂等：refund<=0 或剩余预扣不足时不动账。返回实际退回的积分。
// reason 例：「按实际 3 轮结算，退回 3 积分」。
func RefundTaskPointsPartial(taskId, refund int, reason string) (int, error) {
	if refund <= 0 {
		return 0, nil
	}
	refunded := 0
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		t := &model.GenerationTask{}
		if err := tx.Where("id = ?", taskId).First(t).Error; err != nil {
			return err
		}
		if t.PointsFrozen <= 0 {
			return nil
		}
		n := refund
		if n > t.PointsFrozen {
			// 剩余预扣不足：最多退到 0，不让 points_frozen 变负
			n = t.PointsFrozen
		}
		if n <= 0 {
			return nil
		}
		// 条件更新保证幂等：同一笔只退一次
		result := tx.Model(&model.GenerationTask{}).Where("id = ? AND points_frozen >= ?", taskId, n).
			Update("points_frozen", gorm.Expr("points_frozen - ?", n))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		payer := t.PayerId
		if payer == 0 {
			payer = t.UserId
		}
		if _, err := model.GrantPoints(tx, payer, model.LotSourceRefund, n, common.GetTimestamp()+model.LotTTLRefund, 0, t.Id,
			model.PointLogUnfreeze, model.PointsBizTask, t.TaskNo, reason, 0); err != nil {
			return err
		}
		refunded = n
		return nil
	})
	if err != nil {
		return 0, err
	}
	return refunded, nil
}

// RefundUnrefundedTasks 补偿：已失败 / 已取消但积分还冻着的任务补退。返回补退条数。
func RefundUnrefundedTasks() int {
	list, err := model.GetUnrefundedTasks(200)
	if err != nil {
		common.SysError("task: query unrefunded tasks failed: " + err.Error())
		return 0
	}
	n := 0
	for _, t := range list {
		reason := "生成失败退回"
		if t.Status == model.TaskStatusCancelled {
			reason = "取消任务退回"
		}
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			_, err := refundTaskPointsTx(tx, t.Id, reason)
			return err
		})
		if err != nil {
			common.SysError(fmt.Sprintf("task: refund %s failed: %v", t.TaskNo, err))
			continue
		}
		n++
		common.SysLog(fmt.Sprintf("task: %s refunded %d points by compensation", t.TaskNo, t.PointsFrozen))
	}
	return n
}

func taskTypeLabel(t string) string {
	switch t {
	case model.TaskTypeText:
		return "文本"
	case model.TaskTypeImage:
		return "图片"
	case model.TaskTypeVideo:
		return "视频"
	case model.TaskTypeAudio:
		return "语音"
	}
	return t
}

// ===== 后台任务 =====

// RunBillingJobs 关单 / 会员月发 / 会员过期 / 批次过期，由 main.go 定时调用。
func RunBillingJobs() {
	if n := model.ExpirePendingOrders(); n > 0 {
		common.SysLog(fmt.Sprintf("billing: closed %d expired orders", n))
	}
	if n := model.ExpireMemberships(); n > 0 {
		common.SysLog(fmt.Sprintf("billing: expired %d memberships", n))
	}
	GrantMonthlyPoints()
	ExpireBonusRewards()
	if n, err := model.ExpireLots(500); err != nil {
		common.SysError("billing: expire lots failed: " + err.Error())
	} else if n > 0 {
		common.SysLog(fmt.Sprintf("billing: expired %d point lots", n))
	}
}

// ExpireStaleTasks 超时兜底：长期停留在 pending / running 的任务标失败并退回预扣积分。
// 阈值：TASK_PENDING_TIMEOUT_MINUTES（默认 15）、TASK_RUNNING_TIMEOUT_MINUTES（默认 30）。
func ExpireStaleTasks() {
	ResumeSubjectViews()
	now := common.GetTimestamp()
	pendingMin := common.GetEnvOrDefault("TASK_PENDING_TIMEOUT_MINUTES", 15)
	runningMin := common.GetEnvOrDefault("TASK_RUNNING_TIMEOUT_MINUTES", 30)
	RefundUnrefundedTasks()
	list, err := model.GetStaleTasks(now-int64(pendingMin*60), now-int64(runningMin*60), 200)
	if err != nil {
		common.SysError("task: query stale tasks failed: " + err.Error())
		return
	}
	for _, t := range list {
		if err := FailTask(t.Id, "生成超时，已自动终止并退回积分"); err != nil {
			common.SysError(fmt.Sprintf("task: expire %s failed: %v", t.TaskNo, err))
			continue
		}
		common.SysLog(fmt.Sprintf("task: %s expired (status=%s, frozen=%d)", t.TaskNo, t.Status, t.PointsFrozen))
		link := "/canvas"
		if t.CanvasId > 0 {
			link = fmt.Sprintf("/canvas?id=%d", t.CanvasId)
		}
		model.PushNotification(t.UserId, model.NotificationTypeTask, "生成任务超时",
			fmt.Sprintf("%s 任务（%s）长时间未完成，已自动终止并退回 %d 积分。", t.Type, t.Model, t.PointsFrozen), link)
	}
}

// GrantMonthlyPoints 到期的会员月发积分。
func GrantMonthlyPoints() {
	list, err := model.GetMembershipsDueForGrant(200)
	if err != nil {
		common.SysError("billing: query due memberships failed: " + err.Error())
		return
	}
	for _, m := range list {
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			ok, err := model.AdvanceMembershipGrant(tx, m.Id, m.NextGrantAt)
			if err != nil || !ok {
				return err
			}
			_, err = model.GrantPoints(tx, m.UserId, model.LotSourceMembershipGrant, m.MonthlyPoints,
				common.GetTimestamp()+model.LotTTLMembershipGrant, m.SourceOrderId, 0,
				model.PointLogGrant, model.PointsBizSystem, fmt.Sprintf("membership:%d", m.Id),
				fmt.Sprintf("%s会员每月积分到账", m.PlanName), 0)
			return err
		})
		if err != nil {
			common.SysError(fmt.Sprintf("billing: monthly grant failed for membership %d: %v", m.Id, err))
		}
	}
}

// WalletView 钱包总览。个人空间只展示自己的钱包（不再附团队积分池）；
// 团队空间展示团队积分池（owner 的余额）与 owner 的团队版会员，并附 team 块（API.md 20）。
func WalletView(userId, teamId int) map[string]any {
	view := map[string]any{
		"balance":         model.GetPointsBalance(nil, userId),
		"frozen":          model.GetFrozenPoints(userId),
		"expiring_in_7d":  model.GetExpiringPoints(userId, 7),
		"membership":      nil,
		"server_time":     time.Now().Unix(),
		"promo_enabled":   model.GetBillingPromo().Enabled,
		"is_member":       false,
		"membership_days": 0,
		"team":            nil,
	}
	if teamId > 0 {
		if team, err := model.GetTeamById(teamId); err == nil {
			return teamWalletView(view, userId, team)
		}
	}
	if m := model.GetActiveMembership(userId); m != nil {
		view["membership"] = m
		view["is_member"] = true
		view["membership_days"] = int((m.ExpiresAt - common.GetTimestamp()) / 86400)
	}
	return view
}

// teamWalletView 团队空间的钱包：余额 / 预扣 / 7 天内过期都按 owner 的积分池。
func teamWalletView(view map[string]any, userId int, team *model.Team) map[string]any {
	view["balance"] = model.GetPointsBalance(nil, team.OwnerId)
	view["frozen"] = model.GetFrozenPoints(team.OwnerId)
	view["expiring_in_7d"] = model.GetExpiringPoints(team.OwnerId, 7)
	if m := model.GetActiveMembership(team.OwnerId); m != nil && m.Edition == model.EditionTeam {
		view["membership"] = m
		view["is_member"] = true
		view["membership_days"] = int((m.ExpiresAt - common.GetTimestamp()) / 86400)
	}
	q := model.GetMemberQuota(userId)
	var left any
	if q.Limit > 0 {
		left = q.Left
	}
	permission := ""
	if m := membershipOf(userId); m != nil && m.TeamId == team.Id {
		permission = m.EffectivePermission()
	}
	view["team"] = map[string]any{
		"id":           team.Id,
		"name":         team.Name,
		"permission":   permission,
		"member_limit": q.Limit,
		"member_used":  q.Used,
		"member_left":  left,
	}
	return view
}
