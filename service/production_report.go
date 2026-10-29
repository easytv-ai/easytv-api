package service

import (
	"math"

	"easytv-api/model"
)

// 制片报表（API.md 26.3，复审 G08）：围绕验收结果的一集看板——每镜候选 / 通过 / 退回、返工次数、
// 净结算生成成本，以及「每有效画面秒的成本」。生成结果数量不代表可交付进度，看板只认通过与选用。

type ProductionReport struct {
	CanvasId int                 `json:"canvas_id"`
	Title    string              `json:"title"`
	Shots    []ProductionShot    `json:"shots"`
	Totals   ProductionTotals    `json:"totals"`
	Composes ProductionComposes  `json:"composes"`
	Locked   bool                `json:"locked"`
	Gate     string              `json:"review_gate"`
	Missing  []ProductionMissing `json:"missing"`
}

type ProductionShot struct {
	No         int    `json:"no"`
	NodeId     string `json:"node_id"`
	Line       string `json:"line"`
	Candidates int    `json:"candidates"`
	Approved   int    `json:"approved"`
	Rejected   int    `json:"rejected"`
	Pending    int    `json:"pending"`
	Reworks    int    `json:"reworks"`
	// Selected 时间线选用的候选（0 = 节点当前视频）及其审片状态
	SelectedAssetId int    `json:"selected_asset_id"`
	SelectedStatus  string `json:"selected_status"` // approved / rejected / pending / none
	Enabled         bool   `json:"enabled"`
	// 成本：本镜（图 + 视频 + 配音节点）所有成功任务的净结算积分与任务数
	CostPoints int `json:"cost_points"`
	Tasks      int `json:"tasks"`
	// EffectiveSeconds 时间线里这一镜的预计时长（未启用为 0）
	EffectiveSeconds float64  `json:"effective_seconds"`
	ImageStatus      string   `json:"image_status"` // 分镜图审片状态
	RejectReasons    []string `json:"reject_reasons"`
}

type ProductionTotals struct {
	Shots            int     `json:"shots"`
	ShotsApproved    int     `json:"shots_approved"`
	ShotsEnabled     int     `json:"shots_enabled"`
	Candidates       int     `json:"candidates"`
	Reworks          int     `json:"reworks"`
	CostPoints       int     `json:"cost_points"`
	FailedRefund     int     `json:"failed_tasks"`
	EffectiveSeconds float64 `json:"effective_seconds"`
	// CostPerSecond 净结算成本 ÷ 有效画面秒（有效秒为 0 时为 0）
	CostPerSecond float64 `json:"cost_per_second"`
}

type ProductionComposes = model.ComposeCounts

type ProductionMissing struct {
	No     int    `json:"no"`
	NodeId string `json:"node_id"`
	What   string `json:"what"`
}

// BuildProductionReport 汇总一张画布（一集）的制作状态。
func BuildProductionReport(cv *model.Canvas) (*ProductionReport, error) {
	shots, err := BuildShots(cv)
	if err != nil {
		return nil, err
	}
	rep := &ProductionReport{CanvasId: cv.Id, Title: assetTitle(cv), Shots: []ProductionShot{}, Missing: []ProductionMissing{}}
	row, tl, _ := model.GetCanvasTimeline(cv.Id)
	if row != nil {
		rep.Locked = row.Locked
	}
	if tl == nil {
		tl = DefaultTimeline(cv, shots)
	} else {
		tl = MergeTimeline(tl, shots)
	}
	rep.Gate = NormalizeReviewGate(tl.ReviewGate)
	est := EstimateTimeline(cv, tl)
	estByNode := map[string]float64{}
	enabledByNode := map[string]bool{}
	selectedByNode := map[string]int{}
	for _, c := range tl.Clips {
		if !c.Enabled {
			continue
		}
		enabledByNode[c.NodeId] = true
		if _, ok := selectedByNode[c.NodeId]; !ok {
			selectedByNode[c.NodeId] = c.AssetId
		}
	}
	for _, c := range est.Clips {
		estByNode[c.NodeId] += c.Duration
	}
	costs := model.SumTaskCostByNode(canvasAssetOwners(cv), cv.Id)
	reviews, _ := model.GetShotReviews(cv.Id)
	statusOf := func(assetId int) string {
		if assetId <= 0 {
			return "none"
		}
		if r := reviews[assetId]; r != nil {
			if r.Status == model.ShotReviewApproved {
				return "approved"
			}
			return "rejected"
		}
		return "pending"
	}
	for _, s := range shots {
		ps := ProductionShot{No: s.No, NodeId: s.Video.NodeId, Line: s.Line, Candidates: len(s.Video.Candidates), RejectReasons: []string{}}
		for _, c := range s.Video.Candidates {
			switch statusOf(c.AssetId) {
			case "approved":
				ps.Approved++
			case "rejected":
				ps.Rejected++
				if r := reviews[c.AssetId]; r != nil && r.Reason != "" {
					ps.RejectReasons = append(ps.RejectReasons, r.Reason)
				}
			default:
				ps.Pending++
			}
		}
		if ps.Candidates > 1 {
			ps.Reworks = ps.Candidates - 1
		}
		ps.Enabled = enabledByNode[s.Video.NodeId]
		sel := selectedByNode[s.Video.NodeId]
		if sel == 0 {
			sel = s.Video.AssetId
		}
		ps.SelectedAssetId = sel
		ps.SelectedStatus = statusOf(sel)
		if s.Image != nil {
			ps.ImageStatus = statusOf(s.Image.AssetId)
		} else {
			ps.ImageStatus = "none"
		}
		nodes := []string{s.Video.NodeId}
		if s.Image != nil {
			nodes = append(nodes, s.Image.NodeId)
		}
		if s.Voice != nil {
			nodes = append(nodes, s.Voice.NodeId)
		}
		for _, n := range nodes {
			if c, ok := costs[n]; ok {
				ps.CostPoints += c.Points
				ps.Tasks += c.Tasks
			}
		}
		ps.EffectiveSeconds = math.Round(estByNode[s.Video.NodeId]*100) / 100
		rep.Shots = append(rep.Shots, ps)

		rep.Totals.Shots++
		rep.Totals.Candidates += ps.Candidates
		rep.Totals.Reworks += ps.Reworks
		if ps.Enabled {
			rep.Totals.ShotsEnabled++
			rep.Totals.EffectiveSeconds += ps.EffectiveSeconds
			if ps.SelectedStatus == "approved" {
				rep.Totals.ShotsApproved++
			} else {
				what := "选用的候选还没通过审片"
				if ps.SelectedStatus == "rejected" {
					what = "选用的候选已被退回"
				} else if sel == 0 {
					what = "还没有可用的视频"
				}
				rep.Missing = append(rep.Missing, ProductionMissing{No: ps.No, NodeId: ps.NodeId, What: what})
			}
		}
	}
	// 全画布成本：含角色图、策划案等不属于某一镜的任务
	for _, c := range costs {
		rep.Totals.CostPoints += c.Points
		rep.Totals.FailedRefund += c.Failed
	}
	rep.Totals.EffectiveSeconds = math.Round(rep.Totals.EffectiveSeconds*100) / 100
	if rep.Totals.EffectiveSeconds > 0 {
		rep.Totals.CostPerSecond = math.Round(float64(rep.Totals.CostPoints)/rep.Totals.EffectiveSeconds*100) / 100
	}
	rep.Composes = model.CountComposeJobsByCanvas(cv.Id)
	return rep, nil
}
