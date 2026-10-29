package model

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// ===== 提示词库（运营维护 + 远程源同步，见 API.md 1.11）=====

// Prompt 一条可直接复制使用的提示词。
type Prompt struct {
	Id        int    `json:"id" gorm:"comment:提示词ID"`
	Title     string `json:"title" gorm:"type:varchar(200);index;comment:标题"`
	Content   string `json:"content" gorm:"type:text;comment:提示词正文"`
	CoverUrl  string `json:"cover_url" gorm:"type:varchar(1000);default:'';comment:封面图"`
	ResultUrl string `json:"result_url" gorm:"type:varchar(1000);default:'';comment:效果图"`
	// Tags 逗号分隔；前台按单个标签筛选，用 LIKE 匹配
	Tags     string `json:"tags" gorm:"type:varchar(255);default:'';comment:标签，逗号分隔"`
	Category string `json:"category" gorm:"type:varchar(50);default:'';index;comment:分类"`
	// SourceId 0 表示后台手工录入；>0 关联 prompt_source
	SourceId   int    `json:"source_id" gorm:"index;default:0;comment:来源ID，0 为手工录入"`
	SourceName string `json:"source_name" gorm:"type:varchar(100);default:'';comment:来源展示名"`
	SourceUrl  string `json:"source_url" gorm:"type:varchar(1000);default:'';comment:原文链接"`
	// ExternalKey 同步去重键（来源 + 标题的指纹）。手工录入留空，
	// 留空的行不参与唯一约束，否则多条手工提示词会互相顶掉。
	ExternalKey string         `json:"-" gorm:"type:varchar(64);index;default:'';comment:远程源去重键"`
	Uses        int            `json:"uses" gorm:"default:0;comment:复制次数"`
	SortOrder   int            `json:"sort_order" gorm:"default:0;index;comment:越大越靠前"`
	Status      int            `json:"status" gorm:"index;default:1;comment:1 已上架 2 已下架"`
	CreatedAt   int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt   int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

func (Prompt) TableName() string { return "prompt" }

const (
	PromptStatusOn  = 1
	PromptStatusOff = 2

	// PromptTitleMax / PromptContentMax 入库前的截断长度，远程源里偶尔有超长条目
	PromptTitleMax   = 200
	PromptContentMax = 8000
)

// TagList 拆成数组给前端。
func (p *Prompt) TagList() []string {
	out := []string{}
	for _, item := range strings.Split(p.Tags, ",") {
		if v := strings.TrimSpace(item); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// View 列表 / 详情响应。列表不带正文，避免一页几十条把响应撑大。
func (p *Prompt) View(withContent bool) map[string]any {
	view := map[string]any{
		"id":          p.Id,
		"title":       p.Title,
		"cover_url":   p.CoverUrl,
		"result_url":  p.ResultUrl,
		"tags":        p.TagList(),
		"category":    p.Category,
		"source_id":   p.SourceId,
		"source_name": p.SourceName,
		"source_url":  p.SourceUrl,
		"uses":        p.Uses,
		"sort_order":  p.SortOrder,
		"status":      p.Status,
		"created_at":  p.CreatedAt,
		"updated_at":  p.UpdatedAt,
	}
	if withContent {
		view["content"] = p.Content
	}
	return view
}

// PromptExternalKey 远程源条目的去重键：同一个源里标题相同就视为同一条。
func PromptExternalKey(sourceId int, title string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(strings.ToLower(title))))
	return strconv.Itoa(sourceId) + ":" + hex.EncodeToString(sum[:])[:24]
}

func (p *Prompt) Insert() error {
	now := common.GetTimestamp()
	p.CreatedAt, p.UpdatedAt = now, now
	if p.Status == 0 {
		p.Status = PromptStatusOn
	}
	return DB.Create(p).Error
}

// GetPromptById 按 ID 查询。
func GetPromptById(id int) (*Prompt, error) {
	p := &Prompt{}
	if err := DB.Where("id = ?", id).First(p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// PromptFilter 列表过滤条件。
type PromptFilter struct {
	Keyword  string
	Tag      string
	Category string
	SourceId int
	// Status 为 nil 查全部（管理端），前台固定传已上架
	Status *int
}

// GetPrompts 分页列表，按 sort_order 降序、id 降序。
func GetPrompts(f PromptFilter, page *common.PageInfo) (list []*Prompt, total int64, err error) {
	query := DB.Model(&Prompt{})
	if f.Keyword != "" {
		like := "%" + f.Keyword + "%"
		query = query.Where("title LIKE ? OR content LIKE ?", like, like)
	}
	if f.Tag != "" {
		query = query.Where("tags LIKE ?", "%"+f.Tag+"%")
	}
	if f.Category != "" {
		query = query.Where("category = ?", f.Category)
	}
	if f.SourceId > 0 {
		query = query.Where("source_id = ?", f.SourceId)
	}
	if f.Status != nil {
		query = query.Where("status = ?", *f.Status)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("sort_order desc, id desc").
		Offset(page.GetStartIdx()).Limit(page.GetPageSize()).Find(&list).Error
	return list, total, err
}

// GetPromptTags 前台标签云：取已上架提示词的标签并去重。
func GetPromptTags() ([]string, error) {
	var rows []string
	if err := DB.Model(&Prompt{}).Where("status = ? AND tags <> ''", PromptStatusOn).
		Pluck("tags", &rows).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, row := range rows {
		for _, tag := range strings.Split(row, ",") {
			v := strings.TrimSpace(tag)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}

// IncreasePromptUses 复制提示词时 +1。
func IncreasePromptUses(id int) {
	DB.Model(&Prompt{}).Where("id = ?", id).Update("uses", gorm.Expr("uses + 1"))
}

// UpsertSourcePrompt 远程源同步：按 external_key 存在则更新正文，不存在则插入。
// 返回 true 表示这条是新增的。运营改过的标题 / 封面不覆盖，只更新正文与原文链接。
func UpsertSourcePrompt(p *Prompt) (created bool, err error) {
	if p.ExternalKey == "" {
		return false, errors.New("缺少去重键")
	}
	existing := &Prompt{}
	err = DB.Where("external_key = ?", p.ExternalKey).First(existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, p.Insert()
	}
	if err != nil {
		return false, err
	}
	return false, DB.Model(existing).Updates(map[string]any{
		"content":     p.Content,
		"source_url":  p.SourceUrl,
		"source_name": p.SourceName,
		"updated_at":  common.GetTimestamp(),
	}).Error
}

// ===== 远程提示词源 =====

// PromptSource 一个可定时抓取的提示词来源（GitHub raw 地址等）。
type PromptSource struct {
	Id   int    `json:"id" gorm:"comment:来源ID"`
	Name string `json:"name" gorm:"type:varchar(100);comment:展示名"`
	// Url 直接存完整地址，不限定 GitHub：raw.githubusercontent.com 的 md / json 都能抓
	Url string `json:"url" gorm:"type:varchar(1000);comment:抓取地址"`
	// Format markdown 按二级标题 + 代码块解析；json 期望一个对象数组
	Format string `json:"format" gorm:"type:varchar(20);default:'markdown';comment:markdown / json"`
	// Tag 同步进来的提示词统一打上这个标签，方便前台按来源筛
	Tag          string `json:"tag" gorm:"type:varchar(50);default:'';comment:同步条目统一标签"`
	Enabled      bool   `json:"enabled" gorm:"default:true;comment:是否参与定时同步"`
	LastSyncedAt int64  `json:"last_synced_at" gorm:"bigint;default:0;comment:上次同步时间"`
	LastError    string `json:"last_error" gorm:"type:varchar(500);default:'';comment:上次失败原因"`
	Count        int    `json:"count" gorm:"default:0;comment:上次同步拿到的条目数"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt    int64  `json:"updated_at" gorm:"bigint"`
}

func (PromptSource) TableName() string { return "prompt_source" }

const (
	PromptFormatMarkdown = "markdown"
	PromptFormatJSON     = "json"
)

// IsValidPromptFormat 校验解析格式。
func IsValidPromptFormat(f string) bool {
	return f == PromptFormatMarkdown || f == PromptFormatJSON
}

func (s *PromptSource) Insert() error {
	now := common.GetTimestamp()
	s.CreatedAt, s.UpdatedAt = now, now
	if s.Format == "" {
		s.Format = PromptFormatMarkdown
	}
	return DB.Create(s).Error
}

// GetPromptSourceById 按 ID 查询。
func GetPromptSourceById(id int) (*PromptSource, error) {
	s := &PromptSource{}
	if err := DB.Where("id = ?", id).First(s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s, nil
}

// GetPromptSources 管理端列表；enabledOnly 供定时任务取待同步的源。
func GetPromptSources(enabledOnly bool) (list []*PromptSource, err error) {
	query := DB.Model(&PromptSource{})
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	err = query.Order("id asc").Find(&list).Error
	return list, err
}

// MarkPromptSourceSynced 回写同步结果。
func MarkPromptSourceSynced(id, count int, errMsg string) {
	DB.Model(&PromptSource{}).Where("id = ?", id).Updates(map[string]any{
		"last_synced_at": common.GetTimestamp(),
		"last_error":     trimRunes(errMsg, 500),
		"count":          count,
		"updated_at":     common.GetTimestamp(),
	})
}

// trimRunes 按字符截断，避免多字节字符被切一半。
func trimRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
