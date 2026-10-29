package model

import (
	"encoding/json"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Campaign 活动（T33）：投稿墙 + 榜单 + 按名次发奖。
type Campaign struct {
	Id           int            `json:"id"`
	Title        string         `json:"title" gorm:"type:varchar(100)"`
	Slug         string         `json:"slug" gorm:"type:varchar(60);uniqueIndex"`
	BannerUrl    string         `json:"banner_url" gorm:"type:varchar(1000);default:''"`
	Rules        string         `json:"rules" gorm:"type:text"`
	Tag          string         `json:"tag" gorm:"type:varchar(50);default:''"`
	StartsAt     int64          `json:"starts_at" gorm:"bigint;default:0"`
	EndsAt       int64          `json:"ends_at" gorm:"bigint;default:0"`
	Status       int            `json:"status" gorm:"index;default:0;comment:0 草稿 1 进行中 2 已结束 3 已发奖"`
	Prizes       string         `json:"-" gorm:"type:text;comment:[{rank_from,rank_to,points,member_days}]"`
	ModelPromo   string         `json:"model_promo" gorm:"type:text"`
	EntriesCount int            `json:"entries_count" gorm:"default:0"`
	CreatedAt    int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt    int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`

	PrizesView       json.RawMessage     `json:"prizes" gorm:"-"`
	ModelPromoItems  []CampaignPromoItem `json:"model_promo_items,omitempty" gorm:"-"`
	MyEntry          *CampaignEntry      `json:"my_entry,omitempty" gorm:"-"`
}

func (Campaign) TableName() string { return "campaign" }

const (
	CampaignStatusDraft   = 0
	CampaignStatusActive  = 1
	CampaignStatusEnded   = 2
	CampaignStatusSettled = 3

	CampaignEntryPending  = 0
	CampaignEntryApproved = 1
	CampaignEntryRejected = 2
)

type CampaignPrize struct {
	RankFrom   int `json:"rank_from"`
	RankTo     int `json:"rank_to"`
	Points     int `json:"points"`
	MemberDays int `json:"member_days"`
}

// CampaignEntry 投稿。
type CampaignEntry struct {
	Id            int   `json:"id"`
	CampaignId    int   `json:"campaign_id" gorm:"uniqueIndex:idx_campaign_work,priority:1"`
	WorkId        int   `json:"work_id" gorm:"uniqueIndex:idx_campaign_work,priority:2"`
	UserId        int   `json:"user_id" gorm:"index"`
	Status        int   `json:"status" gorm:"index;default:0;comment:0 待审 1 通过 2 驳回"`
	LikesSnapshot int   `json:"likes_snapshot" gorm:"default:0"`
	Rank          int   `json:"rank" gorm:"default:0"`
	PrizePoints   int   `json:"prize_points" gorm:"default:0"`
	PrizeDays     int   `json:"prize_days" gorm:"default:0"`
	CreatedAt     int64 `json:"created_at" gorm:"bigint"`

	Work *Work       `json:"work,omitempty" gorm:"-"`
	User *WorkAuthor `json:"user,omitempty" gorm:"-"`
}

func (CampaignEntry) TableName() string { return "campaign_entry" }

func (c *Campaign) FillView() {
	if c.Prizes == "" {
		c.PrizesView = json.RawMessage("[]")
	} else {
		c.PrizesView = json.RawMessage(c.Prizes)
	}
	c.ModelPromoItems = ParseCampaignModelPromo(c.ModelPromo)
}

func (c *Campaign) ParsePrizes() []CampaignPrize {
	var list []CampaignPrize
	_ = json.Unmarshal([]byte(c.Prizes), &list)
	return list
}

// EffectiveStatus 按时间推导展示状态：草稿、已结束、已发奖按存的状态；进行中的到期后算已结束。
func (c *Campaign) EffectiveStatus(now int64) int {
	if c.Status == CampaignStatusDraft || c.Status == CampaignStatusEnded || c.Status == CampaignStatusSettled {
		return c.Status
	}
	if c.EndsAt > 0 && now >= c.EndsAt {
		return CampaignStatusEnded
	}
	return CampaignStatusActive
}

func (c *Campaign) Insert() error {
	now := common.GetTimestamp()
	c.CreatedAt, c.UpdatedAt = now, now
	return DB.Create(c).Error
}

func GetCampaignById(id int) (*Campaign, error) {
	c := &Campaign{}
	if err := DB.Where("id = ?", id).First(c).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	c.FillView()
	return c, nil
}

func GetCampaignBySlug(slug string) (*Campaign, error) {
	c := &Campaign{}
	if err := DB.Where("slug = ?", slug).First(c).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	c.FillView()
	return c, nil
}

// GetPublicCampaigns 进行中（按开始时间）+ 最近 3 个已结束 / 已发奖。
func GetPublicCampaigns() ([]*Campaign, error) {
	now := common.GetTimestamp()
	var active, ended []*Campaign
	if err := DB.Where("status = ? AND (ends_at = 0 OR ends_at > ?)", CampaignStatusActive, now).Order("starts_at desc").Find(&active).Error; err != nil {
		return nil, err
	}
	DB.Where("status IN ? OR (status = ? AND ends_at > 0 AND ends_at <= ?)", []int{CampaignStatusEnded, CampaignStatusSettled}, CampaignStatusActive, now).
		Order("ends_at desc").Limit(3).Find(&ended)
	list := append(active, ended...)
	for _, c := range list {
		c.FillView()
	}
	return list, nil
}

func GetCampaigns(keyword string, status *int, pageInfo *common.PageInfo) (list []*Campaign, total int64, err error) {
	q := DB.Model(&Campaign{})
	if kw := strings.TrimSpace(keyword); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("(title LIKE ? OR slug LIKE ? OR tag LIKE ?)", like, like, like)
	}
	if status != nil {
		q = q.Where("status = ?", *status)
	}
	if err = q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = q.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	for _, c := range list {
		c.FillView()
	}
	return list, total, err
}

func UpdateCampaignFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return updateOne(DB.Model(&Campaign{}).Where("id = ?", id), fields)
}

func DeleteCampaign(id int) error {
	return DB.Where("id = ?", id).Delete(&Campaign{}).Error
}

// ---- 投稿 ----

func (e *CampaignEntry) Insert() error {
	e.CreatedAt = common.GetTimestamp()
	if err := DB.Create(e).Error; err != nil {
		return err
	}
	DB.Model(&Campaign{}).Where("id = ?", e.CampaignId).Update("entries_count", gorm.Expr("entries_count + 1"))
	return nil
}

func GetCampaignEntryById(id int) (*CampaignEntry, error) {
	e := &CampaignEntry{}
	if err := DB.Where("id = ?", id).First(e).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return e, nil
}

func GetUserCampaignEntry(campaignId, userId int) *CampaignEntry {
	e := &CampaignEntry{}
	if err := DB.Where("campaign_id = ? AND user_id = ?", campaignId, userId).Order("id desc").First(e).Error; err != nil {
		return nil
	}
	return e
}

// GetCampaignEntries 投稿列表；sort=likes 按作品当前点赞排序（已发奖后按 rank）。
func GetCampaignEntries(campaignId int, status *int, sort string, pageInfo *common.PageInfo) (list []*CampaignEntry, total int64, err error) {
	q := DB.Model(&CampaignEntry{}).Where("campaign_id = ?", campaignId)
	if status != nil {
		q = q.Where("status = ?", *status)
	}
	if err = q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "id desc"
	switch sort {
	case "likes":
		order = "likes_snapshot desc, id asc"
	case "rank":
		order = "CASE WHEN `rank` > 0 THEN 0 ELSE 1 END, `rank` asc, id asc"
	}
	err = q.Order(order).Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	AttachCampaignEntryRefs(list)
	return list, total, err
}

// AttachCampaignEntryRefs 填充作品与作者。
func AttachCampaignEntryRefs(list []*CampaignEntry) {
	if len(list) == 0 {
		return
	}
	workIds, userIds := []int{}, []int{}
	for _, e := range list {
		workIds = append(workIds, e.WorkId)
		userIds = append(userIds, e.UserId)
	}
	var works []*Work
	DB.Where("id IN ?", workIds).Find(&works)
	attachWorkAuthors(works)
	byId := map[int]*Work{}
	for _, w := range works {
		byId[w.Id] = w
	}
	names := GetUserNicknames(userIds)
	for _, e := range list {
		e.Work = byId[e.WorkId]
		e.User = &WorkAuthor{Id: e.UserId, Nickname: names[e.UserId]}
	}
}

func UpdateCampaignEntryFields(id int, fields map[string]any) error {
	return updateOne(DB.Model(&CampaignEntry{}).Where("id = ?", id), fields)
}

// RefreshCampaignLikes 把作品当前点赞数刷进投稿快照。
func RefreshCampaignLikes(campaignId int) {
	DB.Exec("UPDATE campaign_entry e JOIN work w ON w.id = e.work_id SET e.likes_snapshot = w.like_count WHERE e.campaign_id = ?", campaignId)
}
