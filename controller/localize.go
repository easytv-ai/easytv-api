package controller

import (
	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 字幕与多语言（API.md 10.4，F5）=====

// TranslateSubtitle POST /user/subtitle/translate { text, target_lang }
func TranslateSubtitle(c *gin.Context) {
	var req struct {
		Text       string `json:"text"`
		TargetLang string `json:"target_lang"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if len([]rune(req.Text)) > 5000 {
		common.ApiErrorMsg(c, "文本过长，最多 5000 字")
		return
	}
	out, err := service.TranslateText(req.Text, req.TargetLang)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"text": out, "lang": service.NormalizeLang(req.TargetLang)})
}

// LocalizeScreenplay POST /user/screenplay/:id/localize { target_lang }
func LocalizeScreenplay(c *gin.Context) {
	userId := c.GetInt("id")
	sp, err := model.GetUserScreenplay(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	var req struct {
		TargetLang string `json:"target_lang"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	plan, err := service.LocalizeScreenplay(userId, sp, req.TargetLang)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	sp, _ = model.GetUserScreenplay(userId, sp.Id)
	common.ApiSuccess(c, gin.H{"screenplay": sp, "plan": plan})
}

// GetLangs GET /langs 支持的语种（前端下拉用，匿名可调）
func GetLangs(c *gin.Context) {
	items := make([]gin.H, 0, len(service.SupportedLangs))
	for _, code := range []string{"zh", "en", "ja", "ko", "es", "fr"} {
		if service.NormalizeLang(code) == "" {
			continue
		}
		items = append(items, gin.H{"code": code, "label": service.SupportedLangs[code]})
	}
	common.ApiSuccess(c, items)
}
