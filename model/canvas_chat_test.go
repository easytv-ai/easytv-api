package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestNormalizeCanvasChatTitle(t *testing.T) {
	if got := NormalizeCanvasChatTitle("  仙帝  送外卖  "); got != "仙帝 送外卖" {
		t.Fatalf("title = %q", got)
	}
	if got := NormalizeCanvasChatTitle("   "); got != "新对话" {
		t.Fatalf("empty title = %q", got)
	}
	long := strings.Repeat("字", CanvasChatTitleMax+5)
	got := NormalizeCanvasChatTitle(long)
	if r := []rune(got); len(r) != CanvasChatTitleMax {
		t.Fatalf("truncated len = %d", len(r))
	}
}

func TestNormalizeCanvasChatMessages(t *testing.T) {
	raw := json.RawMessage(`[
		{"id":1,"role":"user","text":"《仙帝在人间送外卖》","pending":false},
		{"id":2,"role":"assistant","text":"先确认","pending":true},
		{"id":3,"role":"assistant","text":"画幅用竖屏","card":{"kind":"ask","locked":false},"undoToken":9}
	]`)
	text, n, err := NormalizeCanvasChatMessages(raw)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, body = %s", n, text)
	}
	if strings.Contains(text, "undoToken") || strings.Contains(text, "先确认") {
		t.Fatalf("应丢掉进行中气泡和撤销凭据: %s", text)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatal(err)
	}
	card, _ := list[1]["card"].(map[string]any)
	if card["kind"] != "ask" {
		t.Fatalf("card = %#v", list[1]["card"])
	}

	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"empty", `[]`, ErrCanvasChatEmpty},
		{"null", `null`, ErrCanvasChatEmpty},
		{"not array", `{"role":"user"}`, ErrCanvasChatInvalid},
		{"bad role", `[{"role":"system","text":"x"}]`, ErrCanvasChatInvalid},
		{"text type", `[{"role":"user","text":1}]`, ErrCanvasChatInvalid},
		{"only pending", `[{"role":"user","text":"x","pending":true}]`, ErrCanvasChatEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := NormalizeCanvasChatMessages(json.RawMessage(tc.raw))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	many := make([]map[string]any, CanvasChatMessageMaxN+1)
	for i := range many {
		many[i] = map[string]any{"role": "user", "text": "a"}
	}
	buf, _ := json.Marshal(many)
	if _, _, err := NormalizeCanvasChatMessages(buf); !errors.Is(err, ErrCanvasChatTooMany) {
		t.Fatalf("too many err = %v", err)
	}
}

func TestListCanvasChatsScopesOwner(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	old := DB
	DB = db
	t.Cleanup(func() { DB = old })

	mock.ExpectQuery("SELECT `id`,`user_id`,`canvas_id`,`title`,`message_count`,`created_at`,`updated_at` FROM `canvas_chat` WHERE user_id = \\? AND canvas_id = \\? ORDER BY updated_at desc, id desc LIMIT 30").
		WithArgs(7, 66).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "canvas_id", "title", "message_count", "created_at", "updated_at"}).
			AddRow(3, 7, 66, "仙帝送外卖", 6, 10, 20))
	list, err := ListCanvasChats(7, 66)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "仙帝送外卖" || list[0].MessageCount != 6 {
		t.Fatalf("list = %+v", list)
	}
	if _, ok := list[0].Summary()["messages"]; ok {
		t.Fatal("历史列表不应带消息正文")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
