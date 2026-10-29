package providers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchGatewayBalanceToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/billing/subscription"):
			_, _ = w.Write([]byte(`{"object":"billing_subscription","hard_limit_usd":50,"soft_limit_usd":50}`))
		case strings.HasSuffix(r.URL.Path, "/billing/usage"):
			if r.URL.Query().Get("start_date") == "" {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"object":"list","total_usage":4321}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	b, err := FetchGatewayBalance(srv.URL, "sk-test")
	if err != nil || b.UsedUSD != 43.21 || b.RemainUSD != 6.79 || b.Unlimited || b.Source != "token" {
		t.Fatalf("限额令牌：剩余 = 总额度 − 已用：%+v %v", b, err)
	}
	if _, err := FetchGatewayBalance(srv.URL, "wrong"); err == nil {
		t.Fatal("鉴权失败应报错")
	}
}

func TestFetchGatewayBalanceUnlimitedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/billing/subscription") {
			_, _ = w.Write([]byte(`{"hard_limit_usd":100000000}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	b, err := FetchGatewayBalance(srv.URL, "k")
	if err != nil || !b.Unlimited || b.RemainUSD != 0 || b.UsedUSD != 0 {
		t.Fatalf("不限额令牌只报 unlimited，usage 缺失可容忍：%+v %v", b, err)
	}
}

func TestFetchGatewayAccountBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer at-1" || r.Header.Get("EasyAPI-User") != "7" {
			_, _ = w.Write([]byte(`{"success":false,"message":"未登录或登录已过期"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"quota":-405000,"used_quota":67210000000}}`))
	}))
	defer srv.Close()
	b, err := FetchGatewayAccountBalance(srv.URL+"/", "at-1", "7")
	if err != nil || b.RemainUSD != -0.81 || b.UsedUSD != 134420 || b.Source != "account" || b.Unlimited {
		t.Fatalf("账户余额按 500000 quota = $1 换算，允许负数：%+v %v", b, err)
	}
	if _, err := FetchGatewayAccountBalance(srv.URL, "bad", "7"); err == nil || !strings.Contains(err.Error(), "未登录") {
		t.Fatalf("鉴权失败要带上网关原话：%v", err)
	}
}
