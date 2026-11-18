package model

import "easytv-api/common"

// 播放页关注只统计这段时间里的人数。更早的关注没有作品号，补不回来。
const workFollowWindowSec int64 = 30 * 24 * 3600

// AttachWorkGrowth 给我的作品列表补上近 30 天关注人数，以及做同款后再发布的成片数。
func AttachWorkGrowth(list []*Work) error {
	ids := make([]int, 0, len(list))
	for _, w := range list {
		if w != nil && w.Id > 0 {
			ids = append(ids, w.Id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	follows, err := CountWorkFollowsSince(ids, common.GetTimestamp()-workFollowWindowSec)
	if err != nil {
		return err
	}
	remixes, err := countRemixPublished(ids)
	if err != nil {
		return err
	}
	for _, w := range list {
		if w == nil {
			continue
		}
		w.Follow30d = follows[w.Id]
		w.RemixWorks = remixes[w.Id]
	}
	return nil
}

// countRemixPublished 来源作品 → 从它复制出去的项目里，已发布成片的条数。
func countRemixPublished(workIds []int) (map[int]int, error) {
	out := map[int]int{}
	if len(workIds) == 0 {
		return out, nil
	}
	var rows []struct {
		SourceWorkId int
		N            int
	}
	err := DB.Model(&Work{}).
		Select("project.source_work_id AS source_work_id, COUNT(work.id) AS n").
		Joins("JOIN project ON project.id = work.project_id AND project.deleted_at IS NULL").
		Where("project.source_work_id IN ? AND work.status = ?", workIds, WorkStatusPublished).
		Group("project.source_work_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.SourceWorkId] = row.N
	}
	return out, nil
}
