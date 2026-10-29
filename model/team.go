package model

import (
	"errors"
	"strings"
	"time"

	"easytv-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ===== 团队版会员：团队 / 成员 / 邀请（API.md 6.7）=====

// Team 团队：购买团队版会员的用户自动成为 owner，成员共享 owner 的积分池与会员价。
type Team struct {
	Id        int    `json:"id"`
	OwnerId   int    `json:"owner_id" gorm:"uniqueIndex;comment:团队所有者（购买团队版会员的用户）"`
	Name      string `json:"name" gorm:"type:varchar(50);comment:团队名"`
	Seats     int    `json:"seats" gorm:"default:2;comment:席位数（含 owner）"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`
}

func (Team) TableName() string { return "team" }

// TeamMember 成员；一个用户同时只能属于一个团队。
type TeamMember struct {
	Id     int    `json:"id"`
	TeamId int    `json:"team_id" gorm:"index"`
	UserId int    `json:"user_id" gorm:"uniqueIndex"`
	Role   string `json:"role" gorm:"type:varchar(10);default:'member';comment:owner/member"`
	// Permission 成员权限（API.md 15）：admin 管理员 / editor 编辑者 / viewer 查看者；owner 不看这个字段，恒为管理员
	Permission string `json:"permission" gorm:"type:varchar(10);default:'editor';comment:admin/editor/viewer"`
	JoinedAt   int64  `json:"joined_at" gorm:"bigint"`
	// 成员月度积分上限（F7）：0 不限；每月 1 日按 limit_reset_at 重置 points_used
	PointsLimit  int   `json:"points_limit" gorm:"default:0"`
	PointsUsed   int   `json:"points_used" gorm:"default:0"`
	LimitResetAt int64 `json:"limit_reset_at" gorm:"bigint;default:0"`

	Nickname string `json:"nickname,omitempty" gorm:"-"`
	Username string `json:"username,omitempty" gorm:"-"`
	// PointsLeft 本月剩余（非表字段）；nil 表示不限
	PointsLeft *int `json:"points_left" gorm:"-"`
}

func (TeamMember) TableName() string { return "team_member" }

// TeamInvite 邀请码。
type TeamInvite struct {
	Id        int    `json:"id"`
	TeamId    int    `json:"team_id" gorm:"index"`
	Code      string `json:"code" gorm:"type:varchar(16);uniqueIndex"`
	CreatedBy int    `json:"created_by"`
	ExpiresAt int64  `json:"expires_at" gorm:"bigint;default:0;comment:0 不过期"`
	MaxUses   int    `json:"max_uses" gorm:"default:0;comment:0 不限"`
	Uses      int    `json:"uses" gorm:"default:0"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

func (TeamInvite) TableName() string { return "team_invite" }

const (
	TeamRoleOwner  = "owner"
	TeamRoleMember = "member"
)

var (
	ErrTeamFull        = errors.New("团队席位已满")
	ErrAlreadyInTeam   = errors.New("你已经在一个团队里，先退出再加入")
	ErrInviteInvalid   = errors.New("邀请码无效或已过期")
	ErrOwnerCannotQuit = errors.New("团队所有者不能退出团队")
	// ErrNotTeamMember 请求的团队空间 / 团队项目不属于当前用户所在团队（API.md 20）
	ErrNotTeamMember = errors.New("你已不在该团队，请切回个人空间")
)

// NewInviteCode 8 位大写字母数字。
func NewInviteCode() string {
	return strings.ToUpper(common.GetRandomString(8))
}

func GetTeamById(id int) (*Team, error) {
	t := &Team{}
	if err := DB.Where("id = ?", id).First(t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

func GetTeamByOwner(ownerId int) (*Team, error) {
	t := &Team{}
	if err := DB.Where("owner_id = ?", ownerId).First(t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

// GetTeamMembership 用户所在团队的成员记录；不在团队返回 ErrNotFound。
func GetTeamMembership(userId int) (*TeamMember, error) {
	m := &TeamMember{}
	if err := DB.Where("user_id = ?", userId).First(m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// UpsertTeam 开通/续费团队版时创建或更新团队（事务内），并确保 owner 成员记录存在。
func UpsertTeam(tx *gorm.DB, ownerId, seats int, defaultName string) (*Team, error) {
	now := common.GetTimestamp()
	t := &Team{}
	err := tx.Where("owner_id = ?", ownerId).First(t).Error
	switch {
	case err == nil:
		t.Seats = seats
		t.UpdatedAt = now
		if err := tx.Save(t).Error; err != nil {
			return nil, err
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
		t = &Team{OwnerId: ownerId, Name: defaultName, Seats: seats, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(t).Error; err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	// owner 若已在别的团队当成员，先移出
	if err := tx.Where("user_id = ? AND role = ?", ownerId, TeamRoleMember).Delete(&TeamMember{}).Error; err != nil {
		return nil, err
	}
	var count int64
	tx.Model(&TeamMember{}).Where("team_id = ? AND user_id = ?", t.Id, ownerId).Count(&count)
	if count == 0 {
		if err := tx.Create(&TeamMember{TeamId: t.Id, UserId: ownerId, Role: TeamRoleOwner, JoinedAt: now}).Error; err != nil {
			return nil, err
		}
	}
	return t, nil
}

func UpdateTeamName(ownerId int, name string) error {
	return updateOne(DB.Model(&Team{}).Where("owner_id = ?", ownerId), map[string]any{"name": name, "updated_at": common.GetTimestamp()})
}

// GetTeamMembers 成员列表（附昵称）。
func GetTeamMembers(teamId int) (list []*TeamMember, err error) {
	if err = DB.Where("team_id = ?", teamId).Order("role desc, joined_at asc").Find(&list).Error; err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(list))
	for _, m := range list {
		ids = append(ids, m.UserId)
	}
	names := GetUserNicknames(ids)
	for _, m := range list {
		m.Nickname = names[m.UserId]
	}
	return list, nil
}

func CountTeamMembers(tx *gorm.DB, teamId int) int64 {
	if tx == nil {
		tx = DB
	}
	var n int64
	tx.Model(&TeamMember{}).Where("team_id = ?", teamId).Count(&n)
	return n
}

// AddTeamMember 加入（事务内，校验席位与唯一归属）。
func AddTeamMember(tx *gorm.DB, team *Team, userId int) error {
	var existing int64
	tx.Model(&TeamMember{}).Where("user_id = ?", userId).Count(&existing)
	if existing > 0 {
		return ErrAlreadyInTeam
	}
	if CountTeamMembers(tx, team.Id) >= int64(team.Seats) {
		return ErrTeamFull
	}
	return tx.Create(&TeamMember{TeamId: team.Id, UserId: userId, Role: TeamRoleMember, JoinedAt: common.GetTimestamp()}).Error
}

// RemoveTeamMember 移除普通成员（owner 不可移除）。
func RemoveTeamMember(teamId, userId int) error {
	result := DB.Where("team_id = ? AND user_id = ? AND role = ?", teamId, userId, TeamRoleMember).Delete(&TeamMember{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ----- 邀请码 -----

func GetActiveInvite(teamId int) (*TeamInvite, error) {
	inv := &TeamInvite{}
	now := common.GetTimestamp()
	err := DB.Where("team_id = ? AND (expires_at = 0 OR expires_at > ?) AND (max_uses = 0 OR uses < max_uses)", teamId, now).
		Order("id desc").First(inv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return inv, nil
}

// CreateInvite 生成新邀请码（旧码立即失效），默认 30 天。
func CreateInvite(teamId, createdBy int) (*TeamInvite, error) {
	now := common.GetTimestamp()
	if err := DB.Model(&TeamInvite{}).Where("team_id = ? AND (expires_at = 0 OR expires_at > ?)", teamId, now).Update("expires_at", now).Error; err != nil {
		return nil, err
	}
	inv := &TeamInvite{TeamId: teamId, Code: NewInviteCode(), CreatedBy: createdBy, ExpiresAt: now + 30*86400, CreatedAt: now}
	if err := DB.Create(inv).Error; err != nil {
		return nil, err
	}
	return inv, nil
}

func GetInviteByCode(code string) (*TeamInvite, error) {
	inv := &TeamInvite{}
	now := common.GetTimestamp()
	err := DB.Where("code = ? AND (expires_at = 0 OR expires_at > ?) AND (max_uses = 0 OR uses < max_uses)", strings.ToUpper(strings.TrimSpace(code)), now).First(inv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInviteInvalid
		}
		return nil, err
	}
	return inv, nil
}

func IncrInviteUses(tx *gorm.DB, id int) error {
	return tx.Model(&TeamInvite{}).Where("id = ?", id).Update("uses", gorm.Expr("uses + 1")).Error
}

// ---- 成员积分月度上限（F7）----

// MonthStartUnix 当月 1 日零点（Asia/Shanghai）。
func MonthStartUnix() int64 {
	now := time.Now().In(CheckinLocation)
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, CheckinLocation).Unix()
}

// TeamMemberQuota 成员当月额度视图。
type TeamMemberQuota struct {
	Limit int `json:"points_limit"`
	Used  int `json:"points_used"`
	Left  int `json:"points_left"`
}

// GetMemberQuota 读成员额度，跨月自动把已用归零。
func GetMemberQuota(userId int) TeamMemberQuota {
	m, err := GetTeamMembership(userId)
	if err != nil || m == nil {
		return TeamMemberQuota{}
	}
	return memberQuotaOf(m)
}

// LockMemberQuota 在事务里锁住成员行后读额度：同一成员的预扣串行执行，
// 否则两个并发任务都会拿到同一个「已用」通过校验。
func LockMemberQuota(tx *gorm.DB, userId int) (TeamMemberQuota, error) {
	m := &TeamMember{}
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userId).First(m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TeamMemberQuota{}, nil
	}
	if err != nil {
		return TeamMemberQuota{}, err
	}
	return memberQuotaOf(m), nil
}

func memberQuotaOf(m *TeamMember) TeamMemberQuota {
	used := m.PointsUsed
	if m.LimitResetAt < MonthStartUnix() {
		used = 0
	}
	q := TeamMemberQuota{Limit: m.PointsLimit, Used: used}
	if m.PointsLimit > 0 {
		q.Left = m.PointsLimit - used
		if q.Left < 0 {
			q.Left = 0
		}
	}
	return q
}

const (
	TeamPermAdmin  = "admin"
	TeamPermEditor = "editor"
	TeamPermViewer = "viewer"
)

// IsValidTeamPermission 可设置给成员的权限。
func IsValidTeamPermission(p string) bool {
	return p == TeamPermAdmin || p == TeamPermEditor || p == TeamPermViewer
}

// EffectivePermission owner 恒为管理员；老数据空值按编辑者。
func (m *TeamMember) EffectivePermission() string {
	if m.Role == TeamRoleOwner {
		return TeamPermAdmin
	}
	if IsValidTeamPermission(m.Permission) {
		return m.Permission
	}
	return TeamPermEditor
}

// SetMemberPermission 改成员权限（不能改 owner）。
func SetMemberPermission(teamId, memberUserId int, permission string) error {
	return updateOne(DB.Model(&TeamMember{}).Where("team_id = ? AND user_id = ? AND role = ?", teamId, memberUserId, TeamRoleMember),
		map[string]any{"permission": permission})
}

// SetMemberPointsLimit owner 设某成员的月度上限。
func SetMemberPointsLimit(teamId, memberUserId, limit int) error {
	if limit < 0 {
		limit = 0
	}
	return updateOne(DB.Model(&TeamMember{}).Where("team_id = ? AND user_id = ? AND role = ?", teamId, memberUserId, TeamRoleMember),
		map[string]any{"points_limit": limit})
}

// AddMemberPointsUsed 结算成功后累加成员当月已用（跨月先归零）。
func AddMemberPointsUsed(userId, points int) {
	_ = AddMemberPointsUsedTx(DB, userId, points)
}

// AddMemberPointsUsedTx 同 AddMemberPointsUsed，在任务置成功的事务里执行：
// 任务离开进行中（预扣不再计入）与累加已用必须同时生效，否则中间有个空窗额度会被多用。
func AddMemberPointsUsedTx(tx *gorm.DB, userId, points int) error {
	if points <= 0 {
		return nil
	}
	monthStart := MonthStartUnix()
	now := common.GetTimestamp()
	if err := tx.Model(&TeamMember{}).Where("user_id = ? AND role = ? AND limit_reset_at < ?", userId, TeamRoleMember, monthStart).
		Updates(map[string]any{"points_used": 0, "limit_reset_at": now}).Error; err != nil {
		return err
	}
	return tx.Model(&TeamMember{}).Where("user_id = ? AND role = ?", userId, TeamRoleMember).
		Updates(map[string]any{"points_used": gorm.Expr("points_used + ?", points), "limit_reset_at": now}).Error
}

// AttachMemberQuotas 给成员列表补额度字段。
func AttachMemberQuotas(list []*TeamMember) {
	monthStart := MonthStartUnix()
	for _, m := range list {
		if m.Role != TeamRoleMember {
			continue
		}
		if m.LimitResetAt < monthStart {
			m.PointsUsed = 0
		}
		if m.PointsLimit > 0 {
			left := m.PointsLimit - m.PointsUsed
			if left < 0 {
				left = 0
			}
			m.PointsLeft = &left
		}
	}
}

// ---- 管理端（F7）----

// AdminTeamRow 管理端团队列表一行。
type AdminTeamRow struct {
	Id              int    `json:"id"`
	Name            string `json:"name"`
	OwnerId         int    `json:"owner_id"`
	OwnerName       string `json:"owner_name"`
	Seats           int    `json:"seats"`
	MemberCount     int64  `json:"member_count"`
	PointsLimitSum  int    `json:"points_limit_total"`
	PointsUsedMonth int    `json:"points_used_month"`
	CreatedAt       int64  `json:"created_at"`
}

// GetTeamsAdmin 管理端分页；keyword 匹配团队名，owner_id 精确。
func GetTeamsAdmin(keyword string, ownerId int, pageInfo *common.PageInfo) ([]AdminTeamRow, int64, error) {
	q := DB.Model(&Team{})
	if kw := strings.TrimSpace(keyword); kw != "" {
		q = q.Where("name LIKE ?", "%"+kw+"%")
	}
	if ownerId > 0 {
		q = q.Where("owner_id = ?", ownerId)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var teams []*Team
	if err := q.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&teams).Error; err != nil {
		return nil, 0, err
	}
	ownerIds := make([]int, 0, len(teams))
	for _, t := range teams {
		ownerIds = append(ownerIds, t.OwnerId)
	}
	names := GetUserNicknames(ownerIds)
	monthStart := MonthStartUnix()
	out := make([]AdminTeamRow, 0, len(teams))
	for _, t := range teams {
		row := AdminTeamRow{Id: t.Id, Name: t.Name, OwnerId: t.OwnerId, OwnerName: names[t.OwnerId], Seats: t.Seats, CreatedAt: t.CreatedAt}
		DB.Model(&TeamMember{}).Where("team_id = ?", t.Id).Count(&row.MemberCount)
		var agg struct {
			LimitSum int
			UsedSum  int
		}
		DB.Model(&TeamMember{}).
			Select("COALESCE(SUM(points_limit),0) AS limit_sum, COALESCE(SUM(CASE WHEN limit_reset_at >= ? THEN points_used ELSE 0 END),0) AS used_sum", monthStart).
			Where("team_id = ?", t.Id).Scan(&agg)
		row.PointsLimitSum, row.PointsUsedMonth = agg.LimitSum, agg.UsedSum
		out = append(out, row)
	}
	return out, total, nil
}

// SetMemberPointsLimitById 管理端按 team_member.id 改额度（owner 联系不上时代改）。
func SetMemberPointsLimitById(id, limit int) error {
	if limit < 0 {
		limit = 0
	}
	return updateOne(DB.Model(&TeamMember{}).Where("id = ? AND role = ?", id, TeamRoleMember),
		map[string]any{"points_limit": limit})
}
