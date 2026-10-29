package service

import (
	"errors"

	"easytv-api/model"
)

// 跨空间素材引入（API.md 36.1）：粘贴的节点带着别的空间的资产 id，把它们复制一行到当前空间。
// 用户的决定（2026-09-24）：团队空间可以直接引用个人素材——复制一行记录（同一文件地址）而不是放宽归属校验，
// 这样队友也能拿它生成，列表 / 额度 / 删除的口径都不用改。

const assetImportMax = 200

const (
	AssetImportSame    = "same"
	AssetImportCopied  = "copied"
	AssetImportMissing = "missing"
)

type AssetImportItem struct {
	From   int    `json:"from"`
	To     int    `json:"to"`
	Status string `json:"status"`
}

type AssetImportResult struct {
	Items   []AssetImportItem `json:"items"`
	Copied  int               `json:"copied"`
	Missing int               `json:"missing"`
}

// assetImportDecision 纯函数：一个资产对（userId, teamId 当前空间；memberTeamId 用户所在团队）该怎么处理。
//   - same：已在当前空间可用（个人空间：自己的 team_id=0 资产；团队空间：该团队的资产）
//   - copy：自己的但在别的空间，或自己所在团队空间的资产
//   - missing：已删除 / 无权
func assetImportDecision(a *model.Asset, userId, teamId, memberTeamId int) string {
	if a == nil || a.DeletedAt.Valid {
		return AssetImportMissing
	}
	if teamId > 0 {
		if a.TeamId == teamId {
			return AssetImportSame
		}
	} else if a.UserId == userId && a.TeamId == 0 {
		return AssetImportSame
	}
	if a.UserId == userId || (a.TeamId > 0 && a.TeamId == memberTeamId) {
		return "copy"
	}
	return AssetImportMissing
}

// ImportAssets 把一批资产引入当前空间；同一资产在同一空间只复制一次（按 import_from 回查）。
func ImportAssets(userId, teamId int, ids []int) (*AssetImportResult, error) {
	if len(ids) > assetImportMax {
		return nil, errors.New("一次最多引入 200 个素材")
	}
	memberTeamId := 0
	if m, err := model.GetTeamMembership(userId); err == nil && m != nil {
		memberTeamId = m.TeamId
	}
	res := &AssetImportResult{Items: make([]AssetImportItem, 0, len(ids))}
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		a, err := model.GetAssetByIdUnscoped(id)
		if err != nil {
			a = nil
		}
		switch assetImportDecision(a, userId, teamId, memberTeamId) {
		case AssetImportSame:
			res.Items = append(res.Items, AssetImportItem{From: id, To: id, Status: AssetImportSame})
		case "copy":
			// 引入过就复用那一行；引入行本身再被引入时追到最初的源
			from := id
			if a.ImportFrom > 0 {
				from = a.ImportFrom
			}
			if existing, err := model.FindImportedAsset(userId, teamId, from); err == nil && existing != nil {
				res.Items = append(res.Items, AssetImportItem{From: id, To: existing.Id, Status: AssetImportSame})
				continue
			}
			// 不带 storage_key 与 size：回收站清理时不会把共享的文件删掉，云存储额度也不重复计
			copyRow := &model.Asset{
				UserId: userId, TeamId: teamId, Type: a.Type, Name: a.Name, Url: a.Url, CoverUrl: a.CoverUrl,
				Mime: a.Mime, Width: a.Width, Height: a.Height, Duration: a.Duration,
				Source: model.AssetSourceImport, TaskId: a.TaskId, ImportFrom: from,
			}
			if err := copyRow.Insert(); err != nil {
				return nil, err
			}
			res.Copied++
			res.Items = append(res.Items, AssetImportItem{From: id, To: copyRow.Id, Status: AssetImportCopied})
		default:
			res.Missing++
			res.Items = append(res.Items, AssetImportItem{From: id, To: 0, Status: AssetImportMissing})
		}
	}
	return res, nil
}
