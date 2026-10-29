package model

import (
	"encoding/json"
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Skill 技能模板：Agent 页 / 画布对话面板展示的创作模板，由运营后台维护。
type Skill struct {
	Id          int    `json:"id" gorm:"comment:技能ID"`
	UserId      int    `json:"user_id" gorm:"index;default:0;comment:0 官方；>0 用户私有 Skill"`
	Title       string `json:"title" gorm:"type:varchar(100);comment:名称"`
	Slug        string `json:"slug" gorm:"type:varchar(100);default:'';index;comment:路径标识"`
	Description string `json:"description" gorm:"type:varchar(500);default:'';comment:一句话描述"`
	AuthorName  string `json:"author_name" gorm:"type:varchar(100);default:'';comment:作者展示名"`
	Category    string `json:"category" gorm:"type:varchar(50);default:'';index;comment:分类（专业影视/商业广告/…）"`
	CoverUrl    string `json:"cover_url" gorm:"type:varchar(1000);default:'';comment:封面"`
	VideoUrl    string `json:"video_url" gorm:"type:varchar(1000);default:'';comment:示例视频"`
	Scene       string `json:"scene" gorm:"type:varchar(200);default:'';comment:适用场景"`
	Howto       string `json:"howto" gorm:"type:varchar(500);default:'';comment:输入说明"`
	Output      string `json:"output" gorm:"type:varchar(500);default:'';comment:产出说明"`
	Prompt      string `json:"prompt" gorm:"type:text;comment:提示词模板"`
	// 执行契约：Skill 不只是一段提示词，还能规定 Agent 按什么步骤做、产出长什么样。
	// 只有文本任务会注入，图片 / 视频的提示词直接进模型，混进执行规则只会污染画面。
	Guide        string `json:"guide" gorm:"type:text;comment:执行规则，文本任务作为前置指令注入"`
	OutputFormat string `json:"output_format" gorm:"type:text;comment:输出格式说明，随执行规则一起注入"`
	Uses         int    `json:"uses" gorm:"default:0;comment:使用次数"`
	Stars        int    `json:"stars" gorm:"default:0;comment:收藏数"`
	SortOrder    int    `json:"sort_order" gorm:"default:0;index;comment:排序，越大越前"`
	Status       int    `json:"status" gorm:"index;default:1;comment:1=已上架 2=已下架"`
	Graph        string `json:"-" gorm:"type:longtext;comment:画布模板 JSON（从画布存为 Skill 时写入，已去掉任务与资产 ID）"`
	GraphNodes   int    `json:"graph_nodes" gorm:"default:0;comment:画布模板节点数，0 表示只有提示词"`
	// 团队共享（F7，API.md 10.10）：shared=1 时 team_id 为作者当时所在团队
	TeamId int `json:"team_id" gorm:"index;default:0;comment:共享给哪个团队"`
	Shared int `json:"shared" gorm:"default:0;comment:1=共享给团队"`
	// 上架审核与分成（T26）；官方技能 review_status 恒为 2
	ReviewStatus int            `json:"review_status" gorm:"index;default:0;comment:0 私有 1 待审 2 已上架 3 驳回"`
	ReviewRemark string         `json:"review_remark" gorm:"type:varchar(255);default:''"`
	SubmittedAt  int64          `json:"submitted_at" gorm:"bigint;default:0"`
	PublishedAt  int64          `json:"published_at" gorm:"bigint;default:0"`
	RevenueShare float64        `json:"revenue_share" gorm:"type:decimal(4,2);default:0;comment:作者分成比例，0 用全局 GrowthSkillAuthorShare"`
	CallsTotal   int            `json:"calls_total" gorm:"default:0;comment:被调用产生分成的次数"`
	RevenueTotal int            `json:"revenue_total" gorm:"default:0;comment:累计分成积分"`
	CreatedAt    int64          `json:"created_at" gorm:"bigint;comment:创建时间（Unix 秒）"`
	UpdatedAt    int64          `json:"updated_at" gorm:"bigint;comment:更新时间（Unix 秒）"`
	DeletedAt    gorm.DeletedAt `json:"deleted_at" gorm:"index;comment:删除时间"`

	// Favorited 当前登录用户是否已收藏（非表字段）
	Favorited bool `json:"favorited" gorm:"-"`
	// GraphView 详情接口附带的画布模板（列表不返回）
	GraphView json.RawMessage `json:"graph,omitempty" gorm:"-"`
	// AuthorNickname 后台审核列表附带（非表字段）
	AuthorNickname string `json:"author_nickname,omitempty" gorm:"-"`
	// TeamShared 该行是团队成员共享给我的（非表字段）：前端据此隐藏编辑与删除
	TeamShared bool `json:"team_shared,omitempty" gorm:"-"`
}

// AttachSkillAuthors 填充用户技能的作者昵称。
func AttachSkillAuthors(list []*Skill) {
	ids := []int{}
	for _, s := range list {
		if s.UserId > 0 {
			ids = append(ids, s.UserId)
		}
	}
	if len(ids) == 0 {
		return
	}
	names := GetUserNicknames(ids)
	for _, s := range list {
		if s.UserId > 0 {
			s.AuthorNickname = names[s.UserId]
		}
	}
}

const (
	SkillReviewPrivate   = 0
	SkillReviewPending   = 1
	SkillReviewPublished = 2
	SkillReviewRejected  = 3
)

// PublicVisible 官方技能或已上架的用户技能。
func (s *Skill) PublicVisible() bool {
	return s.Status == SkillStatusOn && (s.UserId == SkillUserOfficial || s.ReviewStatus == SkillReviewPublished)
}

// IncreaseSkillRevenue 分成入账后累加调用数与收益。
func IncreaseSkillRevenue(id, points int) {
	DB.Model(&Skill{}).Where("id = ?", id).Updates(map[string]any{
		"calls_total":   gorm.Expr("calls_total + 1"),
		"revenue_total": gorm.Expr("revenue_total + ?", points),
	})
}

// WithGraph 详情接口把画布模板放进响应。
func (s *Skill) WithGraph() *Skill {
	if s.Graph != "" {
		s.GraphView = json.RawMessage(s.Graph)
	}
	return s
}

func (Skill) TableName() string { return "skill" }

const (
	SkillStatusOn  = 1
	SkillStatusOff = 2

	// SkillUserOfficial 官方 Skill 的所有者标识：0 官方；>0 用户私有
	SkillUserOfficial = 0
	// SkillUserAllPrivate SkillFilter.UserId 过滤哨兵：取所有用户私有（user_id > 0）
	SkillUserAllPrivate = -1
)

// Insert 创建技能。
func (s *Skill) Insert() error {
	now := common.GetTimestamp()
	s.CreatedAt = now
	s.UpdatedAt = now
	if s.Status == 0 {
		s.Status = SkillStatusOn
	}
	return DB.Create(s).Error
}

// GetSkillById 按 ID 查询。
func GetSkillById(id int) (*Skill, error) {
	s := &Skill{}
	if err := DB.Where("id = ?", id).First(s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s, nil
}

// SkillFilter 技能列表过滤条件。
type SkillFilter struct {
	// ReviewStatus 按审核状态过滤；PublicVisible 只取官方或已上架的用户技能
	ReviewStatus  *int
	PublicVisible bool
	Category      string
	Keyword       string
	Status        *int
	// UserId 非 nil 时按所有者过滤：0 只取官方；-1 取所有用户私有（user_id > 0）；正数只取该用户的私有 Skill
	UserId *int
	// TeamId > 0 且 UserId 为正数时，额外取该团队里其他成员共享的技能（F7，API.md 10.10）
	TeamId int
}

// GetSkills 技能列表；公开接口传 Status=1。
func GetSkills(filter SkillFilter, pageInfo *common.PageInfo) (list []*Skill, total int64, err error) {
	query := DB.Model(&Skill{})
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.UserId != nil {
		switch {
		case *filter.UserId == SkillUserAllPrivate:
			query = query.Where("user_id > 0")
		case filter.TeamId > 0 && *filter.UserId > 0:
			// 我的 + 同团队成员已共享的（共享方退团后 team_id 不再匹配，自动失效）
			query = query.Where("(user_id = ? OR (shared = 1 AND team_id = ? AND user_id <> ?))",
				*filter.UserId, filter.TeamId, *filter.UserId)
		default:
			query = query.Where("user_id = ?", *filter.UserId)
		}
	}
	if filter.ReviewStatus != nil {
		query = query.Where("review_status = ?", *filter.ReviewStatus)
	}
	if filter.PublicVisible {
		query = query.Where("(user_id = 0 OR review_status = ?)", SkillReviewPublished)
	}
	if filter.Category != "" {
		query = query.Where("category = ?", filter.Category)
	}
	if filter.Keyword != "" {
		like := "%" + filter.Keyword + "%"
		query = query.Where("(title LIKE ? OR description LIKE ? OR author_name LIKE ?)", like, like, like)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("sort_order desc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// UpdateSkillFields 更新字段。
func UpdateSkillFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	result := DB.Model(&Skill{}).Where("id = ?", id).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSkill 软删除。
func DeleteSkill(id int) error {
	result := DB.Where("id = ?", id).Delete(&Skill{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// IncreaseSkillUses 使用次数 +1。
func IncreaseSkillUses(id int) {
	DB.Model(&Skill{}).Where("id = ?", id).Update("uses", gorm.Expr("uses + 1"))
}

// GetUserSkill 用户私有 Skill。
func GetUserSkill(userId, id int) (*Skill, error) {
	s := &Skill{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(s).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return s, nil
}

// UpdateUserSkillFields 用户更新自己的 Skill。
func UpdateUserSkillFields(userId, id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return updateOne(DB.Model(&Skill{}).Where("id = ? AND user_id = ?", id, userId), fields)
}

// DeleteUserSkill 用户删除自己的 Skill（软删）。
func DeleteUserSkill(userId, id int) error {
	return deleteOne(DB.Where("id = ? AND user_id = ?", id, userId), &Skill{})
}
