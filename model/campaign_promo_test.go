package model

import (
	"testing"
)

func TestParseCampaignModelPromo(t *testing.T) {
	one := ParseCampaignModelPromo(`{"model":"Veo-3","rate":0.5}`)
	if len(one) != 1 || one[0].Model != "veo-3" || one[0].Rate != 0.5 || one[0].Label != "活动 5 折" {
		t.Fatalf("single: %+v", one)
	}
	many := ParseCampaignModelPromo(`{"models":["hailuo-02","kling"],"rate":0.8,"label":"夏日 8 折"}`)
	if len(many) != 2 || many[0].Model != "hailuo-02" || many[1].Model != "kling" || many[0].Label != "夏日 8 折" {
		t.Fatalf("models: %+v", many)
	}
	arr := ParseCampaignModelPromo(`[{"model":"a","points":8},{"model":"b","rate":0.4}]`)
	if len(arr) != 2 || arr[0].Points != 8 || arr[1].Rate != 0.4 {
		t.Fatalf("array: %+v", arr)
	}
	if got := ParseCampaignModelPromo(`{"model":"x"}`); len(got) != 0 {
		t.Fatalf("missing rate/points should drop: %+v", got)
	}
	if got := ParseCampaignModelPromo(`not-json`); len(got) != 0 {
		t.Fatalf("invalid json: %+v", got)
	}
}

func TestNormalizeCampaignModelPromo(t *testing.T) {
	norm, msg := NormalizeCampaignModelPromo(`{"model":"veo-3","rate":0.5}`)
	if msg != "" || norm == "" {
		t.Fatalf("norm=%q msg=%q", norm, msg)
	}
	if _, msg := NormalizeCampaignModelPromo(`{"foo":1}`); msg == "" {
		t.Fatal("invalid promo should error")
	}
	if got, msg := NormalizeCampaignModelPromo(""); got != "" || msg != "" {
		t.Fatalf("empty: %q %q", got, msg)
	}
}

func TestCampaignPromoPointsAndBilling(t *testing.T) {
	t.Cleanup(func() { SetTestCampaignPromos(nil) })
	if CampaignPromoPoints(10, CampaignPromoItem{Rate: 0.5}) != 5 {
		t.Fatal("half of 10")
	}
	if CampaignPromoPoints(25, CampaignPromoItem{Rate: 0.5}) != 13 {
		t.Fatal("ceil 12.5 → 13")
	}
	if CampaignPromoPoints(10, CampaignPromoItem{Points: 8}) != 8 {
		t.Fatal("absolute points")
	}
	if CampaignPromoPoints(10, CampaignPromoItem{Points: 10}) != 0 {
		t.Fatal("not cheaper")
	}

	now := int64(1_800_000_000)
	SetTestCampaignPromos([]CampaignPromoItem{{Model: "veo-3", Rate: 0.5, Label: "活动 5 折"}})
	p := &ModelPrice{Model: "veo-3", PointsPerUnit: 20, MemberPointsPerUnit: 16}
	if got := p.EffectiveUnitPoints(false, now); got != 10 {
		t.Fatalf("campaign standard: want 10 got %d", got)
	}
	if got := p.EffectiveUnitPoints(true, now); got != 10 {
		t.Fatalf("campaign beats member: want 10 got %d", got)
	}
	p.FillEffective(now)
	if !p.PromoActive || p.CampaignPromoLabel != "活动 5 折" || p.PromoPointsPerUnit != 0 {
		t.Fatalf("fill should mark campaign without mutating stored promo: %+v", p)
	}

	// 模型自带促销更便宜时，活动价不抢
	p.PromoPointsPerUnit = 6
	p.PromoLabel = "首发"
	if got := p.EffectiveUnitPoints(false, now); got != 6 {
		t.Fatalf("model promo wins: want 6 got %d", got)
	}

	other := &ModelPrice{Model: "other", PointsPerUnit: 20}
	if got := other.EffectiveUnitPoints(false, now); got != 20 {
		t.Fatalf("unlisted model stays standard: %d", got)
	}
}
