package service

import (
	"fmt"

	"easytv-api/common"
	"easytv-api/model"
)

// ===== 套餐额度：并发路数与云存储（API.md 6.9）=====
//
// 额度以「付费主体」为准：创作版按用户本人；团队版按团队共享（并发 = 计划并发 × 席位，存储 = 计划存储 × 席位），
// 团队全体成员的进行中任务与资产一起计入。未开通会员用免费额度（FREE_CONCURRENT / FREE_STORAGE_GB）。
// 付费主体按请求空间定（API.md 20）：个人空间是本人，团队空间是团队 owner。

// Quota 生效的额度。
type Quota struct {
	PlanName     string `json:"plan_name"`
	Edition      string `json:"edition"`
	Seats        int    `json:"seats"`
	IsMember     bool   `json:"is_member"`
	Concurrent   int    `json:"concurrent"`
	StorageBytes int64  `json:"storage_bytes"`
	// Scope self / team：用量按哪一组用户统计
	Scope   string `json:"scope"`
	userIds []int
}

// Usage 当前用量。
type Usage struct {
	ActiveTasks  int64 `json:"active_tasks"`
	StorageBytes int64 `json:"storage_bytes"`
}

func freeQuota(userId int) Quota {
	return Quota{
		PlanName:     "",
		Edition:      "free",
		Seats:        1,
		Concurrent:   common.GetEnvOrDefault("FREE_CONCURRENT", 1),
		StorageBytes: int64(common.GetEnvOrDefault("FREE_STORAGE_GB", 2)) * 1024 * 1024 * 1024,
		Scope:        "self",
		userIds:      []int{userId},
	}
}

// quotaPayer 额度的付费主体：个人空间是本人，团队空间是团队 owner（团队不存在时退回本人）。
func quotaPayer(userId, teamId int) int {
	if owner := TeamOwnerOf(teamId); owner > 0 {
		return owner
	}
	return userId
}

// QuotaFor 计算用户在 teamId 空间里的生效额度（0 个人空间）。
func QuotaFor(userId, teamId int) Quota {
	payer := quotaPayer(userId, teamId)
	ms := model.GetActiveMembership(payer)
	if ms == nil {
		return freeQuota(userId)
	}
	plan, err := model.GetMembershipPlanById(ms.PlanId)
	if err != nil {
		plan, err = model.GetMembershipPlanByCode(ms.PlanCode)
	}
	q := Quota{
		PlanName: ms.PlanName,
		Edition:  ms.Edition,
		Seats:    ms.Seats,
		IsMember: true,
		Scope:    "self",
		userIds:  []int{userId},
	}
	concurrent, storageGb := 1, 0
	if err == nil && plan != nil {
		concurrent, storageGb = plan.Concurrent, plan.StorageGb
	}
	if q.Seats <= 0 {
		q.Seats = 1
	}
	if ms.Edition == model.EditionTeam {
		q.Scope = "team"
		q.userIds = teamUserIds(payer, userId)
		q.Concurrent = concurrent * q.Seats
		q.StorageBytes = int64(storageGb) * int64(q.Seats) * 1024 * 1024 * 1024
	} else {
		q.Concurrent = concurrent
		q.StorageBytes = int64(storageGb) * 1024 * 1024 * 1024
	}
	if q.Concurrent <= 0 {
		q.Concurrent = 1
	}
	if q.StorageBytes <= 0 {
		q.StorageBytes = freeQuota(userId).StorageBytes
	}
	return q
}

// teamUserIds 团队全部成员（含 owner）；查不到时退化为本人。
func teamUserIds(ownerId, fallback int) []int {
	m, err := model.GetTeamMembership(ownerId)
	if err != nil {
		return []int{fallback}
	}
	members, err := model.GetTeamMembers(m.TeamId)
	if err != nil || len(members) == 0 {
		return []int{fallback}
	}
	ids := make([]int, 0, len(members)+1)
	seen := map[int]bool{}
	for _, mem := range members {
		if !seen[mem.UserId] {
			seen[mem.UserId] = true
			ids = append(ids, mem.UserId)
		}
	}
	if !seen[ownerId] {
		ids = append(ids, ownerId)
	}
	return ids
}

// UsageFor 按额度范围统计用量。
func UsageFor(q Quota) Usage {
	return Usage{
		ActiveTasks:  model.CountActiveTasks(q.userIds),
		StorageBytes: model.SumAssetSize(q.userIds),
	}
}

// UserIds 额度覆盖的用户（团队用量页用）。
func (q Quota) UserIds() []int { return q.userIds }

// ErrConcurrencyFull 并发已满。调用方要能和「真的失败」区分开：
// 成片向导会整层提交，撞上限属于正常排队，前端应该等空位再重试而不是标失败。
type ErrConcurrencyFull struct {
	Limit int
	Used  int
	Team  bool
}

func (e *ErrConcurrencyFull) Error() string {
	if e.Team {
		return fmt.Sprintf("团队并发已达上限（%d 路），请等待进行中的任务完成或增加席位", e.Limit)
	}
	return fmt.Sprintf("并发已达上限（%d 路），请等待进行中的任务完成或升级套餐", e.Limit)
}

// CheckConcurrency 创建任务前校验并发路数；满了返回 *ErrConcurrencyFull。
func CheckConcurrency(userId, teamId int) error {
	q := QuotaFor(userId, teamId)
	used := model.CountActiveTasks(q.userIds)
	if used >= int64(q.Concurrent) {
		return &ErrConcurrencyFull{Limit: q.Concurrent, Used: int(used), Team: q.Scope == "team"}
	}
	return nil
}

// CheckStorage 上传前校验云存储额度（团队空间按团队 owner 的套餐与全队用量）。
func CheckStorage(userId, teamId int, addBytes int64) error {
	q := QuotaFor(userId, teamId)
	used := model.SumAssetSize(q.userIds)
	if used+addBytes > q.StorageBytes {
		return fmt.Errorf("云存储空间不足（已用 %s / 上限 %s），请清理资产或升级套餐", FormatBytes(used), FormatBytes(q.StorageBytes))
	}
	return nil
}

// FormatBytes 人类可读大小。
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// UsageView GET /user/usage 响应（按请求空间）。
func UsageView(userId, teamId int) map[string]any {
	q := QuotaFor(userId, teamId)
	u := UsageFor(q)
	return map[string]any{
		"plan": map[string]any{
			"name":      q.PlanName,
			"edition":   q.Edition,
			"seats":     q.Seats,
			"is_member": q.IsMember,
			"scope":     q.Scope,
		},
		"concurrent": map[string]any{"used": u.ActiveTasks, "limit": q.Concurrent},
		"storage":    map[string]any{"used_bytes": u.StorageBytes, "limit_bytes": q.StorageBytes},
	}
}

// TeamUsageView GET /user/team/usage：成员维度近 30 天用量。团队管理接口，不看请求空间，按用户所在团队统计。
func TeamUsageView(userId int) map[string]any {
	teamId := TeamIdOf(userId)
	if teamId == 0 {
		return nil
	}
	q := QuotaFor(userId, teamId)
	if q.Scope != "team" {
		return nil
	}
	rows := model.MemberUsage(q.userIds, common.GetTimestamp()-30*86400)
	names := model.GetUserNicknames(q.userIds)
	roles := map[int]string{}
	if members, err := model.GetTeamMembers(teamId); err == nil {
		for _, mem := range members {
			roles[mem.UserId] = mem.Role
		}
	}
	var totalTasks, totalPoints, totalStorage, totalActive int64
	items := make([]map[string]any, 0, len(q.userIds))
	for _, uid := range q.userIds {
		r := rows[uid]
		totalTasks += r.Tasks30d
		totalPoints += r.Points30d
		totalStorage += r.StorageBytes
		totalActive += r.ActiveTasks
		items = append(items, map[string]any{
			"user_id":       uid,
			"nickname":      names[uid],
			"role":          roles[uid],
			"tasks_30d":     r.Tasks30d,
			"points_30d":    r.Points30d,
			"storage_bytes": r.StorageBytes,
			"active_tasks":  r.ActiveTasks,
		})
	}
	return map[string]any{
		"members": items,
		"totals": map[string]any{
			"tasks_30d":     totalTasks,
			"points_30d":    totalPoints,
			"storage_bytes": totalStorage,
			"active_tasks":  totalActive,
		},
		"concurrent":   map[string]any{"used": totalActive, "limit": q.Concurrent},
		"storage":      map[string]any{"used_bytes": totalStorage, "limit_bytes": q.StorageBytes},
		"pool_balance": model.GetPointsBalance(nil, quotaPayer(userId, teamId)),
	}
}
