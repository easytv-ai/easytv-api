package service

import (
	"fmt"
	"strings"

	"easytv-api/model"
)

// AgentFactsInput 平台事实的输入：调用方查好会员、免费对话与积分状态后传进来，这里只做渲染、不查库。
// 「不限」的口径与 service.ChatQuota（契约第 5 条）一致：Remaining < 0 或 DailyLimit <= 0 表示不限次数。
type AgentFactsInput struct {
	Models        map[string][]AgentModelHint // 可用模型（与 AgentPromptInput.Models 同一份目录）
	IsMember      bool                        // 当前用户是不是会员（IsMemberForPricing）
	MemberExpiry  string                      // 会员到期日（YYYY-MM-DD；空表示没有到期日）
	FreeChat      bool                        // 当前是不是免费对话
	DailyLimit    int                         // 免费对话每日次数上限；<=0 表示不限
	Remaining     int                         // 今日剩余次数；<0 表示不限
	PointsBalance int                         // 积分余额
}

// AgentFactsText 生成【平台事实】段的中文正文：只写已核实的事实，模型回答平台规则问题时只能照它说。
// 可用模型列表复用 agentModelListText，避免两处渲染口径漂移；模型与价格只在传了 Models 时输出，
// 调用方不传就等于放弃这段快照——生产路径（agentFactsFor）不传，所以 BuildAgentPrompt 会用自己拿到的
// 模型目录把价格行补进【平台事实】，保证线上提示词里有价格事实（见 agent_facts.go 的 agentModelPriceLine 调用点）。
func AgentFactsText(in AgentFactsInput) string {
	lines := []string{}
	if len(in.Models) > 0 {
		lines = append(lines, "可用模型与档位：\n"+agentModelListText(in.Models, ""))
		if price := agentModelPriceLine(in.Models); price != "" {
			lines = append(lines, price)
		}
	}

	if in.IsMember {
		line := "当前用户是会员，按会员价计费，促销生效时按更低的促销价"
		if expiry := strings.TrimSpace(in.MemberExpiry); expiry != "" {
			line += "，会员有效期至 " + expiry
		}
		lines = append(lines, line)
	} else {
		lines = append(lines, "当前用户不是会员，按标准价计费，促销生效时按更低的促销价")
	}

	if in.FreeChat {
		lines = append(lines, "本次对话是免费对话，不扣积分")
	} else {
		lines = append(lines, "本次对话不是免费对话，按积分计费")
	}

	// 剩余次数只在「本次对话是免费对话」时才有意义（复核 claim2d）：免费开关关闭时任务按积分计费，
	// 每日次数不消费也不退，再报「剩余 N 次（每日免费 M 次）」会和上一行自相矛盾、误导模型。
	// 在途免费任务（创建时开关开启）仍带 FreeChat=true，它的次数确实消费过，照常报剩余。
	if in.FreeChat {
		if in.Remaining < 0 || in.DailyLimit <= 0 {
			lines = append(lines, "今日 AI 对话次数不限")
		} else {
			lines = append(lines, fmt.Sprintf("今日 AI 对话剩余 %d 次（每日免费 %d 次）", in.Remaining, in.DailyLimit))
		}
	}

	lines = append(lines, fmt.Sprintf("当前积分余额：%d 分", in.PointsBalance))
	return strings.Join(lines, "\n")
}

// agentModelPriceLine 把标准单价渲染成一行（单位换成人话）。模型列表本体复用 agentModelListText，
// 这里只补价格，不重复渲染模型能力。
func agentModelPriceLine(hints map[string][]AgentModelHint) string {
	units := map[string]string{"count": "次", "second": "秒", "image": "张", "call": "次"}
	parts := []string{}
	for _, t := range []string{model.TaskTypeText, model.TaskTypeImage, model.TaskTypeVideo, model.TaskTypeAudio} {
		for _, h := range hints[t] {
			if h.Points <= 0 {
				continue
			}
			unit := units[h.Unit]
			if unit == "" {
				unit = h.Unit
			}
			if unit == "" {
				unit = "次"
			}
			parts = append(parts, fmt.Sprintf("%s %d 分/%s", h.Model, h.Points, unit))
			// 促销生效且确实更便宜（结算取 min）：把促销价一并写出，否则报价比真实扣费偏贵（复核 claim2c）
			if h.PromoActive && h.PromoPoints > 0 && h.PromoPoints < h.Points {
				parts[len(parts)-1] = fmt.Sprintf("%s %d 分/%s（促销价 %d 分/%s）", h.Model, h.Points, unit, h.PromoPoints, unit)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	// 只写已核实的事实（复核 claim2b）：这里只有标准单价是确定的——会员价由后台单独配置，
	// 后台校验不保证「会员价 <= 标准价」（controller/admin_billing.go 只查非负），
	// EffectiveUnitPoints 会员时直接取 MemberPointsPerUnit（model/billing_plan.go），
	// 所以不能承诺「会员价不高于标准价」。会员价未配置（0）时结算同标准价是确定语义，可以写。
	return "标准价（会员价未配置时同标准价）：" + strings.Join(parts, "、")
}
