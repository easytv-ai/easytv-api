package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Subject 主体库（F2）：角色 / 场景 / 道具，带参考图，画布与成片里可引用以保持一致性。
type Subject struct {
	Id           int    `json:"id"`
	UserId       int    `json:"user_id" gorm:"index"`
	TeamId       int    `json:"team_id" gorm:"index;default:0"`
	Kind         string `json:"kind" gorm:"type:varchar(12);index;default:'character';comment:character/scene/prop"`
	Name         string `json:"name" gorm:"type:varchar(50)"`
	Look         string `json:"look" gorm:"type:text;comment:用于文生图的外貌 / 环境描述"`
	Prompt       string `json:"prompt" gorm:"type:text;default:'';comment:引用时追加的提示词，空则用 look"`
	CoverAssetId int    `json:"cover_asset_id" gorm:"default:0"`
	CoverUrl     string `json:"cover_url" gorm:"type:varchar(1000);default:''"`
	Refs         string `json:"-" gorm:"type:text;comment:[{asset_id,url,label}]"`
	Shared       int    `json:"shared" gorm:"type:tinyint;default:0;comment:1 团队内共享"`
	Uses         int    `json:"uses" gorm:"default:0"`
	// Version 内容版本（API.md 14.2）：名称 / 类型 / 描述 / 提示词 / 封面 / 参考图变了就 +1，并在 subject_version 存快照
	Version   int            `json:"version" gorm:"default:1"`
	CreatedAt int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	RefsView json.RawMessage `json:"refs" gorm:"-"`
	Mine     bool            `json:"mine" gorm:"-"`
}

func (Subject) TableName() string { return "subject" }

const (
	SubjectKindCharacter = "character"
	SubjectKindScene     = "scene"
	SubjectKindProp      = "prop"
)

var SubjectKinds = map[string]bool{SubjectKindCharacter: true, SubjectKindScene: true, SubjectKindProp: true}

// SubjectRef 一张参考图。
type SubjectRef struct {
	AssetId int    `json:"asset_id"`
	Url     string `json:"url"`
	Label   string `json:"label"`
}

func (s *Subject) FillView(viewerId int) {
	if s.Refs == "" {
		s.RefsView = json.RawMessage("[]")
	} else {
		s.RefsView = json.RawMessage(s.Refs)
	}
	s.Mine = s.UserId == viewerId
}

func (s *Subject) ParseRefs() []SubjectRef {
	var list []SubjectRef
	_ = json.Unmarshal([]byte(s.Refs), &list)
	return list
}

// SetRefs 写回参考图并补封面。
func (s *Subject) SetRefs(refs []SubjectRef) error {
	b, _ := json.Marshal(refs)
	fields := map[string]any{"refs": string(b)}
	if len(refs) > 0 && (s.CoverUrl == "" || !subjectCoverInRefs(s.CoverAssetId, refs)) {
		cover := subjectCoverRef(refs)
		fields["cover_url"] = cover.Url
		fields["cover_asset_id"] = cover.AssetId
	}
	return updateSubjectVersioned(func(db *gorm.DB) *gorm.DB { return db.Model(&Subject{}).Where("id = ?", s.Id) }, s, fields)
}

func subjectCoverInRefs(assetId int, refs []SubjectRef) bool {
	if assetId <= 0 {
		return false
	}
	for _, r := range refs {
		if r.AssetId == assetId {
			return true
		}
	}
	return false
}

// subjectCoverRef 封面优先用正面 / 全景，没有就用第一张。
func subjectCoverRef(refs []SubjectRef) SubjectRef {
	for _, r := range refs {
		if r.Label == "正面" || r.Label == "全景" {
			return r
		}
	}
	return refs[0]
}

func (s *Subject) Insert() error {
	now := common.GetTimestamp()
	s.CreatedAt, s.UpdatedAt = now, now
	if s.Refs == "" {
		s.Refs = "[]"
	}
	if !SubjectKinds[s.Kind] {
		s.Kind = SubjectKindCharacter
	}
	if s.Version <= 0 {
		s.Version = 1
	}
	if err := DB.Create(s).Error; err != nil {
		return err
	}
	return saveSubjectSnapshot(DB, s)
}

// SubjectVersion 主体某个版本的内容快照（API.md 14.2）。画布节点可以锁定引用的版本，主体再改也不影响已锁定的镜头。
type SubjectVersion struct {
	Id           int    `json:"id"`
	SubjectId    int    `json:"subject_id" gorm:"uniqueIndex:idx_subject_version"`
	Version      int    `json:"version" gorm:"uniqueIndex:idx_subject_version"`
	Kind         string `json:"kind" gorm:"type:varchar(12)"`
	Name         string `json:"name" gorm:"type:varchar(50)"`
	Look         string `json:"look" gorm:"type:text"`
	Prompt       string `json:"prompt" gorm:"type:text"`
	CoverAssetId int    `json:"cover_asset_id" gorm:"default:0"`
	CoverUrl     string `json:"cover_url" gorm:"type:varchar(1000);default:''"`
	Refs         string `json:"-" gorm:"type:text"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint"`

	RefsView json.RawMessage `json:"refs" gorm:"-"`
}

func (SubjectVersion) TableName() string { return "subject_version" }

// subjectContentFields 这些字段变了才算新版本（共享开关、使用次数不算）。
var subjectContentFields = []string{"name", "kind", "look", "prompt", "cover_asset_id", "cover_url", "refs"}

func saveSubjectSnapshot(tx *gorm.DB, s *Subject) error {
	refs := s.Refs
	if refs == "" {
		refs = "[]"
	}
	snap := &SubjectVersion{SubjectId: s.Id, Version: s.Version, Kind: s.Kind, Name: s.Name, Look: s.Look, Prompt: s.Prompt,
		CoverAssetId: s.CoverAssetId, CoverUrl: s.CoverUrl, Refs: refs, CreatedAt: common.GetTimestamp()}
	var n int64
	tx.Model(&SubjectVersion{}).Where("subject_id = ? AND version = ?", s.Id, s.Version).Count(&n)
	if n > 0 {
		return nil
	}
	return tx.Create(snap).Error
}

// updateSubjectVersioned 更新字段；内容字段有变化时版本 +1 并存新快照（旧版本没快照的先补一份）。
// scope 在给定的 db（普通连接或事务）上拼出要更新的那一行。
func updateSubjectVersioned(scope func(db *gorm.DB) *gorm.DB, s *Subject, fields map[string]any) error {
	changed := false
	for _, k := range subjectContentFields {
		if v, ok := fields[k]; ok && fmt.Sprint(v) != fmt.Sprint(subjectFieldValue(s, k)) {
			changed = true
			break
		}
	}
	fields["updated_at"] = common.GetTimestamp()
	if !changed {
		return updateOne(scope(DB), fields)
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := saveSubjectSnapshot(tx, s); err != nil {
			return err
		}
		fields["version"] = gorm.Expr("version + 1")
		if err := updateOne(scope(tx), fields); err != nil {
			return err
		}
		fresh := &Subject{}
		if err := tx.Where("id = ?", s.Id).First(fresh).Error; err != nil {
			return err
		}
		return saveSubjectSnapshot(tx, fresh)
	})
}

func subjectFieldValue(s *Subject, key string) any {
	switch key {
	case "name":
		return s.Name
	case "kind":
		return s.Kind
	case "look":
		return s.Look
	case "prompt":
		return s.Prompt
	case "cover_asset_id":
		return s.CoverAssetId
	case "cover_url":
		return s.CoverUrl
	case "refs":
		return s.Refs
	}
	return nil
}

// GetSubjectVersion 主体某个版本的快照。
func GetSubjectVersion(subjectId, version int) (*SubjectVersion, error) {
	v := &SubjectVersion{}
	err := DB.Where("subject_id = ? AND version = ?", subjectId, version).First(v).Error
	return v, wrapNotFound(err)
}

// GetSubjectVersions 主体的版本列表（新的在前）。
func GetSubjectVersions(subjectId, limit int) ([]*SubjectVersion, error) {
	var list []*SubjectVersion
	err := DB.Where("subject_id = ?", subjectId).Order("version desc").Limit(limit).Find(&list).Error
	for _, v := range list {
		v.RefsView = json.RawMessage(v.Refs)
	}
	return list, err
}

// AsSubject 快照还原成主体（用于按锁定版本生成）。
func (v *SubjectVersion) AsSubject(base *Subject) *Subject {
	out := *base
	out.Kind, out.Name, out.Look, out.Prompt = v.Kind, v.Name, v.Look, v.Prompt
	out.CoverAssetId, out.CoverUrl, out.Refs, out.Version = v.CoverAssetId, v.CoverUrl, v.Refs, v.Version
	return &out
}

// GetUserSubject 自己的，或 teamId 这个团队空间的（API.md 20）。
func GetUserSubject(userId, teamId, id int) (*Subject, error) {
	s := &Subject{}
	q := DB.Where("id = ?", id)
	if teamId > 0 {
		q = q.Where("(user_id = ? OR team_id = ?)", userId, teamId)
	} else {
		q = q.Where("user_id = ?", userId)
	}
	if err := q.First(s).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return s, nil
}

// GetOwnSubject 只取自己的（改 / 删用）。
func GetOwnSubject(userId, id int) (*Subject, error) {
	s := &Subject{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(s).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return s, nil
}

type SubjectFilter struct {
	UserId  int
	TeamId  int
	Kind    string
	Keyword string
}

// GetSubjects 主体库列表（API.md 20）：团队空间取该团队的全部主体；个人空间只取自己 team_id=0 的。
func GetSubjects(f SubjectFilter, pageInfo *common.PageInfo) (list []*Subject, total int64, err error) {
	q := DB.Model(&Subject{})
	if f.TeamId > 0 {
		q = q.Where("team_id = ?", f.TeamId)
	} else {
		q = q.Where("user_id = ? AND team_id = 0", f.UserId)
	}
	if SubjectKinds[f.Kind] {
		q = q.Where("kind = ?", f.Kind)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("(name LIKE ? OR look LIKE ?)", like, like)
	}
	if err = q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = q.Order("uses desc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	for _, s := range list {
		s.FillView(f.UserId)
	}
	return list, total, err
}

// GetSubjectsByIds 取可用主体（自己的，或 teamId 团队空间的）。
func GetSubjectsByIds(userId, teamId int, ids []int) []*Subject {
	if len(ids) == 0 {
		return nil
	}
	var list []*Subject
	q := DB.Where("id IN ?", ids)
	if teamId > 0 {
		q = q.Where("(user_id = ? OR team_id = ?)", userId, teamId)
	} else {
		q = q.Where("user_id = ?", userId)
	}
	q.Find(&list)
	return list
}

// FindSubjectByName 按名称精确找角色（策划案 build 时匹配同名主体）。
func FindSubjectByName(userId, teamId int, kind, name string) *Subject {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	s := &Subject{}
	q := DB.Where("name = ? AND kind = ?", name, kind)
	if teamId > 0 {
		q = q.Where("(user_id = ? OR team_id = ?)", userId, teamId)
	} else {
		q = q.Where("user_id = ?", userId)
	}
	if err := q.First(s).Error; err != nil {
		return nil
	}
	return s
}

func UpdateSubjectFields(userId, id int, fields map[string]any) error {
	s, err := GetOwnSubject(userId, id)
	if err != nil {
		return err
	}
	return updateSubjectVersioned(func(db *gorm.DB) *gorm.DB { return db.Model(&Subject{}).Where("id = ? AND user_id = ?", id, userId) }, s, fields)
}

func DeleteUserSubject(userId, id int) error {
	return DB.Where("id = ? AND user_id = ?", id, userId).Delete(&Subject{}).Error
}

func IncreaseSubjectUses(ids []int) {
	if len(ids) == 0 {
		return
	}
	DB.Model(&Subject{}).Where("id IN ?", ids).Update("uses", gorm.Expr("uses + 1"))
}
