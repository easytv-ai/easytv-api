package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

func TestParseTeamHeader(t *testing.T) {
	cases := map[string]int{"": 0, " ": 0, "0": 0, "3": 3, " 12 ": 12, "-1": 0, "abc": 0, "3.5": 0}
	for raw, want := range cases {
		if got := ParseTeamHeader(raw); got != want {
			t.Errorf("ParseTeamHeader(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestTeamSpaceAllowed(t *testing.T) {
	if teamSpaceAllowed(3, nil) {
		t.Fatal("不在团队不能进团队空间")
	}
	if teamSpaceAllowed(3, &model.TeamMember{TeamId: 4}) {
		t.Fatal("别的团队不能进")
	}
	if !teamSpaceAllowed(3, &model.TeamMember{TeamId: 3}) {
		t.Fatal("本团队成员应放行")
	}
}

func TestTeamSpaceExempt(t *testing.T) {
	for _, p := range []string{"/user/self", "/user/team", "/user/team/mine", "/user/team/member/:uid", "/user/invoice/summary", "/user/invoice/apply"} {
		if !teamSpaceExempt(p) {
			t.Errorf("%s 应不看空间头", p)
		}
	}
	for _, p := range []string{"/user/project", "/user/teamwork", "/user/self/groups", "/user/wallet"} {
		if teamSpaceExempt(p) {
			t.Errorf("%s 应校验空间头", p)
		}
	}
}

// TestTeamSpacePersonalPassThrough 个人空间（无头 / 0）不查库、不写 team_id。
func TestTeamSpacePersonalPassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(TeamSpace())
	r.GET("/user/project", func(c *gin.Context) {
		if _, ok := c.Get("team_id"); ok {
			t.Error("个人空间不该写 team_id")
		}
		c.String(http.StatusOK, "ok")
	})
	for _, h := range []string{"", "0"} {
		req := httptest.NewRequest(http.MethodGet, "/user/project", nil)
		if h != "" {
			req.Header.Set(TeamSpaceHeader, h)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Body.String() != "ok" {
			t.Fatalf("header %q: body = %s", h, w.Body.String())
		}
	}
}

func TestCORSAllowsTeamHeader(t *testing.T) {
	w := doRequest(newCORSEngine(), http.MethodGet, "https://easytv.ai")
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "EasyTV-Team") {
		t.Fatalf("Allow-Headers 缺 EasyTV-Team: %s", w.Header().Get("Access-Control-Allow-Headers"))
	}
}
