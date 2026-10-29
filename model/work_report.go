package model

import (
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
)

// ===== 作品举报（社区审核，见 API.md 1.8） =====

const (
	ReportStatusPending  = 0 // 待审核
	ReportStatusUpheld   = 1 // 举报成立（作品已下架）
	ReportStatusRejected = 2 // 驳回
)

const (
	ReportTargetWork    = "work"
	ReportTargetComment = "comment"
)

// WorkReport 用户对已发布作品或评论的举报（target_type 区分，评论举报也带 work_id 便于定位）。
type WorkReport struct {
	Id         int    `json:"id" gorm:"comment:举报ID"`
	TargetType string `json:"target_type" gorm:"type:varchar(20);default:'work';index;comment:work 作品 / comment 评论"`
	CommentId  int    `json:"comment_id" gorm:"default:0;index;comment:被举报评论ID（target_type=comment）"`
	WorkId     int    `json:"work_id" gorm:"index;comment:作品ID"`
	UserId     int    `json:"user_id" gorm:"index;comment:举报人"`
	Reason     string `json:"reason" gorm:"type:varchar(50);comment:原因（违规内容 / 侵权 / 垃圾信息 / 其他）"`
	Content    string `json:"content" gorm:"type:varchar(500);default:'';comment:补充说明"`
	Status     int    `json:"status" gorm:"index;default:0;comment:0 待审核 1 举报成立 2 驳回"`
	Remark     string `json:"remark" gorm:"type:varchar(255);default:'';comment:处理备注"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt  int64  `json:"updated_at" gorm:"bigint"`

	// 列表附带（非表字段）
	WorkTitle      string `json:"work_title" gorm:"-"`
	ReporterName   string `json:"reporter_name" gorm:"-"`
	CommentContent string `json:"comment_content,omitempty" gorm:"-"`
}

func (WorkReport) TableName() string { return "work_report" }

// IsValidReportStatus 校验处理结果。
func IsValidReportStatus(s int) bool {
	return s == ReportStatusUpheld || s == ReportStatusRejected
}

// CreateWorkReport 举报已发布作品；同一用户对同一作品存在待审核记录时直接返回该记录。
func CreateWorkReport(userId, workId int, reason, content string) (*WorkReport, error) {
	var w Work
	if err := DB.Where("id = ? AND status = ?", workId, WorkStatusPublished).First(&w).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	existing := &WorkReport{}
	err := DB.Where("user_id = ? AND work_id = ? AND status = ?", userId, workId, ReportStatusPending).First(existing).Error
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	now := common.GetTimestamp()
	r := &WorkReport{
		TargetType: ReportTargetWork,
		WorkId:     workId,
		UserId:     userId,
		Reason:     reason,
		Content:    content,
		Status:     ReportStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := DB.Create(r).Error; err != nil {
		return nil, err
	}
	return r, nil
}

// CreateCommentReport 举报评论；同一用户对同一评论只保留一条待审核记录。
func CreateCommentReport(userId, commentId int, reason, content string) (*WorkReport, error) {
	var cm WorkComment
	if err := DB.Where("id = ? AND status = ?", commentId, CommentStatusNormal).First(&cm).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	existing := &WorkReport{}
	err := DB.Where("user_id = ? AND comment_id = ? AND status = ?", userId, commentId, ReportStatusPending).First(existing).Error
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	now := common.GetTimestamp()
	r := &WorkReport{
		TargetType: ReportTargetComment,
		CommentId:  commentId,
		WorkId:     cm.WorkId,
		UserId:     userId,
		Reason:     reason,
		Content:    content,
		Status:     ReportStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := DB.Create(r).Error; err != nil {
		return nil, err
	}
	return r, nil
}

// GetWorkReportById 单条举报。
func GetWorkReportById(id int) (*WorkReport, error) {
	r := &WorkReport{}
	if err := DB.Where("id = ?", id).First(r).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return r, nil
}

// GetWorkReports 管理端列表；keyword 匹配作品标题或说明；targetType 为空查全部。
func GetWorkReports(status *int, targetType, keyword string, pageInfo *common.PageInfo) (list []*WorkReport, total int64, err error) {
	query := DB.Model(&WorkReport{})
	if status != nil {
		query = query.Where("status = ?", *status)
	}
	if targetType != "" {
		query = query.Where("target_type = ?", targetType)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("content LIKE ? OR reason LIKE ? OR work_id IN (?)", like, like,
			DB.Unscoped().Model(&Work{}).Select("id").Where("title LIKE ?", like))
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("status asc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	attachReportRefs(list)
	return list, total, nil
}

func attachReportRefs(list []*WorkReport) {
	if len(list) == 0 {
		return
	}
	workIds := make([]int, 0, len(list))
	userIds := make([]int, 0, len(list))
	for _, r := range list {
		workIds = append(workIds, r.WorkId)
		userIds = append(userIds, r.UserId)
	}
	var works []struct {
		Id    int
		Title string
	}
	DB.Unscoped().Model(&Work{}).Select("id, title").Where("id IN ?", workIds).Scan(&works)
	titles := map[int]string{}
	for _, w := range works {
		titles[w.Id] = w.Title
	}
	names := GetUserNicknames(userIds)
	commentIds := make([]int, 0)
	for _, r := range list {
		r.WorkTitle = titles[r.WorkId]
		r.ReporterName = names[r.UserId]
		if r.CommentId > 0 {
			commentIds = append(commentIds, r.CommentId)
		}
	}
	if len(commentIds) > 0 {
		var comments []struct {
			Id      int
			Content string
		}
		DB.Unscoped().Model(&WorkComment{}).Select("id, content").Where("id IN ?", commentIds).Scan(&comments)
		contents := map[int]string{}
		for _, c := range comments {
			contents[c.Id] = c.Content
		}
		for _, r := range list {
			if r.CommentId > 0 {
				r.CommentContent = contents[r.CommentId]
			}
		}
	}
}

// SetWorkReportStatus 处理举报：成立则同时下架作品（status=2），驳回只改举报状态。
func SetWorkReportStatus(id, status int, remark string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		r := &WorkReport{}
		if err := tx.Where("id = ?", id).First(r).Error; err != nil {
			return wrapNotFound(err)
		}
		if r.Status != ReportStatusPending {
			return errors.New("该举报已处理")
		}
		now := common.GetTimestamp()
		if err := tx.Model(r).Updates(map[string]any{"status": status, "remark": remark, "updated_at": now}).Error; err != nil {
			return err
		}
		if status == ReportStatusUpheld && r.TargetType == ReportTargetComment {
			// 评论举报成立：隐藏评论，同一评论的其他待审核举报一并结案
			if err := tx.Model(&WorkComment{}).Where("id = ?", r.CommentId).
				Updates(map[string]any{"status": CommentStatusHidden, "remark": "举报成立：" + r.Reason, "updated_at": now}).Error; err != nil {
				return err
			}
			var cm WorkComment
			if err := tx.Where("id = ?", r.CommentId).First(&cm).Error; err == nil {
				if err := recountReplies(tx, cm.ParentId); err != nil {
					return err
				}
				if err := recountWorkComments(tx, cm.WorkId); err != nil {
					return err
				}
			}
			return tx.Model(&WorkReport{}).Where("comment_id = ? AND status = ? AND id <> ?", r.CommentId, ReportStatusPending, r.Id).
				Updates(map[string]any{"status": ReportStatusUpheld, "remark": "合并处理", "updated_at": now}).Error
		}
		if status == ReportStatusUpheld {
			if err := tx.Model(&Work{}).Where("id = ? AND status = ?", r.WorkId, WorkStatusPublished).
				Updates(map[string]any{"status": WorkStatusUnlisted, "remark": "举报成立：" + r.Reason, "updated_at": now}).Error; err != nil {
				return err
			}
			// 同一作品的其他待审核作品举报一并结案
			if err := tx.Model(&WorkReport{}).Where("work_id = ? AND target_type = ? AND status = ? AND id <> ?", r.WorkId, ReportTargetWork, ReportStatusPending, r.Id).
				Updates(map[string]any{"status": ReportStatusUpheld, "remark": "合并处理", "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func wrapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
