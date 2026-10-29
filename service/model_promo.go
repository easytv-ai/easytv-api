package service

import (
	"math"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// 模型促销价（T18）。运营在后台按模型手工设促销四字段；网关导入新模型时按 GrowthNewModelPromo* 自动写首发价。

// ApplyNewModelPromo 给新导入的模型写首发价：促销价 = ceil(标准价 × GrowthNewModelPromoRate)，
// 从现在起持续 GrowthNewModelPromoDays 天，标签取 GrowthNewModelPromoLabel。
// 比例为 0 / 天数为 0 / 折后不低于标准价时不写，返回 false。
func ApplyNewModelPromo(p *model.ModelPrice) bool {
	if p == nil || p.PointsPerUnit <= 0 {
		return false
	}
	cfg := setting.GetGrowthConfig()
	return applyNewModelPromo(p, cfg.NewModelPromoRate, cfg.NewModelPromoDays, cfg.NewModelPromoLabel, common.GetTimestamp())
}

func applyNewModelPromo(p *model.ModelPrice, rate float64, days int, label string, now int64) bool {
	if rate <= 0 || rate >= 1 || days <= 0 || p.PointsPerUnit <= 0 {
		return false
	}
	promo := int(math.Ceil(float64(p.PointsPerUnit)*rate - 1e-9))
	if promo < 1 {
		promo = 1
	}
	if promo >= p.PointsPerUnit {
		return false
	}
	if r := []rune(label); len(r) > 20 {
		label = string(r[:20])
	}
	p.PromoPointsPerUnit = promo
	p.PromoLabel = label
	p.PromoStartsAt = now
	p.PromoEndsAt = now + int64(days)*86400
	return true
}

// NewModelPromoPreview 供测试与后台预览：不读 setting，直接按参数算。
func NewModelPromoPreview(points int, rate float64, days int, label string, now int64) (promo int, endsAt int64, ok bool) {
	p := &model.ModelPrice{PointsPerUnit: points}
	ok = applyNewModelPromo(p, rate, days, label, now)
	return p.PromoPointsPerUnit, p.PromoEndsAt, ok
}
