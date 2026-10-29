package controller

import (
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// AI 对话配置的取值边界（契约：max_tokens ∈[0,32000]、daily_free_count ∈[0,100000]、knowledge ≤8000 字符）。
const (
	aiChatMaxTokensLimit      = 32000
	aiChatDailyFreeCountLimit = 100000
	aiChatKnowledgeMaxRunes   = 8000
)

// aiChatConfigPayload 管理端 AI 对话配置 payload：指针字段 = 局部更新（nil 表示本次不改该键）。
type aiChatConfigPayload struct {
	Enabled        *bool   `json:"enabled"`
	Free           *bool   `json:"free"`
	Model          *string `json:"model"`
	MaxTokens      *int    `json:"max_tokens"`
	ThinkingMode   *string `json:"thinking_mode"`
	ThinkingEffort *string `json:"thinking_effort"`
	DailyFreeCount *int    `json:"daily_free_count"`
	Knowledge      *string `json:"knowledge"`
}

// validAIChatConfigPayload 校验 AI 对话配置 payload：全部合法返回 ""，否则返回给用户看的中文原因。
// 纯函数（不查库、不起 HTTP）：model 的「已定价 text 模型」判定需要查库，由调用方判好后经 modelPriced 传入；
// payload.Model 为 nil 或空串（表示清空、回到自动选模型）时忽略该参数。
// 校验顺序 model → max_tokens → thinking_mode → thinking_effort → daily_free_count → knowledge，命中即返回。
func validAIChatConfigPayload(p *aiChatConfigPayload, modelPriced bool) string {
	if p == nil {
		return ""
	}
	if p.Model != nil {
		if name := strings.TrimSpace(*p.Model); name != "" && !modelPriced {
			return "对话模型必须是已定价的文本模型"
		}
	}
	if p.MaxTokens != nil && (*p.MaxTokens < 0 || *p.MaxTokens > aiChatMaxTokensLimit) {
		return "最大输出 token 必须在 0 到 32000 之间"
	}
	if p.ThinkingMode != nil {
		switch strings.TrimSpace(*p.ThinkingMode) {
		case "", "off", "on":
		default:
			return "思考模式只能是 off、on 或留空"
		}
	}
	if p.ThinkingEffort != nil {
		switch strings.TrimSpace(*p.ThinkingEffort) {
		case "", "low", "medium", "high":
		default:
			return "思考强度只能是 low、medium、high 或留空"
		}
	}
	if p.DailyFreeCount != nil && (*p.DailyFreeCount < 0 || *p.DailyFreeCount > aiChatDailyFreeCountLimit) {
		return "每日免费次数必须在 0 到 100000 之间"
	}
	if p.Knowledge != nil && len([]rune(strings.TrimSpace(*p.Knowledge))) > aiChatKnowledgeMaxRunes {
		return "平台知识最多 8000 字"
	}
	return ""
}

// validChatSettingWrite 通用设置入口（PUT /admin/setting/:key）对长度受限键的补校验。
// ChatKnowledge 的长度上限专用接口已挡，这里兜住运维从通用入口直接改键这条旁路，
// 让「平台知识不会把提示词撑爆」在两条写入路径上都成立；其余键沿用既有语义，不做额外限制。
func validChatSettingWrite(key, value string) string {
	if key == setting.ChatKnowledge && len([]rune(strings.TrimSpace(value))) > aiChatKnowledgeMaxRunes {
		return "平台知识最多 8000 字"
	}
	return ""
}

// aiChatConfigView AI 对话配置视图；text_models 只列生效中（已定价）的文本模型，供管理端下拉选择。
func aiChatConfigView() gin.H {
	textModels := make([]gin.H, 0)
	if list, err := model.GetModelPrices(true); err == nil {
		for _, p := range list {
			if p.Type == model.TaskTypeText {
				textModels = append(textModels, gin.H{"model": p.Model, "label": p.Label})
			}
		}
	}
	return gin.H{
		"enabled":          setting.IsAgentEnabled(),
		"free":             setting.IsChatFreeEnabled(),
		"model":            setting.GetAgentModel(),
		"max_tokens":       setting.GetChatMaxTokens(),
		"thinking_mode":    setting.GetChatThinkingMode(),
		"thinking_effort":  setting.GetChatThinkingEffort(),
		"daily_free_count": setting.GetChatDailyFreeCount(),
		"knowledge":        setting.GetChatKnowledge(),
		"text_models":      textModels,
	}
}

// AdminGetAIChatConfig AI 对话配置（GET /admin/ai-chat/config）。
func AdminGetAIChatConfig(c *gin.Context) {
	common.ApiSuccess(c, aiChatConfigView())
}

// AdminUpdateAIChatConfig 更新 AI 对话配置（PUT /admin/ai-chat/config）。
// 先整体校验再逐键写入；UpdateSetting 同步内存缓存，写完立即热生效，返回与 GET 相同的视图。
func AdminUpdateAIChatConfig(c *gin.Context) {
	var payload aiChatConfigPayload
	if err := common.DecodeJson(c.Request.Body, &payload); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	modelPriced := false
	if payload.Model != nil {
		if name := strings.TrimSpace(*payload.Model); name != "" {
			if p, err := model.GetModelPrice(name); err == nil && p.Type == model.TaskTypeText {
				modelPriced = true
			}
		}
	}
	if msg := validAIChatConfigPayload(&payload, modelPriced); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if payload.Enabled != nil {
		if err := model.UpdateSetting(setting.AgentEnabled, strconv.FormatBool(*payload.Enabled)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.Free != nil {
		if err := model.UpdateSetting(setting.ChatFreeEnabled, strconv.FormatBool(*payload.Free)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.Model != nil {
		if err := model.UpdateSetting(setting.AgentModel, strings.TrimSpace(*payload.Model)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.MaxTokens != nil {
		if err := model.UpdateSetting(setting.ChatMaxTokens, strconv.Itoa(*payload.MaxTokens)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.ThinkingMode != nil {
		if err := model.UpdateSetting(setting.ChatThinkingMode, strings.TrimSpace(*payload.ThinkingMode)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.ThinkingEffort != nil {
		if err := model.UpdateSetting(setting.ChatThinkingEffort, strings.TrimSpace(*payload.ThinkingEffort)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.DailyFreeCount != nil {
		if err := model.UpdateSetting(setting.ChatDailyFreeCount, strconv.Itoa(*payload.DailyFreeCount)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.Knowledge != nil {
		if err := model.UpdateSetting(setting.ChatKnowledge, strings.TrimSpace(*payload.Knowledge)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	common.ApiSuccess(c, aiChatConfigView())
}
