package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"

	"github.com/shopspring/decimal"
)

// ===== 发票（API.md 21，参照 EasyAPI）=====
// 发票由第三方「EasyAPI 发票平台」开具，本地不建表：已开票金额每次从平台按用户汇总。
// 平台协议：响应 {code, message, content, last, totalElements}；code=1 成功，code=0 暂无数据，code<0 错误（看 message）。

const (
	invoiceHTTPTimeout = 15 * time.Second
	invoicePageSize    = 100
	invoiceMaxPages    = 50 // 防御性封顶 5000 张
	invoiceRemarkMax   = 200
	invoiceLockTTL     = 30 * time.Second

	InvoiceTypeCompany  = "企业"
	InvoiceTypePersonal = "个人"
)

// 发票平台状态：0 审核中、4 开票中、1 已开票 占用可开票金额；2 作废、3 红冲、-1 失败、-9 放弃 不占用。
const (
	InvoiceStateReviewing = 0
	InvoiceStateIssued    = 1
	InvoiceStateVoided    = 2
	InvoiceStateRedFlush  = 3
	InvoiceStateIssuing   = 4
	InvoiceStateFailed    = -1
	InvoiceStateAbandoned = -9
)

// 发票相关提示（用户可见）。
const (
	InvoiceMsgDisabled         = "发票功能未开启"
	InvoiceMsgConfigMissing    = "发票功能配置不完整，请联系客服"
	InvoiceMsgQuotaUnavailable = "发票平台暂时不可用，可开票金额暂不可用"
	InvoiceMsgPlatformDown     = "发票平台暂时不可用，请稍后再试"
	InvoiceMsgApplyBusy        = "发票申请正在处理，请稍后再试"
)

// ErrInvoiceConfigMissing appKey/appSecret 未配置。
var ErrInvoiceConfigMissing = errors.New(InvoiceMsgConfigMissing)

// InvoicePlatformError 发票平台业务错误（code<0 等），Message 可直接展示给用户。
type InvoicePlatformError struct {
	Code    int
	Message string
}

func (e *InvoicePlatformError) Error() string { return e.Message }

// InvoiceErrorMessage 把发票调用错误转成用户可见文案：配置缺失 / 平台业务错误原样返回，网络等其他错误统一「暂时不可用」。
func InvoiceErrorMessage(err error) string {
	var pe *InvoicePlatformError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvoiceConfigMissing):
		return InvoiceMsgConfigMissing
	case errors.As(err, &pe) && pe.Message != "":
		return pe.Message
	}
	return InvoiceMsgPlatformDown
}

// InvoiceUsername 历史平台用户名：easytv_<userId>。新开票优先用邮箱（InvoiceUsernameForUser）。
func InvoiceUsername(userId int) string {
	return "easytv_" + strconv.Itoa(userId)
}

// InvoiceUsernameForUser 开票/取令牌用的平台 username。账号是邮箱则用邮箱（与 EasyAPI 发票平台一致），否则回退 easytv_<id>。
func InvoiceUsernameForUser(userId int, username string) string {
	if model.IsEmailFormat(username) {
		return model.NormalizeEmail(username)
	}
	return InvoiceUsername(userId)
}

// InvoiceOccupiedUsernames 汇总占用时同时查邮箱与历史 easytv_<id>，避免改用户名后漏算旧票。
func InvoiceOccupiedUsernames(userId int, username string) []string {
	legacy := InvoiceUsername(userId)
	primary := InvoiceUsernameForUser(userId, username)
	if primary == legacy {
		return []string{legacy}
	}
	return []string{primary, legacy}
}

// InvoiceStateOccupied 该状态的发票是否占用可开票金额（审核中 / 开票中 / 已开票）。
func InvoiceStateOccupied(state int) bool {
	return state == InvoiceStateReviewing || state == InvoiceStateIssuing || state == InvoiceStateIssued
}

var invoiceSpecialCategoryRe = regexp.MustCompile(`专[用票]|增值税专用`)

// IsSpecialInvoiceCategory 票种是否增值税专用发票（专票必须有购方税号）。
func IsSpecialInvoiceCategory(category string) bool {
	return invoiceSpecialCategoryRe.MatchString(category)
}

// InvoiceRound2 四舍五入到分。
func InvoiceRound2(v float64) float64 {
	return decimal.NewFromFloat(v).Round(2).InexactFloat64()
}

// InvoiceAvailableAmount 可开票金额 = max(0, round2(实付 − 已开票))。
func InvoiceAvailableAmount(paid, invoiced float64) float64 {
	v := decimal.NewFromFloat(paid).Sub(decimal.NewFromFloat(invoiced)).Round(2)
	if v.IsNegative() {
		return 0
	}
	return v.InexactFloat64()
}

// NewInvoiceOutOrderNo 开票外部单号：ETI + yyyymmddHHMMSS + userId + 4 位随机数字。
func NewInvoiceOutOrderNo(userId int, now time.Time) string {
	return fmt.Sprintf("ETI%s%d%04d", now.Format("20060102150405"), userId, rand.Intn(10000))
}

// ===== 申请参数校验 =====

// InvoiceApplyRequest 用户开票申请（POST /user/invoice/apply）。
type InvoiceApplyRequest struct {
	Amount                  float64 `json:"amount"`
	Type                    string  `json:"type"` // 企业 / 个人
	Category                string  `json:"category"`
	Email                   string  `json:"email"`
	PurchaserName           string  `json:"purchaser_name"`
	PurchaserTaxpayerNumber string  `json:"purchaser_taxpayer_number"`
	PurchaserAddress        string  `json:"purchaser_address"`
	PurchaserPhone          string  `json:"purchaser_phone"`
	PurchaserBank           string  `json:"purchaser_bank"`
	PurchaserBankAccount    string  `json:"purchaser_bank_account"`
	Remark                  string  `json:"remark"`
}

// NormalizeInvoiceApply 去空白、补缺省票种并校验表单（不含可开票上限，见 CheckInvoiceAmount）。
func NormalizeInvoiceApply(req *InvoiceApplyRequest, defaultCategory string) error {
	for _, p := range []*string{&req.Type, &req.Category, &req.Email, &req.PurchaserName, &req.PurchaserTaxpayerNumber,
		&req.PurchaserAddress, &req.PurchaserPhone, &req.PurchaserBank, &req.PurchaserBankAccount, &req.Remark} {
		*p = strings.TrimSpace(*p)
	}
	if req.Category == "" {
		req.Category = defaultCategory
	}
	if req.Amount <= 0 {
		return errors.New("开票金额必须大于 0")
	}
	if d := decimal.NewFromFloat(req.Amount); !d.Equal(d.Round(2)) {
		return errors.New("开票金额最多保留两位小数")
	}
	switch req.Type {
	case InvoiceTypePersonal:
		if req.PurchaserName == "" {
			return errors.New("请填写抬头名称")
		}
	case InvoiceTypeCompany:
		if req.PurchaserName == "" {
			return errors.New("请填写抬头名称")
		}
		if req.PurchaserTaxpayerNumber == "" {
			return errors.New("企业抬头需填写纳税人识别号")
		}
	default:
		return errors.New("抬头类型只能是企业或个人")
	}
	if IsSpecialInvoiceCategory(req.Category) && req.PurchaserTaxpayerNumber == "" {
		return errors.New("增值税专用发票需填写抬头税号")
	}
	if !isInvoiceEmail(req.Email) {
		return errors.New("请填写接收发票的邮箱")
	}
	if utf8.RuneCountInString(req.Remark) > invoiceRemarkMax {
		return fmt.Errorf("备注不能超过 %d 字", invoiceRemarkMax)
	}
	return nil
}

// CheckInvoiceAmount 开票金额不超过可开票金额（按分比较，避免浮点误差）。
func CheckInvoiceAmount(amount, available float64) error {
	if decimal.NewFromFloat(amount).Round(2).GreaterThan(decimal.NewFromFloat(available).Round(2)) {
		return fmt.Errorf("开票金额不能超过可开票金额 ¥%.2f", available)
	}
	return nil
}

// isInvoiceEmail 邮箱格式：单个地址、不带显示名、域名含点。
func isInvoiceEmail(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || addr.Name != "" {
		return false
	}
	at := strings.LastIndex(s, "@")
	return at > 0 && strings.Contains(s[at+1:], ".")
}

// ===== 平台客户端 =====

// InvoiceClient 发票平台客户端。
type InvoiceClient struct {
	BaseURL   string
	AppKey    string
	AppSecret string
	HTTP      *http.Client
}

// NewInvoiceClient 按生效配置建客户端（15 秒超时）。
func NewInvoiceClient(cfg setting.InvoiceConfig) *InvoiceClient {
	return &InvoiceClient{
		BaseURL:   strings.TrimRight(cfg.ServiceUrl, "/"),
		AppKey:    cfg.AppKey,
		AppSecret: cfg.AppSecret,
		HTTP:      &http.Client{Timeout: invoiceHTTPTimeout},
	}
}

// invoiceResponse 平台统一响应。
type invoiceResponse struct {
	Code          int             `json:"code"`
	Message       string          `json:"message"`
	Content       json.RawMessage `json:"content"`
	Last          bool            `json:"last"`
	TotalElements int64           `json:"totalElements"`
}

// do 发请求并解析统一响应；HTTP 非 200 且无 code<0 message 时按网络错误处理。
func (cl *InvoiceClient) do(req *http.Request) (*invoiceResponse, error) {
	if cl.AppKey == "" || cl.AppSecret == "" {
		return nil, ErrInvoiceConfigMissing
	}
	res, err := cl.HTTP.Do(req)
	if err != nil {
		// url.Error 会带完整 URL（含 appKey/appSecret），只保留底层原因，避免密钥进日志
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("请求发票平台 %s 失败: %w", req.URL.Path, ue.Err)
		}
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var out invoiceResponse
	if err := common.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("发票平台响应解析失败（HTTP %d）: %w", res.StatusCode, err)
	}
	if out.Code < 0 {
		msg := strings.TrimSpace(out.Message)
		if msg == "" {
			msg = fmt.Sprintf("发票平台返回错误码 %d", out.Code)
		}
		return nil, &InvoicePlatformError{Code: out.Code, Message: msg}
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("发票平台返回异常状态码 %d", res.StatusCode)
	}
	return &out, nil
}

func (cl *InvoiceClient) get(path string, query url.Values) (*invoiceResponse, error) {
	if cl.AppKey == "" || cl.AppSecret == "" {
		return nil, ErrInvoiceConfigMissing
	}
	query.Set("appKey", cl.AppKey)
	query.Set("appSecret", cl.AppSecret)
	req, err := http.NewRequest(http.MethodGet, cl.BaseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return cl.do(req)
}

// InvoiceAccessToken 开票用户令牌。前端持令牌直调平台查询发票，appKey/appSecret 不下发。
type InvoiceAccessToken struct {
	AccessToken string `json:"accessToken"`
	Username    string `json:"username"`
}

// AccessToken 获取开票用户令牌（GET /access-token）。
func (cl *InvoiceClient) AccessToken(username string) (*InvoiceAccessToken, error) {
	out, err := cl.get("/access-token", url.Values{"username": {username}})
	if err != nil {
		return nil, err
	}
	var tok InvoiceAccessToken
	if out.Code != 1 || len(out.Content) == 0 || common.Unmarshal(out.Content, &tok) != nil || tok.AccessToken == "" {
		msg := strings.TrimSpace(out.Message)
		if msg == "" {
			msg = "发票平台未返回有效令牌"
		}
		return nil, &InvoicePlatformError{Code: out.Code, Message: msg}
	}
	if tok.Username == "" {
		tok.Username = username
	}
	return &tok, nil
}

// invoiceListRow 发票列表行（只取汇总所需字段）。
type invoiceListRow struct {
	State      int     `json:"state"`
	Price      float64 `json:"price"`
	Tax        float64 `json:"tax"`
	OutOrderNo string  `json:"outOrderNo"`
}

// SumOccupied 该用户在平台上占用额度的发票金额合计（price+tax，审核中 / 开票中 / 已开票），
// 分页拉 /invoices（page 从 0 开始，每页 100，最多 50 页）。平台不可达返回错误，调用方按 0 可开票处理。
func (cl *InvoiceClient) SumOccupied(username string) (float64, error) {
	total, _, err := cl.OccupiedInvoices(username)
	return total, err
}

// OccupiedInvoices 同 SumOccupied，另返回平台上已经出现的商户订单号（任意状态），
// 用来判断本地记下的「刚提交、平台还没列出来」的申请是否已同步。
func (cl *InvoiceClient) OccupiedInvoices(username string) (float64, map[string]bool, error) {
	seen := map[string]bool{}
	total := decimal.Zero
	for page := 0; page < invoiceMaxPages; page++ {
		out, err := cl.get("/invoices", url.Values{
			"username": {username},
			"page":     {strconv.Itoa(page)},
			"size":     {strconv.Itoa(invoicePageSize)},
		})
		if err != nil {
			return 0, nil, err
		}
		if out.Code == 0 || len(out.Content) == 0 { // 暂无数据
			break
		}
		var rows []invoiceListRow
		if err := common.Unmarshal(out.Content, &rows); err != nil {
			return 0, nil, fmt.Errorf("发票列表解析失败: %w", err)
		}
		if len(rows) == 0 { // content 为 null / 空数组
			break
		}
		for _, r := range rows {
			if r.OutOrderNo != "" {
				seen[r.OutOrderNo] = true
			}
			if InvoiceStateOccupied(r.State) {
				total = total.Add(decimal.NewFromFloat(r.Price)).Add(decimal.NewFromFloat(r.Tax))
			}
		}
		if out.Last { // 只认 last：平台若把每页上限压到 100 以下，按条数判断会漏算
			break
		}
	}
	return total.Round(2).InexactFloat64(), seen, nil
}

// InvoiceMakeItem 开票明细。
type InvoiceMakeItem struct {
	No      string `json:"no"`      // 税收商品编码
	TaxRate string `json:"taxRate"` // 税率
	Sum     string `json:"sum"`     // 含税金额，两位小数
	Name    string `json:"name"`    // 品名
}

// InvoiceMakeRequest POST /invoice/make 请求体。
type InvoiceMakeRequest struct {
	AccessToken             string            `json:"accessToken"`
	Username                string            `json:"username"`
	Email                   string            `json:"email"`
	Type                    string            `json:"type"`
	Category                string            `json:"category"`
	OutOrderNo              string            `json:"outOrderNo"`
	PurchaserName           string            `json:"purchaserName"`
	PurchaserTaxpayerNumber string            `json:"purchaserTaxpayerNumber"`
	PurchaserAddress        string            `json:"purchaserAddress"`
	PurchaserPhone          string            `json:"purchaserPhone"`
	PurchaserBank           string            `json:"purchaserBank"`
	PurchaserBankAccount    string            `json:"purchaserBankAccount"`
	Remark                  string            `json:"remark"`
	Items                   []InvoiceMakeItem `json:"items"`
}

// BuildInvoiceMakeRequest 由申请参数 + 配置拼平台开票请求（单条明细，sum 为含税金额两位小数）。
func BuildInvoiceMakeRequest(cfg setting.InvoiceConfig, req InvoiceApplyRequest, accessToken, username, outOrderNo string) InvoiceMakeRequest {
	return InvoiceMakeRequest{
		AccessToken:             accessToken,
		Username:                username,
		Email:                   req.Email,
		Type:                    req.Type,
		Category:                req.Category,
		OutOrderNo:              outOrderNo,
		PurchaserName:           req.PurchaserName,
		PurchaserTaxpayerNumber: req.PurchaserTaxpayerNumber,
		PurchaserAddress:        req.PurchaserAddress,
		PurchaserPhone:          req.PurchaserPhone,
		PurchaserBank:           req.PurchaserBank,
		PurchaserBankAccount:    req.PurchaserBankAccount,
		Remark:                  req.Remark,
		Items: []InvoiceMakeItem{{
			No:      cfg.ItemNo,
			TaxRate: cfg.TaxRate,
			Sum:     decimal.NewFromFloat(req.Amount).StringFixed(2),
			Name:    cfg.ItemName,
		}},
	}
}

// MakeInvoice 提交开票（POST /invoice/make），返回平台 content 原文。
func (cl *InvoiceClient) MakeInvoice(req InvoiceMakeRequest) (json.RawMessage, error) {
	if cl.AppKey == "" || cl.AppSecret == "" {
		return nil, ErrInvoiceConfigMissing
	}
	body, err := common.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, cl.BaseURL+"/invoice/make", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	out, err := cl.do(httpReq)
	if err != nil {
		return nil, err
	}
	if out.Code != 1 {
		msg := strings.TrimSpace(out.Message)
		if msg == "" {
			msg = "发票平台未受理开票申请"
		}
		return nil, &InvoicePlatformError{Code: out.Code, Message: msg}
	}
	if len(out.Content) == 0 {
		return json.RawMessage("null"), nil
	}
	return out.Content, nil
}

// ===== 可开票金额 =====

// InvoiceQuota 用户发票额度。Error 非空时 Available 固定为 0（fail-closed）。
type InvoiceQuota struct {
	Paid      float64
	Invoiced  float64
	Available float64
	Error     string
}

// ComputeInvoiceQuota 由实付合计与平台占用合计算额度（占用查询注入便于测试）。
// 平台出错：可开票按 0，配置缺失提示联系客服，其余提示平台暂不可用。
func ComputeInvoiceQuota(paid float64, username string, occupied func(string) (float64, error)) InvoiceQuota {
	q := InvoiceQuota{Paid: InvoiceRound2(paid)}
	invoiced, err := occupied(username)
	if err != nil {
		if errors.Is(err, ErrInvoiceConfigMissing) {
			q.Error = InvoiceMsgConfigMissing
		} else {
			q.Error = InvoiceMsgQuotaUnavailable
			common.SysError(fmt.Sprintf("invoice sum occupied failed: username=%s err=%v", username, err))
		}
		return q
	}
	q.Invoiced = InvoiceRound2(invoiced)
	q.Available = InvoiceAvailableAmount(paid, invoiced)
	return q
}

// GetInvoiceQuota 查用户可开票金额；error 仅为数据库错误。
// paid = 套餐/积分包实付 + 无对应订单的纯充值；占用按邮箱与历史 easytv_<id> 合计。
func GetInvoiceQuota(cl *InvoiceClient, userId int) (InvoiceQuota, error) {
	paidOrders, err := model.SumUserInvoiceablePaid(userId)
	if err != nil {
		return InvoiceQuota{}, err
	}
	paidTopup, err := model.SumUserInvoiceableTopup(userId)
	if err != nil {
		return InvoiceQuota{}, err
	}
	paid := paidOrders + paidTopup
	account := ""
	if u, uerr := model.GetUserById(userId); uerr == nil && u != nil {
		account = u.Username
	}
	names := InvoiceOccupiedUsernames(userId, account)
	occupied := func(_ string) (float64, error) {
		total := decimal.Zero
		seen := map[string]bool{}
		for _, name := range names {
			part, partSeen, err := cl.OccupiedInvoices(name)
			if err != nil {
				return 0, err
			}
			total = total.Add(decimal.NewFromFloat(part))
			for no := range partSeen {
				seen[no] = true
			}
		}
		// 刚提交的申请平台可能还没列出来：本地记着的金额也算已开票，防止马上再申请重复开
		return total.Round(2).InexactFloat64() + invoicePendingSum(userId, seen), nil
	}
	q := ComputeInvoiceQuota(paid, InvoiceUsernameForUser(userId, account), occupied)
	if q.Error == "" {
		if err := ReconcileUserRechargeInvoices(userId, q.Invoiced); err != nil {
			common.SysError(fmt.Sprintf("invoice reconcile recharge failed: user=%d err=%v", userId, err))
		}
	}
	return q, nil
}

// ===== 刚提交、平台尚未列出的申请 =====

const (
	invoicePendingTTL    = 24 * time.Hour
	invoicePendingPrefix = "easytv:invoicepending:"
)

type invoicePendingEntry struct {
	Amount float64 `json:"amount"`
	At     int64   `json:"at"`
}

var (
	invoicePendingMu    sync.Mutex
	invoicePendingLocal = map[int]map[string]invoicePendingEntry{}
)

// RechargeInvoiceRow 开票 FIFO 对账用的充值单视图。
type RechargeInvoiceRow struct {
	Id                int
	Original          float64 // 实付 − 已退
	InvoiceableAmount float64 // 当前可开票余额
	Invoiced          bool
}

// AllocateInvoiceFIFO 把 amount 按入账顺序扣到各充值单的可开票余额上；扣到 0 则标记已开票。
func AllocateInvoiceFIFO(rows []RechargeInvoiceRow, amount float64) []RechargeInvoiceRow {
	remain := decimal.NewFromFloat(amount).Round(2)
	if remain.LessThanOrEqual(decimal.Zero) {
		return nil
	}
	var changed []RechargeInvoiceRow
	for i := range rows {
		if rows[i].Invoiced {
			continue
		}
		avail := decimal.NewFromFloat(rows[i].InvoiceableAmount).Round(2)
		if avail.LessThanOrEqual(decimal.Zero) {
			continue
		}
		take := avail
		if take.GreaterThan(remain) {
			take = remain
		}
		leftover := avail.Sub(take)
		rows[i].InvoiceableAmount = leftover.InexactFloat64()
		rows[i].Invoiced = leftover.LessThanOrEqual(decimal.Zero)
		changed = append(changed, rows[i])
		remain = remain.Sub(take)
		if remain.LessThanOrEqual(decimal.Zero) {
			break
		}
	}
	return changed
}

func invoiceAmountsEqual(a, b float64) bool {
	return decimal.NewFromFloat(a).Round(2).Equal(decimal.NewFromFloat(b).Round(2))
}

// ReconcileInvoiceFIFO 从每单原始可开票额出发，按平台占用总额重算剩余与已开票标记。
// 作废 / 红冲后占用变少时，会把余额还回去。
func ReconcileInvoiceFIFO(rows []RechargeInvoiceRow, occupied float64) []RechargeInvoiceRow {
	remain := decimal.NewFromFloat(occupied).Round(2)
	if remain.IsNegative() {
		remain = decimal.Zero
	}
	var changed []RechargeInvoiceRow
	for i := range rows {
		orig := decimal.NewFromFloat(rows[i].Original).Round(2)
		leftover := orig
		invoiced := false
		if orig.GreaterThan(decimal.Zero) && remain.GreaterThan(decimal.Zero) {
			take := orig
			if take.GreaterThan(remain) {
				take = remain
			}
			leftover = orig.Sub(take)
			remain = remain.Sub(take)
			invoiced = leftover.LessThanOrEqual(decimal.Zero)
		}
		nextAmt := leftover.InexactFloat64()
		if invoiceAmountsEqual(nextAmt, rows[i].InvoiceableAmount) && invoiced == rows[i].Invoiced {
			continue
		}
		rows[i].InvoiceableAmount = nextAmt
		rows[i].Invoiced = invoiced
		changed = append(changed, rows[i])
	}
	return changed
}

// ReconcileUserRechargeInvoices 按占用总额重写该用户成功充值单的可开票余额（失败只应由调用方记日志）。
func ReconcileUserRechargeInvoices(userId int, occupied float64) error {
	rows, err := model.ListUserSuccessRecharges(userId)
	if err != nil {
		return err
	}
	view := make([]RechargeInvoiceRow, len(rows))
	for i, r := range rows {
		view[i] = RechargeInvoiceRow{
			Id:                r.Id,
			Original:          model.RechargeOriginalInvoiceable(r),
			InvoiceableAmount: r.InvoiceableAmount,
			Invoiced:          r.Invoiced,
		}
	}
	for _, item := range ReconcileInvoiceFIFO(view, occupied) {
		if err := model.UpdateRechargeInvoice(item.Id, item.InvoiceableAmount, item.Invoiced); err != nil {
			return err
		}
	}
	return nil
}

// AllocateInvoiceToRecharges 开票成功后按「当前占用 + 本次金额」重对充值单。
func AllocateInvoiceToRecharges(userId int, occupiedAfter float64) error {
	return ReconcileUserRechargeInvoices(userId, occupiedAfter)
}

// RecordInvoicePending 申请提交成功后记下（Redis hash，24 小时；无 Redis 时存内存）。
func RecordInvoicePending(userId int, outOrderNo string, amount float64) {
	entry := invoicePendingEntry{Amount: amount, At: time.Now().Unix()}
	if common.RedisClient != nil {
		key := invoicePendingPrefix + strconv.Itoa(userId)
		data, _ := common.Marshal(entry)
		_ = common.RedisClient.HSet(common.RedisCtx, key, outOrderNo, string(data)).Err()
		_ = common.RedisClient.Expire(common.RedisCtx, key, invoicePendingTTL).Err()
		return
	}
	invoicePendingMu.Lock()
	defer invoicePendingMu.Unlock()
	if invoicePendingLocal[userId] == nil {
		invoicePendingLocal[userId] = map[string]invoicePendingEntry{}
	}
	invoicePendingLocal[userId][outOrderNo] = entry
}

// invoicePendingSum 本地记着、平台列表里还没出现的申请金额；已出现或超过 24 小时的顺手清掉。
func invoicePendingSum(userId int, seen map[string]bool) float64 {
	entries := map[string]invoicePendingEntry{}
	key := invoicePendingPrefix + strconv.Itoa(userId)
	if common.RedisClient != nil {
		raw, err := common.RedisClient.HGetAll(common.RedisCtx, key).Result()
		if err != nil {
			return 0
		}
		for no, v := range raw {
			var e invoicePendingEntry
			if common.UnmarshalJsonStr(v, &e) == nil {
				entries[no] = e
			}
		}
	} else {
		invoicePendingMu.Lock()
		for no, e := range invoicePendingLocal[userId] {
			entries[no] = e
		}
		invoicePendingMu.Unlock()
	}
	sum, stale := pendingNotSeen(entries, seen, time.Now())
	if len(stale) > 0 {
		if common.RedisClient != nil {
			_ = common.RedisClient.HDel(common.RedisCtx, key, stale...).Err()
		} else {
			invoicePendingMu.Lock()
			for _, no := range stale {
				delete(invoicePendingLocal[userId], no)
			}
			invoicePendingMu.Unlock()
		}
	}
	return sum
}

// pendingNotSeen 纯函数：未在平台出现且未过期的金额合计，以及可以清掉的订单号。
func pendingNotSeen(entries map[string]invoicePendingEntry, seen map[string]bool, now time.Time) (float64, []string) {
	total := decimal.Zero
	var stale []string
	for no, e := range entries {
		if seen[no] || now.Unix()-e.At > int64(invoicePendingTTL.Seconds()) {
			stale = append(stale, no)
			continue
		}
		total = total.Add(decimal.NewFromFloat(e.Amount))
	}
	return total.Round(2).InexactFloat64(), stale
}

// ===== 每用户申请锁 =====

var (
	invoiceLocalLocksMu sync.Mutex
	invoiceLocalLocks   = map[int]bool{}
)

// invoiceUnlockScript 只删自己持有的锁（值相等才删），避免 TTL 过期后误删别人的锁。
const invoiceUnlockScript = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`

// LockInvoiceApply 抢用户开票锁（不阻塞）：Redis 可用时 SET NX 30 秒，否则进程内锁。
// 抢到返回释放函数；已被占用返回 nil,false。Redis 出错时退回进程内锁。
func LockInvoiceApply(userId int) (func(), bool) {
	if common.RedisClient != nil {
		key := fmt.Sprintf("easytv:invoicelock:%d", userId)
		token := common.GetRandomString(16)
		ok, err := common.RedisClient.SetNX(common.RedisCtx, key, token, invoiceLockTTL).Result()
		if err == nil {
			if !ok {
				return nil, false
			}
			return func() {
				if err := common.RedisClient.Eval(common.RedisCtx, invoiceUnlockScript, []string{key}, token).Err(); err != nil {
					common.SysError("invoice unlock failed: " + err.Error())
				}
			}, true
		}
		common.SysError("invoice lock failed, fallback to local: " + err.Error())
	}
	invoiceLocalLocksMu.Lock()
	defer invoiceLocalLocksMu.Unlock()
	if invoiceLocalLocks[userId] {
		return nil, false
	}
	invoiceLocalLocks[userId] = true
	return func() {
		invoiceLocalLocksMu.Lock()
		delete(invoiceLocalLocks, userId)
		invoiceLocalLocksMu.Unlock()
	}, true
}
