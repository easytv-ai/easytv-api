package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"easytv-api/model"
)

// 资产引用（API.md 14.1）：删除素材前告诉用户它在哪些画布节点、主体、作品、成片时间线里被用着。
// 不维护引用表：按用户范围先用 LIKE 粗筛，再解析 JSON 精确确认，数据量是单个用户的，够用且不会和保存逻辑不同步。

const assetRefsLimit = 20

// AssetRef 一处引用。
type AssetRef struct {
	Type    string   `json:"type"` // canvas / subject / work / timeline
	Id      int      `json:"id"`
	Name    string   `json:"name"`
	NodeIds []string `json:"node_ids,omitempty"`
}

// FindAssetRefs 列出引用该资产的地方（最多 20 处）。
func FindAssetRefs(userId, assetId int) []AssetRef {
	refs := []AssetRef{}
	if assetId <= 0 {
		return refs
	}
	needle := fmt.Sprintf("%%:%d%%", assetId)

	var canvases []*model.Canvas
	model.DB.Select("id, name, graph").Where("user_id = ? AND graph LIKE ?", userId, needle).Limit(200).Find(&canvases)
	for _, cv := range canvases {
		var g struct {
			Nodes []struct {
				Id   string         `json:"id"`
				Data map[string]any `json:"data"`
			} `json:"nodes"`
		}
		if json.Unmarshal([]byte(cv.Graph), &g) != nil {
			continue
		}
		var nodeIds []string
		for _, n := range g.Nodes {
			if jsonReferencesAsset(n.Data, assetId) {
				nodeIds = append(nodeIds, n.Id)
			}
		}
		if len(nodeIds) > 0 {
			refs = append(refs, AssetRef{Type: "canvas", Id: cv.Id, Name: cv.Name, NodeIds: nodeIds})
		}
	}

	var subjects []*model.Subject
	model.DB.Select("id, name, refs, cover_asset_id").Where("user_id = ? AND (cover_asset_id = ? OR refs LIKE ?)", userId, assetId, needle).Limit(100).Find(&subjects)
	for _, s := range subjects {
		used := s.CoverAssetId == assetId
		for _, r := range s.ParseRefs() {
			if r.AssetId == assetId {
				used = true
			}
		}
		if used {
			refs = append(refs, AssetRef{Type: "subject", Id: s.Id, Name: s.Name})
		}
	}

	var works []*model.Work
	model.DB.Select("id, title").Where("user_id = ? AND asset_id = ?", userId, assetId).Limit(20).Find(&works)
	for _, w := range works {
		refs = append(refs, AssetRef{Type: "work", Id: w.Id, Name: w.Title})
	}

	var timelines []*model.CanvasTimeline
	model.DB.Select("canvas_id, data").Where("user_id = ? AND data LIKE ?", userId, needle).Limit(50).Find(&timelines)
	for _, row := range timelines {
		var tl model.Timeline
		if json.Unmarshal([]byte(row.Data), &tl) != nil {
			continue
		}
		for _, c := range tl.Clips {
			if c.AssetId == assetId {
				refs = append(refs, AssetRef{Type: "timeline", Id: row.CanvasId, NodeIds: []string{c.NodeId}})
				break
			}
		}
	}
	if len(refs) > assetRefsLimit {
		refs = refs[:assetRefsLimit]
	}
	return refs
}

// jsonReferencesAsset 递归判断节点数据里是否有键（assetId / asset_id / *AssetId）等于该资产 id。
func jsonReferencesAsset(v any, assetId int) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if k == "assetId" || k == "asset_id" || strings.HasSuffix(k, "AssetId") {
				if toInt(child) == assetId {
					return true
				}
				continue
			}
			if jsonReferencesAsset(child, assetId) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if jsonReferencesAsset(child, assetId) {
				return true
			}
		}
	}
	return false
}

// AssetRefsSummary 一句话概括引用（删除确认用）。
func AssetRefsSummary(refs []AssetRef) string {
	counts := map[string]int{}
	for _, r := range refs {
		counts[r.Type]++
	}
	parts := []string{}
	for _, k := range []struct{ key, label string }{{"canvas", "张画布"}, {"subject", "个主体"}, {"work", "个作品"}, {"timeline", "条成片时间线"}} {
		if counts[k.key] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k.key], k.label))
		}
	}
	return "该素材正在被 " + strings.Join(parts, "、") + " 使用"
}
