package model

import (
	"encoding/json"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// CanvasWorkflow 工作流模板（API.md 31，L3）：把画布上打好组的一串节点连同连线存下来，
// 之后能整套发回任意画布。graph 就是前端的节点 / 连线快照，后端只做大小与归属校验，不解释内容。
type CanvasWorkflow struct {
	Id        int            `json:"id"`
	UserId    int            `json:"user_id" gorm:"index"`
	TeamId    int            `json:"team_id" gorm:"index;default:0"`
	Name      string         `json:"name" gorm:"type:varchar(50)"`
	CoverUrl  string         `json:"cover_url" gorm:"type:varchar(1000);default:''"`
	NodeCount int            `json:"node_count" gorm:"default:0"`
	EdgeCount int            `json:"edge_count" gorm:"default:0"`
	Graph     string         `json:"-" gorm:"type:mediumtext;comment:{nodes:[],edges:[]}"`
	Uses      int            `json:"uses" gorm:"default:0"`
	CreatedAt int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	// GraphView 只有详情接口填，列表不带，省得把整张图塞进列表响应
	GraphView json.RawMessage `json:"graph,omitempty" gorm:"-"`
	Mine      bool            `json:"mine" gorm:"-"`
}

func (CanvasWorkflow) TableName() string { return "canvas_workflow" }

// CanvasWorkflowGraphMax graph 的字节上限，超过按「工作流太大」回绝（mediumtext 放得下，但没必要）
const CanvasWorkflowGraphMax = 512 * 1024

// CanvasWorkflowListFields 列表不取 graph
var CanvasWorkflowListFields = []string{
	"id", "user_id", "team_id", "name", "cover_url",
	"node_count", "edge_count", "uses", "created_at", "updated_at",
}

func (w *CanvasWorkflow) FillView(viewerId int, withGraph bool) {
	w.Mine = w.UserId == viewerId
	if withGraph && w.Graph != "" {
		w.GraphView = json.RawMessage(w.Graph)
	}
}

func (w *CanvasWorkflow) Insert() error {
	now := common.GetTimestamp()
	w.CreatedAt, w.UpdatedAt = now, now
	if w.Graph == "" {
		w.Graph = "{}"
	}
	return DB.Create(w).Error
}

func (w *CanvasWorkflow) Update(fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return DB.Model(&CanvasWorkflow{}).Where("id = ?", w.Id).Updates(fields).Error
}

type CanvasWorkflowFilter struct {
	UserId  int
	TeamId  int
	Keyword string
	// WithGraph 连 graph 一起取。列表接口不要（响应会被整张图撑爆），
	// 但 agent 的模板检索（API.md 32.6）要拿 graph 给模型抄，不取就只剩一句「几个节点几条连线」。
	WithGraph bool
}

// GetCanvasWorkflows 个人空间只列自己 team_id=0 的；团队空间列该团队的全部（口径同主体库，API.md 20）。
func GetCanvasWorkflows(f CanvasWorkflowFilter, pageInfo *common.PageInfo) (list []*CanvasWorkflow, total int64, err error) {
	q := DB.Model(&CanvasWorkflow{})
	if f.TeamId > 0 {
		q = q.Where("team_id = ?", f.TeamId)
	} else {
		q = q.Where("user_id = ? AND team_id = 0", f.UserId)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		q = q.Where("name LIKE ?", "%"+kw+"%")
	}
	if err = q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if !f.WithGraph {
		q = q.Select(CanvasWorkflowListFields)
	}
	err = q.Order("uses desc, id desc").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).
		Find(&list).Error
	for _, w := range list {
		w.FillView(f.UserId, false)
	}
	return list, total, err
}

// GetCanvasWorkflow 取一条：个人空间只认自己的，团队空间认队友共建的。
func GetCanvasWorkflow(userId, teamId, id int) (*CanvasWorkflow, error) {
	w := &CanvasWorkflow{}
	q := DB.Where("id = ?", id)
	if teamId > 0 {
		q = q.Where("team_id = ?", teamId)
	} else {
		q = q.Where("user_id = ? AND team_id = 0", userId)
	}
	if err := q.First(w).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return w, nil
}

// GetOwnCanvasWorkflow 改名 / 删除只允许作者本人。
func GetOwnCanvasWorkflow(userId, id int) (*CanvasWorkflow, error) {
	w := &CanvasWorkflow{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(w).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return w, nil
}

func (w *CanvasWorkflow) Delete() error {
	return DB.Delete(&CanvasWorkflow{}, w.Id).Error
}

// BumpCanvasWorkflowUses 发送到画布时 +1，列表按热度排序。
func BumpCanvasWorkflowUses(id int) {
	DB.Model(&CanvasWorkflow{}).Where("id = ?", id).UpdateColumn("uses", gorm.Expr("uses + 1"))
}
