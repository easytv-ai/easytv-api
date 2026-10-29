package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"easytv-api/common"
	"easytv-api/setting"
)

func TestInvoicePureHelpers(t *testing.T) {
	if InvoiceUsername(42) != "easytv_42" {
		t.Fatalf("username: %s", InvoiceUsername(42))
	}
	if got := InvoiceUsernameForUser(42, "Ada@Example.com"); got != "ada@example.com" {
		t.Fatalf("邮箱用户名: %s", got)
	}
	if got := InvoiceUsernameForUser(42, "wx_guest"); got != "easytv_42" {
		t.Fatalf("非邮箱回退: %s", got)
	}
	if names := InvoiceOccupiedUsernames(42, "ada@example.com"); len(names) != 2 || names[0] != "ada@example.com" || names[1] != "easytv_42" {
		t.Fatalf("占用用户名: %v", names)
	}
	if names := InvoiceOccupiedUsernames(42, "wx_guest"); len(names) != 1 || names[0] != "easytv_42" {
		t.Fatalf("仅历史用户名: %v", names)
	}
	for state, want := range map[int]bool{0: true, 4: true, 1: true, 2: false, 3: false, -1: false, -9: false, 7: false} {
		if InvoiceStateOccupied(state) != want {
			t.Errorf("state %d 占用应为 %v", state, want)
		}
	}
	for cat, want := range map[string]bool{
		"全电电子普通发票": false, "增值税电子普通发票": false, "增值税专用发票": true, "全电电子专用发票": true, "电子专票": true, "": false,
	} {
		if IsSpecialInvoiceCategory(cat) != want {
			t.Errorf("%q 专票判断应为 %v", cat, want)
		}
	}
	cases := []struct{ paid, invoiced, want float64 }{
		{100, 0, 100},
		{100.1, 0.2, 99.9},
		{0.3, 0.1, 0.2},
		{50, 80, 0},
		{99.995, 0, 100},
	}
	for _, c := range cases {
		if got := InvoiceAvailableAmount(c.paid, c.invoiced); got != c.want {
			t.Errorf("available(%v,%v)=%v want %v", c.paid, c.invoiced, got, c.want)
		}
	}
	no := NewInvoiceOutOrderNo(42, time.Date(2026, 9, 15, 8, 5, 3, 0, time.Local))
	if !regexp.MustCompile(`^ETI2026091508050342\d{4}$`).MatchString(no) {
		t.Fatalf("out order no: %s", no)
	}
}

func TestNormalizeInvoiceApply(t *testing.T) {
	ok := func() InvoiceApplyRequest {
		return InvoiceApplyRequest{Amount: 12.34, Type: "个人", Email: " a@b.com ", PurchaserName: " 张三 "}
	}
	r := ok()
	if err := NormalizeInvoiceApply(&r, "全电电子普通发票"); err != nil {
		t.Fatalf("合法个人申请: %v", err)
	}
	if r.Category != "全电电子普通发票" || r.Email != "a@b.com" || r.PurchaserName != "张三" {
		t.Fatalf("应去空白并补缺省票种: %+v", r)
	}

	bad := []struct {
		mut  func(*InvoiceApplyRequest)
		want string
	}{
		{func(r *InvoiceApplyRequest) { r.Amount = 0 }, "开票金额必须大于 0"},
		{func(r *InvoiceApplyRequest) { r.Amount = -1 }, "开票金额必须大于 0"},
		{func(r *InvoiceApplyRequest) { r.Amount = 1.234 }, "开票金额最多保留两位小数"},
		{func(r *InvoiceApplyRequest) { r.Type = "" }, "抬头类型只能是企业或个人"},
		{func(r *InvoiceApplyRequest) { r.PurchaserName = " " }, "请填写抬头名称"},
		{func(r *InvoiceApplyRequest) { r.Type = "企业" }, "企业抬头需填写纳税人识别号"},
		{func(r *InvoiceApplyRequest) { r.Type = "企业"; r.PurchaserName = "" }, "请填写抬头名称"},
		{func(r *InvoiceApplyRequest) { r.Category = "增值税专用发票" }, "增值税专用发票需填写抬头税号"},
		{func(r *InvoiceApplyRequest) { r.Email = "" }, "请填写接收发票的邮箱"},
		{func(r *InvoiceApplyRequest) { r.Email = "not-an-email" }, "请填写接收发票的邮箱"},
		{func(r *InvoiceApplyRequest) { r.Email = "a@localhost" }, "请填写接收发票的邮箱"},
		{func(r *InvoiceApplyRequest) { r.Email = "张三 <a@b.com>" }, "请填写接收发票的邮箱"},
		{func(r *InvoiceApplyRequest) { r.Remark = strings.Repeat("备", 201) }, "备注不能超过 200 字"},
	}
	for i, c := range bad {
		r := ok()
		c.mut(&r)
		if err := NormalizeInvoiceApply(&r, "全电电子普通发票"); err == nil || err.Error() != c.want {
			t.Errorf("#%d 期望「%s」，得到 %v", i, c.want, err)
		}
	}

	r = ok()
	r.Type, r.PurchaserTaxpayerNumber, r.Category, r.Amount, r.Remark = "企业", "91310000MA1FL", "增值税专用发票", 0.1, strings.Repeat("备", 200)
	if err := NormalizeInvoiceApply(&r, "全电电子普通发票"); err != nil {
		t.Fatalf("合法企业专票: %v", err)
	}
}

func TestCheckInvoiceAmount(t *testing.T) {
	if err := CheckInvoiceAmount(100, 100); err != nil {
		t.Fatalf("等于可开票应通过: %v", err)
	}
	if err := CheckInvoiceAmount(0.3, 0.1+0.2); err != nil {
		t.Fatalf("浮点误差不应拦截: %v", err)
	}
	if err := CheckInvoiceAmount(100.01, 100); err == nil || err.Error() != "开票金额不能超过可开票金额 ¥100.00" {
		t.Fatalf("超额: %v", err)
	}
	if err := CheckInvoiceAmount(1, 0); err == nil || err.Error() != "开票金额不能超过可开票金额 ¥0.00" {
		t.Fatalf("无额度: %v", err)
	}
}

// fakeInvoicePlatform 假发票平台：校验 appKey/appSecret，按 username 返回预置发票并记录开票请求。
type fakeInvoicePlatform struct {
	mu        sync.Mutex
	invoices  []invoiceListRow // 当前用户的全部发票
	pageSize  int              // 平台实际每页条数（可小于请求的 size）
	listCalls int
	made      []InvoiceMakeRequest
	makeResp  string // 非空时 /invoice/make 原样返回
	tokenResp string // 非空时 /access-token 原样返回
}

func (f *fakeInvoicePlatform) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		q := r.URL.Query()
		if q.Get("appKey") != "k" || q.Get("appSecret") != "s" {
			_, _ = io.WriteString(w, `{"code":-1,"message":"appKey 或 appSecret 错误"}`)
			return false
		}
		return true
	}
	mux.HandleFunc("/access-token", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if f.tokenResp != "" {
			_, _ = io.WriteString(w, f.tokenResp)
			return
		}
		fmt.Fprintf(w, `{"code":1,"message":"成功","content":{"accessToken":"tok-%s","username":"%s"}}`, r.URL.Query().Get("username"), r.URL.Query().Get("username"))
	})
	mux.HandleFunc("/invoices", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.listCalls++
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size := f.pageSize
		if size == 0 {
			size, _ = strconv.Atoi(r.URL.Query().Get("size"))
		}
		start := page * size
		if start >= len(f.invoices) {
			_, _ = io.WriteString(w, `{"code":0,"message":"暂无数据","content":[]}`)
			return
		}
		end := min(start+size, len(f.invoices))
		body, _ := json.Marshal(map[string]any{
			"code": 1, "content": f.invoices[start:end], "last": end == len(f.invoices), "totalElements": len(f.invoices),
		})
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/invoice/make", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("make 应为 POST JSON: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		var req InvoiceMakeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode make: %v", err)
		}
		f.mu.Lock()
		f.made = append(f.made, req)
		f.mu.Unlock()
		if f.makeResp != "" {
			_, _ = io.WriteString(w, f.makeResp)
			return
		}
		_, _ = io.WriteString(w, `{"code":1,"message":"提交成功","content":{"invoiceId":9,"state":0}}`)
	})
	return mux
}

func newFakeInvoiceClient(t *testing.T, f *fakeInvoicePlatform) *InvoiceClient {
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return NewInvoiceClient(setting.InvoiceConfig{ServiceUrl: srv.URL + "/", AppKey: "k", AppSecret: "s"})
}

func TestInvoiceClientAccessToken(t *testing.T) {
	f := &fakeInvoicePlatform{}
	cl := newFakeInvoiceClient(t, f)
	tok, err := cl.AccessToken("easytv_7")
	if err != nil || tok.AccessToken != "tok-easytv_7" || tok.Username != "easytv_7" {
		t.Fatalf("token: %+v err=%v", tok, err)
	}

	f.tokenResp = `{"code":-2,"message":"用户不存在"}`
	if _, err := cl.AccessToken("easytv_7"); InvoiceErrorMessage(err) != "用户不存在" {
		t.Fatalf("平台错误原样返回: %v", err)
	}
	f.tokenResp = `{"code":1,"content":{}}`
	if _, err := cl.AccessToken("easytv_7"); InvoiceErrorMessage(err) != "发票平台未返回有效令牌" {
		t.Fatalf("空令牌: %v", err)
	}

	bad := *cl
	bad.AppSecret = "wrong"
	if _, err := bad.AccessToken("easytv_7"); InvoiceErrorMessage(err) != "appKey 或 appSecret 错误" {
		t.Fatalf("密钥错: %v", err)
	}
	bad.AppKey = ""
	if _, err := bad.AccessToken("easytv_7"); !errors.Is(err, ErrInvoiceConfigMissing) || InvoiceErrorMessage(err) != "发票功能配置不完整，请联系客服" {
		t.Fatalf("缺密钥: %v", err)
	}
}

func TestInvoiceClientSumOccupied(t *testing.T) {
	var rows []invoiceListRow
	want := 0.0
	for i := 0; i < 230; i++ {
		states := []int{0, 4, 1, 2, 3, -1, -9}
		r := invoiceListRow{State: states[i%len(states)], Price: 9.9, Tax: 0.1}
		if InvoiceStateOccupied(r.State) {
			want += 10
		}
		rows = append(rows, r)
	}
	f := &fakeInvoicePlatform{invoices: rows}
	cl := newFakeInvoiceClient(t, f)
	got, err := cl.SumOccupied("easytv_1")
	if err != nil || got != InvoiceRound2(want) {
		t.Fatalf("sum=%v want %v err=%v", got, want, err)
	}
	if f.listCalls != 3 {
		t.Fatalf("230 条每页 100 应拉 3 页，实际 %d", f.listCalls)
	}

	// 平台每页只给 20 条：不能因为条数不足 100 就提前停
	f.pageSize, f.listCalls = 20, 0
	if got, err := cl.SumOccupied("easytv_1"); err != nil || got != InvoiceRound2(want) || f.listCalls != 12 {
		t.Fatalf("小分页 sum=%v calls=%d err=%v", got, f.listCalls, err)
	}

	// 暂无数据
	f.invoices, f.pageSize = nil, 0
	if got, err := cl.SumOccupied("easytv_1"); err != nil || got != 0 {
		t.Fatalf("无数据: %v %v", got, err)
	}

	// 平台不可达 → 额度 fail-closed
	down := NewInvoiceClient(setting.InvoiceConfig{ServiceUrl: "http://127.0.0.1:1", AppKey: "k", AppSecret: "s"})
	if _, err := down.SumOccupied("easytv_1"); err == nil || InvoiceErrorMessage(err) != "发票平台暂时不可用，请稍后再试" {
		t.Fatalf("不可达: %v", err)
	} else if strings.Contains(err.Error(), "appSecret") {
		t.Fatalf("错误信息不能带密钥（会进日志）: %v", err)
	}
	q := ComputeInvoiceQuota(88.8, "easytv_1", down.SumOccupied)
	if q.Available != 0 || q.Paid != 88.8 || q.Error != "发票平台暂时不可用，可开票金额暂不可用" {
		t.Fatalf("不可达额度: %+v", q)
	}
	noKey := NewInvoiceClient(setting.InvoiceConfig{ServiceUrl: "http://127.0.0.1:1"})
	if q := ComputeInvoiceQuota(88.8, "easytv_1", noKey.SumOccupied); q.Available != 0 || q.Error != "发票功能配置不完整，请联系客服" {
		t.Fatalf("缺密钥额度: %+v", q)
	}

	// 正常：实付 100 − 占用 30.5
	f.invoices = []invoiceListRow{{State: 1, Price: 20, Tax: 0.2}, {State: 0, Price: 10, Tax: 0.3}, {State: 2, Price: 50}}
	if q := ComputeInvoiceQuota(100, "easytv_1", cl.SumOccupied); q.Error != "" || q.Invoiced != 30.5 || q.Available != 69.5 {
		t.Fatalf("额度: %+v", q)
	}
}

func TestInvoiceClientMakeInvoice(t *testing.T) {
	f := &fakeInvoicePlatform{}
	cl := newFakeInvoiceClient(t, f)
	cfg := setting.InvoiceConfigFrom(func(string) string { return "" })
	req := InvoiceApplyRequest{Amount: 88.8, Type: "企业", Category: "增值税专用发票", Email: "a@b.com",
		PurchaserName: "某某科技", PurchaserTaxpayerNumber: "9131", PurchaserBank: "招行", Remark: "备注"}
	mk := BuildInvoiceMakeRequest(cfg, req, "tok", "easytv_3", "ETI1")
	content, err := cl.MakeInvoice(mk)
	if err != nil || !strings.Contains(string(content), `"invoiceId":9`) {
		t.Fatalf("make: %s err=%v", content, err)
	}
	if len(f.made) != 1 {
		t.Fatalf("应提交一次: %d", len(f.made))
	}
	got := f.made[0]
	if got.AccessToken != "tok" || got.Username != "easytv_3" || got.OutOrderNo != "ETI1" || got.Type != "企业" ||
		got.Category != "增值税专用发票" || got.PurchaserTaxpayerNumber != "9131" || got.PurchaserBank != "招行" || got.Email != "a@b.com" {
		t.Fatalf("请求字段: %+v", got)
	}
	if len(got.Items) != 1 || got.Items[0] != (InvoiceMakeItem{No: "3040203", TaxRate: "0.01", Sum: "88.80", Name: "技术服务费"}) {
		t.Fatalf("明细: %+v", got.Items)
	}

	f.makeResp = `{"code":-1,"message":"税号格式不正确"}`
	if _, err := cl.MakeInvoice(mk); InvoiceErrorMessage(err) != "税号格式不正确" {
		t.Fatalf("平台拒绝: %v", err)
	}
	f.makeResp = `{"code":0,"message":""}`
	if _, err := cl.MakeInvoice(mk); InvoiceErrorMessage(err) != "发票平台未受理开票申请" {
		t.Fatalf("code=0: %v", err)
	}
}

// TestLockInvoiceApplyLocal Redis 未连接时走进程内锁。
func TestLockInvoiceApplyLocal(t *testing.T) {
	if common.RedisClient != nil {
		t.Skip("redis 已连接，只测进程内锁")
	}
	unlock, ok := LockInvoiceApply(99)
	if !ok {
		t.Fatal("首次加锁应成功")
	}
	if _, ok := LockInvoiceApply(99); ok {
		t.Fatal("持锁期间同用户应失败")
	}
	unlock2, ok := LockInvoiceApply(100)
	if !ok {
		t.Fatal("不同用户互不影响")
	}
	unlock2()
	unlock()
	unlock3, ok := LockInvoiceApply(99)
	if !ok {
		t.Fatal("释放后应可再加锁")
	}
	unlock3()
}

// TestInvoicePendingNotSeen 刚提交、平台还没列出的申请计入已开票；出现后或超过 24 小时清掉。
func TestInvoicePendingNotSeen(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	entries := map[string]invoicePendingEntry{
		"A": {Amount: 10, At: now.Unix() - 60},
		"B": {Amount: 20.5, At: now.Unix() - 60},
		"C": {Amount: 5, At: now.Unix() - int64(invoicePendingTTL.Seconds()) - 1},
	}
	sum, stale := pendingNotSeen(entries, map[string]bool{"A": true}, now)
	if sum != 20.5 {
		t.Fatalf("sum = %v", sum)
	}
	if len(stale) != 2 {
		t.Fatalf("stale = %v", stale)
	}
}

func TestAllocateInvoiceFIFO(t *testing.T) {
	if got := AllocateInvoiceFIFO(nil, 10); len(got) != 0 {
		t.Fatalf("空列表应无变更: %v", got)
	}
	if got := AllocateInvoiceFIFO([]RechargeInvoiceRow{{Id: 1, InvoiceableAmount: 100}}, 0); len(got) != 0 {
		t.Fatalf("金额 0 应无变更: %v", got)
	}

	rows := []RechargeInvoiceRow{
		{Id: 1, InvoiceableAmount: 40},
		{Id: 2, InvoiceableAmount: 100, Invoiced: true},
		{Id: 3, InvoiceableAmount: 60},
	}
	changed := AllocateInvoiceFIFO(rows, 70)
	if len(changed) != 2 {
		t.Fatalf("应扣两单：%v", changed)
	}
	if rows[0].InvoiceableAmount != 0 || !rows[0].Invoiced {
		t.Fatalf("第一单应开完：%+v", rows[0])
	}
	if rows[1].InvoiceableAmount != 100 || !rows[1].Invoiced {
		t.Fatalf("已开票单应跳过：%+v", rows[1])
	}
	if rows[2].InvoiceableAmount != 30 || rows[2].Invoiced {
		t.Fatalf("第二未开票单应剩 30：%+v", rows[2])
	}
}

func TestReconcileInvoiceFIFO(t *testing.T) {
	rows := []RechargeInvoiceRow{
		{Id: 1, Original: 40, InvoiceableAmount: 40},
		{Id: 2, Original: 60, InvoiceableAmount: 60},
	}
	changed := ReconcileInvoiceFIFO(rows, 70)
	if len(changed) != 2 {
		t.Fatalf("两单都应改：%v", changed)
	}
	if rows[0].InvoiceableAmount != 0 || !rows[0].Invoiced {
		t.Fatalf("第一单应开完：%+v", rows[0])
	}
	if rows[1].InvoiceableAmount != 30 || rows[1].Invoiced {
		t.Fatalf("第二单应剩 30：%+v", rows[1])
	}
	// 占用降回 0（作废）应把余额还回去
	changed = ReconcileInvoiceFIFO(rows, 0)
	if len(changed) != 2 || rows[0].InvoiceableAmount != 40 || rows[0].Invoiced || rows[1].InvoiceableAmount != 60 {
		t.Fatalf("占用清零应还原：changed=%v rows=%+v", changed, rows)
	}
	if got := ReconcileInvoiceFIFO(rows, 0); len(got) != 0 {
		t.Fatalf("已对齐不应再写：%v", got)
	}
}
