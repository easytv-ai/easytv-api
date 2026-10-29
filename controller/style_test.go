package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func strp(s string) *string { return &s }
func intp(i int) *int       { return &i }

// TestStylePayloadFields 风格库管理端入参校验（API.md 34）。
func TestStylePayloadFields(t *testing.T) {
	t.Run("empty name", func(t *testing.T) {
		p := stylePayload{Name: strp("  ")}
		if _, msg := p.fields(); msg != "请填写风格名" {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run("empty prompt", func(t *testing.T) {
		p := stylePayload{Prompt: strp("")}
		if _, msg := p.fields(); msg != "请填写风格提示词" {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run("prompt too long", func(t *testing.T) {
		p := stylePayload{Prompt: strp(strings.Repeat("风", 2001))}
		if _, msg := p.fields(); msg != "风格提示词过长" {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run("invalid status", func(t *testing.T) {
		p := stylePayload{Status: intp(5)}
		if _, msg := p.fields(); msg != "无效的状态" {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run("pending status accepted by fields", func(t *testing.T) {
		p := stylePayload{Status: intp(3)}
		if f, msg := p.fields(); msg != "" || f["status"] != 3 {
			t.Fatalf("f = %v msg = %q", f, msg)
		}
	})
	t.Run("source asset id", func(t *testing.T) {
		p := stylePayload{SourceAssetId: intp(-1)}
		if _, msg := p.fields(); msg != "无效的来源图" {
			t.Fatalf("msg = %q", msg)
		}
		p = stylePayload{SourceAssetId: intp(42)}
		f, msg := p.fields()
		if msg != "" || f["source_asset_id"] != 42 {
			t.Fatalf("f = %v msg = %q", f, msg)
		}
		if styleFromFields(f).SourceAssetId != 42 {
			t.Fatal("styleFromFields 未带 source_asset_id")
		}
	})
	t.Run("invalid kind", func(t *testing.T) {
		kinds := []string{"audio"}
		p := stylePayload{Kinds: &kinds}
		if _, msg := p.fields(); msg != "无效的适用类型" {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run("ok", func(t *testing.T) {
		tags := []string{" 胶片 ", "", "颗粒"}
		kinds := []string{"Video", "image", "video"}
		p := stylePayload{
			Name:      strp(" 电影胶片 "),
			Prompt:    strp(" 35mm 胶片颗粒 "),
			Negative:  strp(" 过曝 "),
			Tags:      &tags,
			Kinds:     &kinds,
			Category:  strp("电影感"),
			SortOrder: intp(5),
			Status:    intp(2),
		}
		f, msg := p.fields()
		if msg != "" {
			t.Fatalf("unexpected msg %q", msg)
		}
		want := map[string]any{
			"name": "电影胶片", "prompt": "35mm 胶片颗粒", "negative": "过曝",
			"tags": "胶片,颗粒", "kinds": "video,image", "category": "电影感",
			"sort_order": 5, "status": 2,
		}
		for k, v := range want {
			if f[k] != v {
				t.Errorf("%s = %v, want %v", k, f[k], v)
			}
		}
	})
	t.Run("kinds cleared", func(t *testing.T) {
		kinds := []string{}
		p := stylePayload{Kinds: &kinds}
		f, msg := p.fields()
		if msg != "" || f["kinds"] != "" {
			t.Fatalf("f = %v msg = %q", f, msg)
		}
	})
}

// TestStyleStatusRules 官方 / 用户风格可写状态、管理端改用户行的字段限制（API.md 34.5）。
func TestStyleStatusRules(t *testing.T) {
	for _, tc := range []struct {
		official bool
		status   int
		ok       bool
	}{
		{true, 1, true}, {true, 2, true}, {true, 3, false}, {true, 4, false},
		{false, 1, true}, {false, 2, false}, {false, 3, true}, {false, 4, true},
	} {
		if got := styleStatusAllowed(tc.official, tc.status); got != tc.ok {
			t.Errorf("styleStatusAllowed(%v, %d) = %v", tc.official, tc.status, got)
		}
	}
	user := &model.Style{UserId: 8, Status: model.StyleStatusOn}
	if msg := adminStyleUpdateCheck(user, map[string]any{"prompt": "x"}); msg != "用户风格只能修改分类、标签、排序和状态" {
		t.Fatalf("msg = %q", msg)
	}
	if msg := adminStyleUpdateCheck(user, map[string]any{"status": 2}); msg != "无效的状态" {
		t.Fatalf("msg = %q", msg)
	}
	if msg := adminStyleUpdateCheck(user, map[string]any{"category": "电影感", "status": 4}); msg != "" {
		t.Fatalf("msg = %q", msg)
	}
	official := &model.Style{Status: model.StyleStatusOn}
	if msg := adminStyleUpdateCheck(official, map[string]any{"prompt": "x", "status": 2}); msg != "" {
		t.Fatalf("msg = %q", msg)
	}
	if msg := adminStyleUpdateCheck(official, map[string]any{"status": 4}); msg != "无效的状态" {
		t.Fatalf("msg = %q", msg)
	}
}

// TestUserStyleTransitions 投稿 / 撤回 / 编辑回退 / 审核的状态机。
func TestUserStyleTransitions(t *testing.T) {
	t.Run("submit", func(t *testing.T) {
		if _, msg := userStyleSubmitTransition(&model.Style{Status: 1}); msg != "投稿需要一张封面" {
			t.Fatalf("msg = %q", msg)
		}
		if next, msg := userStyleSubmitTransition(&model.Style{Status: 1, CoverUrl: "https://x/a.png"}); msg != "" || next != 3 {
			t.Fatalf("next = %d msg = %q", next, msg)
		}
		if _, msg := userStyleSubmitTransition(&model.Style{Status: 3, CoverUrl: "c"}); msg != "已投稿，请等待审核" {
			t.Fatalf("msg = %q", msg)
		}
		if _, msg := userStyleSubmitTransition(&model.Style{Status: 4, CoverUrl: "c"}); msg != "已收录" {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run("withdraw", func(t *testing.T) {
		for _, st := range []int{3, 4} {
			if next, msg := userStyleWithdrawTransition(&model.Style{Status: st}); msg != "" || next != 1 {
				t.Fatalf("status %d: next = %d msg = %q", st, next, msg)
			}
		}
		if _, msg := userStyleWithdrawTransition(&model.Style{Status: 1}); msg == "" {
			t.Fatal("status 1 不应可撤回")
		}
	})
	t.Run("edit", func(t *testing.T) {
		if userStyleStatusAfterEdit(4) != 3 || userStyleStatusAfterEdit(3) != 3 || userStyleStatusAfterEdit(1) != 1 {
			t.Fatal("编辑回退规则不对")
		}
	})
	t.Run("review", func(t *testing.T) {
		if _, msg := styleReviewTransition(&model.Style{UserId: 0, Status: 3}, true); msg != "官方风格无需审核" {
			t.Fatalf("msg = %q", msg)
		}
		if _, msg := styleReviewTransition(&model.Style{UserId: 8, Status: 1}, true); msg != "该风格不在待审核状态" {
			t.Fatalf("msg = %q", msg)
		}
		if next, _ := styleReviewTransition(&model.Style{UserId: 8, Status: 3}, true); next != 4 {
			t.Fatalf("approve next = %d", next)
		}
		if next, _ := styleReviewTransition(&model.Style{UserId: 8, Status: 3}, false); next != 1 {
			t.Fatalf("reject next = %d", next)
		}
		title, body := styleReviewNotice("胶片", false, "封面不清晰")
		if title != "风格投稿未通过" || !strings.Contains(body, "原因：封面不清晰") {
			t.Fatalf("title = %q body = %q", title, body)
		}
		title, _ = styleReviewNotice("胶片", true, "")
		if title != "风格投稿已收录" {
			t.Fatalf("title = %q", title)
		}
	})
}

// TestStyleVisibility 公开 / 套用口径的纯函数。
func TestStyleVisibility(t *testing.T) {
	cases := []struct {
		s      model.Style
		public bool
	}{
		{model.Style{UserId: 0, Status: 1}, true},
		{model.Style{UserId: 0, Status: 2}, false},
		{model.Style{UserId: 8, Status: 1}, false},
		{model.Style{UserId: 8, Status: 3}, false},
		{model.Style{UserId: 8, Status: 4}, true},
	}
	for _, tc := range cases {
		if got := model.IsPublicStyle(&tc.s); got != tc.public {
			t.Errorf("IsPublicStyle(%+v) = %v", tc.s, got)
		}
	}
	private := &model.Style{UserId: 8, Status: 3}
	if !model.CanUseStyle(private, 8) || model.CanUseStyle(private, 9) || model.CanUseStyle(private, 0) {
		t.Fatal("私有风格只有本人可套用")
	}
}

// TestPublicStyleListSQL 公开列表的 SQL 带可见性口径（sqlmock，不连库）。
func TestPublicStyleListSQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new failed: %v", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open failed: %v", err)
	}
	old := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = old })

	visible := "\\(\\(user_id = 0 AND status = \\?\\) OR status = \\?\\)"
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `style` WHERE "+visible+" AND `style`.`deleted_at` IS NULL").
		WithArgs(1, 4).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT \\* FROM `style` WHERE "+visible+" AND `style`.`deleted_at` IS NULL ORDER BY sort_order desc, id desc LIMIT 10").
		WithArgs(1, 4).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "name", "status"}).AddRow(7, 8, "我的胶片", 4))

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/style", GetPublicStyles)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/style", nil))
	if !strings.Contains(w.Body.String(), `"我的胶片"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql 不符: %v", err)
	}
}
