package tests

import (
	"errors"
	"testing"

	"easytv-api/model"
)

func TestBonusCodeUsable(t *testing.T) {
	now := int64(1_000_000)
	cases := []struct {
		name string
		code model.BonusCode
		want error
	}{
		{"ok", model.BonusCode{Status: 1}, nil},
		{"disabled", model.BonusCode{Status: 0}, model.ErrBonusCodeNotFound},
		{"not started", model.BonusCode{Status: 1, StartsAt: now + 1}, model.ErrBonusCodeNotStarted},
		{"expired", model.BonusCode{Status: 1, ExpiresAt: now}, model.ErrBonusCodeExpired},
		{"exhausted", model.BonusCode{Status: 1, MaxClaims: 3, ClaimedCount: 3}, model.ErrBonusCodeExhausted},
		{"unlimited", model.BonusCode{Status: 1, MaxClaims: 0, ClaimedCount: 999}, nil},
	}
	for _, c := range cases {
		if got := c.code.Usable(now); !errors.Is(got, c.want) {
			t.Errorf("%s: want %v got %v", c.name, c.want, got)
		}
	}
	if model.NormalizeBonusCode(" easyTv2026 ") != "EASYTV2026" {
		t.Error("normalize should trim and upper-case")
	}
}
