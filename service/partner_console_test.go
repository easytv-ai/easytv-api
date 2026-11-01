package service

import (
	"testing"
	"time"

	"easytv-api/model"
)

func TestBuildPartnerStatsFillsEveryDay(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	end := time.Date(2026, 11, 2, 15, 0, 0, 0, loc)
	start := time.Date(2026, 10, 31, 0, 0, 0, 0, loc)

	joins := []int64{
		time.Date(2026, 10, 31, 9, 0, 0, 0, loc).Unix(),
		time.Date(2026, 11, 2, 1, 0, 0, 0, loc).Unix(),
		time.Date(2026, 10, 30, 23, 0, 0, 0, loc).Unix(), // 区间外，忽略
	}
	rebates := []model.PartnerRebateRow{
		{CreatedAt: time.Date(2026, 11, 1, 12, 0, 0, 0, loc).Unix(), Amount: 15, SourceAmount: 100},
		{CreatedAt: time.Date(2026, 11, 1, 18, 0, 0, 0, loc).Unix(), Amount: 1.505, SourceAmount: 10.03},
	}
	s := buildPartnerStats(start, end, joins, rebates)

	if len(s.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(s.Rows))
	}
	if s.StartDate != "2026-10-31" || s.EndDate != "2026-11-02" {
		t.Fatalf("range = %s..%s", s.StartDate, s.EndDate)
	}
	if s.Rows[0].NewCustomers != 1 || s.Rows[2].NewCustomers != 1 {
		t.Fatalf("new customers = %+v", s.Rows)
	}
	day2 := s.Rows[1]
	if day2.Orders != 2 || day2.Amount != 110.03 || day2.Rebate != 16.51 {
		t.Fatalf("day2 = %+v", day2)
	}
	if s.Totals.NewCustomers != 2 || s.Totals.Orders != 2 || s.Totals.Rebate != 16.51 {
		t.Fatalf("totals = %+v", s.Totals)
	}
}
