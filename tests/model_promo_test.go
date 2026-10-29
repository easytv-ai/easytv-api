package tests

import (
	"testing"

	"easytv-api/model"
	"easytv-api/service"
)

// TestModelPricePromo 促销价的有效期判断与实际单价（T18）。
func TestModelPricePromo(t *testing.T) {
	now := int64(1_800_000_000)
	p := &model.ModelPrice{PointsPerUnit: 10, MemberPointsPerUnit: 8, PromoPointsPerUnit: 5, PromoStartsAt: now - 10, PromoEndsAt: now + 10}
	if !p.IsPromoActive(now) {
		t.Fatal("promo should be active inside window")
	}
	if p.IsPromoActive(now + 10) {
		t.Fatal("promo should end at promo_ends_at")
	}
	if p.IsPromoActive(now - 11) {
		t.Fatal("promo should not start before promo_starts_at")
	}
	if got := p.EffectiveUnitPoints(false, now); got != 5 {
		t.Fatalf("effective standard: want 5 got %d", got)
	}
	if got := p.EffectiveUnitPoints(true, now); got != 5 {
		t.Fatalf("effective member: want 5 got %d", got)
	}
	if got := p.EffectiveUnitPoints(true, now+100); got != 8 {
		t.Fatalf("effective member after promo: want 8 got %d", got)
	}
	// 促销价 0 = 无促销；开始 / 结束为 0 = 不限
	open := &model.ModelPrice{PointsPerUnit: 10, PromoPointsPerUnit: 7}
	if !open.IsPromoActive(now) || open.EffectiveUnitPoints(false, now) != 7 {
		t.Fatal("promo without window should always be active")
	}
	none := &model.ModelPrice{PointsPerUnit: 10}
	if none.IsPromoActive(now) || none.EffectiveUnitPoints(true, now) != 10 {
		t.Fatal("zero promo should be inactive")
	}
	none.FillEffective(now)
	if none.PromoActive || none.EffectivePointsPerUnit != 10 || none.EffectiveMemberPointsPerUnit != 10 {
		t.Fatalf("fill effective: %+v", none)
	}
}

// TestNewModelPromo 网关导入新模型自动首发价：ceil(标准价 × 比例)，持续 N 天；比例 0 / 1 或天数 0 不写。
func TestNewModelPromo(t *testing.T) {
	now := int64(1_800_000_000)
	promo, endsAt, ok := service.NewModelPromoPreview(25, 0.5, 7, "首发 5 折", now)
	if !ok || promo != 13 || endsAt != now+7*86400 {
		t.Fatalf("promo=%d endsAt=%d ok=%v", promo, endsAt, ok)
	}
	if _, _, ok := service.NewModelPromoPreview(25, 0, 7, "", now); ok {
		t.Fatal("rate 0 should disable")
	}
	if _, _, ok := service.NewModelPromoPreview(25, 1, 7, "", now); ok {
		t.Fatal("rate 1 should disable")
	}
	if _, _, ok := service.NewModelPromoPreview(25, 0.5, 0, "", now); ok {
		t.Fatal("days 0 should disable")
	}
	if _, _, ok := service.NewModelPromoPreview(1, 0.5, 7, "", now); ok {
		t.Fatal("1 point model cannot go lower")
	}
}
