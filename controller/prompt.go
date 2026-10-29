package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 提示词库：公开 + 管理端（API.md 1.11 / 2.2 / 2.3）=====

// promptViews 批量转响应视图。
func promptViews(list []*model.Prompt, withContent bool) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		out = append(out, item.View(withContent))
	}
	return out
}

// GetPublicPrompts 提示词列表（GET /prompt?keyword=&tag=&category=&source_id=），仅已上架，不返回正文。
func GetPublicPrompts(c *gin.Context) {
	on := model.PromptStatusOn
	filter := model.PromptFilter{
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Tag:      strings.TrimSpace(c.Query("tag")),
		Category: strings.TrimSpace(c.Query("category")),
		SourceId: queryInt(c, "source_id", 0),
		Status:   &on,
	}
	list, total, err := model.GetPrompts(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, promptViews(list, false), total)
}

// GetPublicPromptTags 标签云（GET /prompt/tags）。
func GetPublicPromptTags(c *gin.Context) {
	tags, err := model.GetPromptTags()
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, tags)
}

// GetPublicPrompt 提示词详情（GET /prompt/:id），复制次数 +1，返回正文。
func GetPublicPrompt(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的提示词 ID")
		return
	}
	p, err := model.GetPromptById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if p.Status != model.PromptStatusOn {
		common.ApiErrorMsg(c, "提示词不存在或已下架")
		return
	}
	model.IncreasePromptUses(id)
	p.Uses++
	common.ApiSuccess(c, p.View(true))
}

// GetPublicPromptSources 前台来源筛选项（GET /prompt/sources），只给 id 和名字。
func GetPublicPromptSources(c *gin.Context) {
	list, err := model.GetPromptSources(false)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, item := range list {
		out = append(out, gin.H{"id": item.Id, "name": item.Name, "tag": item.Tag})
	}
	common.ApiSuccess(c, out)
}

// ===== 管理端 =====

type promptPayload struct {
	Title     *string   `json:"title"`
	Content   *string   `json:"content"`
	CoverUrl  *string   `json:"cover_url"`
	ResultUrl *string   `json:"result_url"`
	Tags      *[]string `json:"tags"`
	Category  *string   `json:"category"`
	SortOrder *int      `json:"sort_order"`
	Status    *int      `json:"status"`
}

func (p *promptPayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Title != nil {
		title := trimLimit(*p.Title, model.PromptTitleMax)
		if title == "" {
			return nil, "请填写标题"
		}
		f["title"] = title
	}
	if p.Content != nil {
		content := strings.TrimSpace(*p.Content)
		if content == "" {
			return nil, "请填写提示词正文"
		}
		if len([]rune(content)) > model.PromptContentMax {
			return nil, "提示词正文过长"
		}
		f["content"] = content
	}
	if p.CoverUrl != nil {
		f["cover_url"] = trimLimit(*p.CoverUrl, 1000)
	}
	if p.ResultUrl != nil {
		f["result_url"] = trimLimit(*p.ResultUrl, 1000)
	}
	if p.Tags != nil {
		cleaned := []string{}
		for _, tag := range *p.Tags {
			if v := trimLimit(tag, 30); v != "" {
				cleaned = append(cleaned, v)
			}
		}
		f["tags"] = trimLimit(strings.Join(cleaned, ","), 255)
	}
	if p.Category != nil {
		f["category"] = trimLimit(*p.Category, 50)
	}
	if p.SortOrder != nil {
		f["sort_order"] = *p.SortOrder
	}
	if p.Status != nil {
		if *p.Status != model.PromptStatusOn && *p.Status != model.PromptStatusOff {
			return nil, "无效的状态"
		}
		f["status"] = *p.Status
	}
	return f, ""
}

// AdminGetPrompts 管理端列表（GET /admin/prompt），带正文，可按状态过滤。
func AdminGetPrompts(c *gin.Context) {
	filter := model.PromptFilter{
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Tag:      strings.TrimSpace(c.Query("tag")),
		Category: strings.TrimSpace(c.Query("category")),
		SourceId: queryInt(c, "source_id", 0),
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status := queryInt(c, "status", 0)
		if status != model.PromptStatusOn && status != model.PromptStatusOff {
			common.ApiErrorMsg(c, "无效的状态")
			return
		}
		filter.Status = &status
	}
	list, total, err := model.GetPrompts(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, promptViews(list, true), total)
}

// AdminCreatePrompt 新增提示词（POST /admin/prompt）。
func AdminCreatePrompt(c *gin.Context) {
	var req promptPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Title == nil || req.Content == nil {
		common.ApiErrorMsg(c, "标题和正文必填")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	str := func(k string) string {
		if v, ok := f[k].(string); ok {
			return v
		}
		return ""
	}
	p := &model.Prompt{
		Title:     str("title"),
		Content:   str("content"),
		CoverUrl:  str("cover_url"),
		ResultUrl: str("result_url"),
		Tags:      str("tags"),
		Category:  str("category"),
	}
	if v, ok := f["sort_order"].(int); ok {
		p.SortOrder = v
	}
	if v, ok := f["status"].(int); ok {
		p.Status = v
	}
	if err := p.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	common.ApiSuccess(c, p.View(true))
}

// AdminUpdatePrompt 修改提示词（PUT /admin/prompt/:id）。
func AdminUpdatePrompt(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的提示词 ID")
		return
	}
	var req promptPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if len(f) == 0 {
		common.ApiErrorMsg(c, "没有要更新的字段")
		return
	}
	f["updated_at"] = common.GetTimestamp()
	if err := model.DB.Model(&model.Prompt{}).Where("id = ?", id).Updates(f).Error; err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeletePrompt 删除提示词（DELETE /admin/prompt/:id）。
func AdminDeletePrompt(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的提示词 ID")
		return
	}
	if err := model.DB.Where("id = ?", id).Delete(&model.Prompt{}).Error; err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ===== 管理端：远程源 =====

type promptSourcePayload struct {
	Name    *string `json:"name"`
	Url     *string `json:"url"`
	Format  *string `json:"format"`
	Tag     *string `json:"tag"`
	Enabled *bool   `json:"enabled"`
}

func (p *promptSourcePayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Name != nil {
		name := trimLimit(*p.Name, 100)
		if name == "" {
			return nil, "请填写来源名称"
		}
		f["name"] = name
	}
	if p.Url != nil {
		url := trimLimit(*p.Url, 1000)
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return nil, "抓取地址必须是 http(s) 链接"
		}
		f["url"] = url
	}
	if p.Format != nil {
		if !model.IsValidPromptFormat(*p.Format) {
			return nil, "格式只支持 markdown 或 json"
		}
		f["format"] = *p.Format
	}
	if p.Tag != nil {
		f["tag"] = trimLimit(*p.Tag, 50)
	}
	if p.Enabled != nil {
		f["enabled"] = *p.Enabled
	}
	return f, ""
}

// AdminGetPromptSources 来源列表（GET /admin/prompt-source）。
func AdminGetPromptSources(c *gin.Context) {
	list, err := model.GetPromptSources(false)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, list)
}

// AdminCreatePromptSource 新增来源（POST /admin/prompt-source）。
func AdminCreatePromptSource(c *gin.Context) {
	var req promptSourcePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Name == nil || req.Url == nil {
		common.ApiErrorMsg(c, "名称和抓取地址必填")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	s := &model.PromptSource{Enabled: true}
	if v, ok := f["name"].(string); ok {
		s.Name = v
	}
	if v, ok := f["url"].(string); ok {
		s.Url = v
	}
	if v, ok := f["format"].(string); ok {
		s.Format = v
	}
	if v, ok := f["tag"].(string); ok {
		s.Tag = v
	}
	if v, ok := f["enabled"].(bool); ok {
		s.Enabled = v
	}
	if err := s.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	common.ApiSuccess(c, s)
}

// AdminUpdatePromptSource 修改来源（PUT /admin/prompt-source/:id）。
func AdminUpdatePromptSource(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的来源 ID")
		return
	}
	var req promptSourcePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if len(f) == 0 {
		common.ApiErrorMsg(c, "没有要更新的字段")
		return
	}
	f["updated_at"] = common.GetTimestamp()
	if err := model.DB.Model(&model.PromptSource{}).Where("id = ?", id).Updates(f).Error; err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeletePromptSource 删除来源（DELETE /admin/prompt-source/:id）。
// 已同步进来的提示词保留，只把它们的来源标记清空，免得列表里出现指向空源的筛选项。
func AdminDeletePromptSource(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的来源 ID")
		return
	}
	if err := model.DB.Where("id = ?", id).Delete(&model.PromptSource{}).Error; err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	model.DB.Model(&model.Prompt{}).Where("source_id = ?", id).
		Updates(map[string]any{"source_id": 0, "external_key": ""})
	common.ApiSuccess(c, nil)
}

// AdminSyncPromptSource 手动同步一个来源（POST /admin/prompt-source/:id/sync）。
func AdminSyncPromptSource(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的来源 ID")
		return
	}
	s, err := model.GetPromptSourceById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	total, created, err := service.SyncPromptSource(s)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"total": total, "created": created})
}
