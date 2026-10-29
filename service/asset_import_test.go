package service

import (
	"testing"
	"time"

	"easytv-api/model"

	"gorm.io/gorm"
)

// 跨空间引入的判定（API.md 36.1）：same / copy / missing
func TestAssetImportDecision(t *testing.T) {
	deleted := gorm.DeletedAt{Time: time.Now(), Valid: true}
	cases := []struct {
		name                   string
		asset                  *model.Asset
		userId, teamId, member int
		want                   string
	}{
		{"个人空间里自己的个人资产", &model.Asset{UserId: 8, TeamId: 0}, 8, 0, 0, AssetImportSame},
		{"个人空间里自己在团队传的资产要复制", &model.Asset{UserId: 8, TeamId: 1}, 8, 0, 1, "copy"},
		{"团队空间里该团队的资产（队友的）", &model.Asset{UserId: 9, TeamId: 1}, 8, 1, 1, AssetImportSame},
		{"团队空间里自己的个人资产要复制", &model.Asset{UserId: 8, TeamId: 0}, 8, 1, 1, "copy"},
		{"个人空间里自己所在团队的队友资产要复制", &model.Asset{UserId: 9, TeamId: 1}, 8, 0, 1, "copy"},
		{"别的团队的资产无权", &model.Asset{UserId: 9, TeamId: 2}, 8, 1, 1, AssetImportMissing},
		{"别人的个人资产无权", &model.Asset{UserId: 9, TeamId: 0}, 8, 0, 0, AssetImportMissing},
		{"已删除", &model.Asset{UserId: 8, TeamId: 0, DeletedAt: deleted}, 8, 0, 0, AssetImportMissing},
		{"不存在", nil, 8, 0, 0, AssetImportMissing},
	}
	for _, c := range cases {
		if got := assetImportDecision(c.asset, c.userId, c.teamId, c.member); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}
