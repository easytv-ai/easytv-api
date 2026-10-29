package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 内容分类：公开 + 管理端（API.md 1.7 / 2.2 / 2.3） =====

// GetPublicCategories 前台分类（GET /category?scope=work|skill），仅启用，按 sort_order 升序。
func GetPublicCategories(c *gin.Context) {
	scope := strings.TrimSpace(c.Query("scope"))
	if scope == "" {
		scope = model.CategoryScopeWork
	}
	if !model.IsValidCategoryScope(scope) {
		common.ApiErrorMsg(c, "无效的范围")
		return
	}
	list, err := model.GetActiveCategories(scope)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, list)
}

type categoryPayload struct {
	Name      *string `json:"name"`
	Scope     *string `json:"scope"`
	SortOrder *int    `json:"sort_order"`
	Status    *int    `json:"status"`
}

func (p *categoryPayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Name != nil {
		name := trimLimit(*p.Name, 50)
		if name == "" {
			return nil, "请填写分类名称"
		}
		f["name"] = name
	}
	if p.Scope != nil {
		if !model.IsValidCategoryScope(*p.Scope) {
			return nil, "无效的范围"
		}
		f["scope"] = *p.Scope
	}
	if p.SortOrder != nil {
		f["sort_order"] = *p.SortOrder
	}
	if p.Status != nil {
		if *p.Status != model.ContentStatusOn && *p.Status != model.ContentStatusOff {
			return nil, "无效的状态"
		}
		f["status"] = *p.Status
	}
	return f, ""
}

// AdminGetCategories 分类列表（GET /admin/category?scope=&keyword=&status=），不分页。
func AdminGetCategories(c *gin.Context) {
	scope := strings.TrimSpace(c.Query("scope"))
	if scope != "" && !model.IsValidCategoryScope(scope) {
		common.ApiErrorMsg(c, "无效的范围")
		return
	}
	status, ok := statusFilter(c)
	if !ok {
		return
	}
	list, err := model.GetCategories(scope, strings.TrimSpace(c.Query("keyword")), status)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, int64(len(list)))
}

// AdminCreateCategory 新建分类（POST /admin/category）。
func AdminCreateCategory(c *gin.Context) {
	var req categoryPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Name == nil || req.Scope == nil {
		common.ApiErrorMsg(c, "请填写分类名称与范围")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	name, _ := f["name"].(string)
	scope, _ := f["scope"].(string)
	if model.IsCategoryNameTaken(scope, name, 0) {
		common.ApiErrorMsg(c, "同一范围内分类名称已存在")
		return
	}
	cat := &model.Category{Name: name, Scope: scope}
	if v, ok := f["sort_order"].(int); ok {
		cat.SortOrder = v
	}
	if v, ok := f["status"].(int); ok {
		cat.Status = v
	}
	if err := cat.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	common.ApiSuccess(c, cat)
}

// AdminUpdateCategory 修改分类（PUT /admin/category/:id），含启停。
func AdminUpdateCategory(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的分类 ID")
		return
	}
	var req categoryPayload
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
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	current, err := model.GetCategoryById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	name, scope := current.Name, current.Scope
	if v, ok := f["name"].(string); ok {
		name = v
	}
	if v, ok := f["scope"].(string); ok {
		scope = v
	}
	if model.IsCategoryNameTaken(scope, name, id) {
		common.ApiErrorMsg(c, "同一范围内分类名称已存在")
		return
	}
	if err := model.UpdateCategoryFields(id, f); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteCategory 删除分类（DELETE /admin/category/:id）。不影响已使用该分类的作品 / 技能。
func AdminDeleteCategory(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的分类 ID")
		return
	}
	if err := model.DeleteCategory(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}
