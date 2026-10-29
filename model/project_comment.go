package model

import (
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// ProjectComment 项目评论批注（F7，API.md 10.10）：团队成员在项目或画布节点上留言。
// node_id 为空是项目级留言，非空是画布节点批注。
type ProjectComment struct {
	Id         int    `json:"id"`
	ProjectId  int    `json:"project_id" gorm:"index;comment:所属项目"`
	UserId     int    `json:"user_id" gorm:"index;comment:评论人"`
	CanvasId   int    `json:"canvas_id" gorm:"default:0;comment:0 表示项目级"`
	NodeId     string `json:"node_id" gorm:"type:varchar(40);default:'';comment:画布节点 id，空表示不针对节点"`
	Content    string `json:"content" gorm:"type:varchar(1000);comment:正文"`
	Resolved   int    `json:"resolved" gorm:"index;default:0;comment:1=已解决"`
	ResolvedBy int    `json:"resolved_by" gorm:"default:0"`
	ResolvedAt int64  `json:"resolved_at" gorm:"bigint;default:0"`

	CreatedAt int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	// 非表字段
	AuthorNickname   string `json:"author_nickname,omitempty" gorm:"-"`
	ResolvedNickname string `json:"resolved_by_nickname,omitempty" gorm:"-"`
}

func (ProjectComment) TableName() string { return "project_comment" }

// ProjectCommentMaxLen 正文长度上限（字符数，按 rune 计）。
const ProjectCommentMaxLen = 1000

func (pc *ProjectComment) Insert() error {
	now := common.GetTimestamp()
	pc.CreatedAt = now
	pc.UpdatedAt = now
	return DB.Create(pc).Error
}

// ProjectCommentFilter 列表过滤。
type ProjectCommentFilter struct {
	ProjectId int
	// CanvasId > 0 时只取该画布
	CanvasId int
	// NodeId 非空时只取该节点
	NodeId string
	// OnlyOpen 只取未解决
	OnlyOpen bool
}

// GetProjectComments 未解决的排在前面，其次按时间倒序。
func GetProjectComments(filter ProjectCommentFilter, pageInfo *common.PageInfo) (list []*ProjectComment, total int64, err error) {
	query := DB.Model(&ProjectComment{}).Where("project_id = ?", filter.ProjectId)
	if filter.CanvasId > 0 {
		query = query.Where("canvas_id = ?", filter.CanvasId)
	}
	if filter.NodeId != "" {
		query = query.Where("node_id = ?", filter.NodeId)
	}
	if filter.OnlyOpen {
		query = query.Where("resolved = 0")
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("resolved asc, id desc").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// CountOpenProjectComments 项目未解决批注数，用于项目列表角标。
func CountOpenProjectComments(projectId int) int64 {
	var n int64
	DB.Model(&ProjectComment{}).Where("project_id = ? AND resolved = 0", projectId).Count(&n)
	return n
}

// GetProjectCommentById 单条。
func GetProjectCommentById(id int) (*ProjectComment, error) {
	pc := &ProjectComment{}
	if err := DB.Where("id = ?", id).First(pc).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return pc, nil
}

// SetProjectCommentResolved 标记解决 / 重新打开。
func SetProjectCommentResolved(id, byUserId int, resolved bool) error {
	f := map[string]any{"updated_at": common.GetTimestamp()}
	if resolved {
		f["resolved"] = 1
		f["resolved_by"] = byUserId
		f["resolved_at"] = common.GetTimestamp()
	} else {
		f["resolved"] = 0
		f["resolved_by"] = 0
		f["resolved_at"] = 0
	}
	return updateOne(DB.Model(&ProjectComment{}).Where("id = ?", id), f)
}

// DeleteProjectComment 软删。
func DeleteProjectComment(id int) error {
	return deleteOne(DB.Where("id = ?", id), &ProjectComment{})
}

// AttachProjectCommentUsers 填充评论人与解决人昵称。
func AttachProjectCommentUsers(list []*ProjectComment) {
	ids := []int{}
	seen := map[int]bool{}
	add := func(id int) {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, pc := range list {
		add(pc.UserId)
		add(pc.ResolvedBy)
	}
	if len(ids) == 0 {
		return
	}
	type row struct {
		Id       int
		Nickname string
		Username string
	}
	rows := []row{}
	DB.Model(&User{}).Select("id, nickname, username").Where("id IN ?", ids).Find(&rows)
	names := map[int]string{}
	for _, r := range rows {
		name := strings.TrimSpace(r.Nickname)
		if name == "" {
			name = r.Username
		}
		names[r.Id] = name
	}
	for _, pc := range list {
		pc.AuthorNickname = names[pc.UserId]
		pc.ResolvedNickname = names[pc.ResolvedBy]
	}
}
