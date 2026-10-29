package common

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// SubMail 短信模板发送（API V4，sms/xsend）。
// 签名规则见 https://www.mysubmail.com/documents/nUriM2：
// sign_type=md5/sha1 时，取服务端时间戳，signature = 算法(appid + appkey + 参数按 key 排序拼接 + appid + appkey)，小写 hex。
// sign_version=2 时 vars 不参与签名计算；移植自 ic-conference-api 的 SubmailUtil.java。

const (
	submailSignTypeMd5  = "md5"
	submailSignTypeSha1 = "sha1"
	submailSignVersion  = "2"
)

var smsHttpClient = &http.Client{Timeout: 15 * time.Second}

// SmsConfig 一份完整的短信配置。整体原子替换：定时同步写、发短信的请求读，
// 不会读到「新 AppId + 旧 AppKey」这种半新半旧的组合。
type SmsConfig struct {
	Enabled  bool
	Url      string
	AppId    string
	AppKey   string
	SignType string
	Project  string
}

var smsConfig atomic.Pointer[SmsConfig]

// SmsEnvConfig 环境变量给出的基线配置（InitEnv 之后不变）。
func SmsEnvConfig() SmsConfig {
	return SmsConfig{
		Enabled:  SmsEnabled,
		Url:      SubmailUrl,
		AppId:    SubmailAppId,
		AppKey:   SubmailAppKey,
		SignType: SubmailSignType,
		Project:  SubmailSmsProject,
	}
}

// SetSmsConfig 替换当前生效的短信配置。
func SetSmsConfig(cfg SmsConfig) {
	smsConfig.Store(&cfg)
}

// CurrentSmsConfig 当前生效的短信配置；DB 尚未加载时就是 env 基线。
func CurrentSmsConfig() SmsConfig {
	if cfg := smsConfig.Load(); cfg != nil {
		return *cfg
	}
	return SmsEnvConfig()
}

// SendSMS 通过短信模板（SubmailSmsProject）发送短信，vars 为模板文本变量（key 对应模板 @var(key)）。
// 短信签名使用模板默认签名，不传 sms_signature。
// SmsEnabled=false 时仅打印日志（联调模式，与 SendEmail 行为一致）。
// SmsTestAccounts 测试手机号 → 固定验证码（SMS_TEST_ACCOUNTS="13800000000:123456,13900000000:654321"）。
// 只给本地联调 / 演示用：命中的号码不真发短信，直接认这个验证码。线上服务器不要配置。
var SmsTestAccounts = map[string]string{}

// ParseSmsTestAccounts 解析 SMS_TEST_ACCOUNTS；格式不对的项跳过，验证码至少 6 位。
func ParseSmsTestAccounts(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		mobile, code, ok := strings.Cut(strings.TrimSpace(part), ":")
		mobile, code = strings.TrimSpace(mobile), strings.TrimSpace(code)
		if !ok || len(mobile) != 11 || len(code) < 6 {
			continue
		}
		out[mobile] = code
	}
	return out
}

func SendSMS(phone string, vars map[string]string) error {
	cfg := CurrentSmsConfig()
	if !cfg.Enabled {
		SysLog(fmt.Sprintf("[SMS][联调模式] to=%s project=%s vars=%v", phone, cfg.Project, vars))
		return nil
	}
	if cfg.AppId == "" || cfg.AppKey == "" {
		return fmt.Errorf("SUBMAIL_APP_ID / SUBMAIL_APP_KEY 未配置")
	}
	url := cfg.Url + "/sms/xsend"

	// 组合请求数据；project 参与签名计算，vars 与 signature 不参与（sign_version=2，vars 在签名后追加）
	requestData := map[string]string{
		"appid":   cfg.AppId,
		"to":      phone,
		"project": cfg.Project,
	}
	signType := cfg.SignType
	if signType == "" {
		signType = submailSignTypeMd5
	}
	if signType == submailSignTypeMd5 || signType == submailSignTypeSha1 {
		timestamp, err := getSubmailTimestamp(cfg.Url)
		if err != nil {
			return err
		}
		requestData["timestamp"] = timestamp
		requestData["sign_type"] = signType
		requestData["sign_version"] = submailSignVersion
		signStr := cfg.AppId + cfg.AppKey + formatSubmailParams(requestData) + cfg.AppId + cfg.AppKey
		requestData["signature"] = encodeSubmailSign(signType, signStr)
	} else {
		requestData["signature"] = cfg.AppKey
	}
	// vars 序列化为 JSON 字符串（sign_version=2 时不参与签名计算）
	varsJSON, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	requestData["vars"] = string(varsJSON)

	body, err := json.Marshal(requestData)
	if err != nil {
		return err
	}
	// 日志不带 signature：normal 签名模式下它就是 AppKey 明文
	SysLog(fmt.Sprintf("=====请求SubMail发送短信===== to=%s url=%s project=%s", phone, url, cfg.Project))

	// Content-Type 必须带 charset=UTF-8，否则中文按 ISO-8859-1 编码变乱码，导致签名无法识别返回 119
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	resp, err := smsHttpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	SysLog(fmt.Sprintf("=====SubMail发送短信响应===== %s", string(responseBody)))

	var jsonResp struct {
		Status string `json:"status"`
		Code   *int   `json:"code"`
		Msg    string `json:"msg"`
	}
	if err := json.Unmarshal(responseBody, &jsonResp); err != nil {
		return fmt.Errorf("短信发送失败：响应解析失败")
	}
	if jsonResp.Status == "success" {
		return nil
	}
	reason := ""
	if jsonResp.Code != nil {
		reason = submailErrorCodeMap[*jsonResp.Code]
	}
	if reason == "" {
		reason = jsonResp.Msg
	}
	if reason == "" {
		reason = "短信发送失败"
	}
	return fmt.Errorf("%s", reason)
}

// getSubmailTimestamp 获取服务端时间戳（签名用）。
func getSubmailTimestamp(baseUrl string) (string, error) {
	url := baseUrl + "/service/timestamp"
	resp, err := smsHttpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	// timestamp 可能返回数字或字符串，用 RawMessage 保留原始字面量（等价于 fastjson 的 getString）
	var jsonResp struct {
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if err := json.Unmarshal(responseBody, &jsonResp); err != nil {
		return "", err
	}
	timestamp := strings.Trim(string(jsonResp.Timestamp), `"`)
	if timestamp == "" {
		return "", fmt.Errorf("获取SubMail时间戳失败")
	}
	return timestamp, nil
}

// formatSubmailParams 请求参数按 key 排序后拼接为 key=value&key=value 形式（等价于 Java TreeMap 自然序）。
func formatSubmailParams(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, key := range keys {
		if i > 0 {
			sb.WriteString("&")
		}
		sb.WriteString(key)
		sb.WriteString("=")
		sb.WriteString(data[key])
	}
	return sb.String()
}

// encodeSubmailSign 按指定算法（md5/sha1）计算签名字符串的小写 hex。
func encodeSubmailSign(algorithm string, str string) string {
	var digest []byte
	switch algorithm {
	case submailSignTypeSha1:
		sum := sha1.Sum([]byte(str))
		digest = sum[:]
	default:
		sum := md5.Sum([]byte(str))
		digest = sum[:]
	}
	return hex.EncodeToString(digest)
}

// submailErrorCodeMap SubMail 错误码中文映射（见 https://www.mysubmail.com/documents/rK2yh3）。
var submailErrorCodeMap = map[int]string{
	101: "不正确的APP ID",
	102: "此应用已被禁用，请至submail > 应用集成 > 应用页面开启此应用",
	103: "未启用的开发者，此应用的开发者身份未验证，请更新您的开发者资料",
	104: "此开发者未通过验证或此开发者资料发生更改，请至应用集成页面更新你的开发者资料",
	105: "此账户已过期",
	106: "此账户已被禁用",
	107: "sign_type（验证模式）必须设置为MD5或SHA1或normal（密匙模式）",
	108: "signature参数无效",
	109: "appkey无效",
	110: "sign_type错误",
	111: "空的signature参数",
	112: "应用的订阅与退订功能已禁用",
	113: "请求的APPID已设置IP白名单，您的IP不在此白名单范围",
	114: "该手机号码在账户黑名单中，已被屏蔽",
	115: "该手机号码请求超限",
	116: "签名错误，该签名已被其他应用使用并已申请固定签名",
	117: "该模板已失效，短信模板签名与固定签名不一致或你的账户已取消固签，请联系SUBMAIL管理员",
	118: "该模板已失效，请联系SUBMAIL管理员",
	119: "您不具备使用该API的权限，请联系SUBMAIL管理员",
	120: "模板已失效",
	126: "短信签名还未报备成功",
	127: "短信签名已存在，无需创建新签名",
	151: "错误的UNIX时间戳",
	152: "错误的UNIX时间戳，请将请求UNIX时间戳至发送API的过程控制在6秒以内",
	154: "appid下无可用签名",
	201: "未知的addressbook模式",
	202: "错误的收件人地址",
	203: "错误的收件人地址，如果你正在使用addressbook，你所标记的地址簿不包含任何联系人",
	251: "错误的收件人地址",
	252: "错误的收件人地址，如果你正在使用addressbook模式，你所标记的地址簿不包含任何联系人",
	253: "此联系人已退订你的短信系统",
	305: "没有填写项目标记",
	306: "无效的项目标记",
	307: "错误的json格式，请检查vars和links参数",
	310: "tag参数长度不能超过32个字符",
	401: "短信签名不能为空",
	402: "请将短信签名控制在40个字符以内",
	403: "短信正文不能为空",
	404: "请将短信内容（加上签名）控制在1000个字符以内",
	405: "依据当地法律法规，以下'$var'词或短语不能出现在短信中",
	406: "项目标记不能为空",
	407: "无效的项目标记",
	408: "你不能向此联系人或此地址簿中包含的联系人发送完全相同的短信",
	409: "尝试发送的短信项目正在审核中，请稍候再试",
	410: "multi参数无效",
	411: "您必须为每条短信模板提交一个短信签名，且该签名必须使用全角大括号【】包括起来，请将短信签名的字数控制在2至10字符以内（括号不计算字符数）",
	412: "请将短信签名的字数控制在10字符以内（括号不计算字符数）",
	413: "请将短信签名的字数控制在2到10个字符之间（括号不计算字符数）",
	414: "请提交短信正文",
	415: "请将短信正文的字数控制在1000个字符以内",
	416: "请将短信标题的字数控制在64个字符以内",
	417: "请提交需要更新的模板ID",
	418: "尝试更新的模板不存在",
	419: "短信正文不能为空",
	420: "找不到可匹配的模板",
	422: "请控制您的模板长度在255个字符内",
	501: "错误的目标地址簿标识",
	901: "你今日的发送配额已用尽，如需提高发送配额，请至submail > 应用集成 > 应用页面开启更多发送配额",
	903: "您的短信发送许可已用尽或您的余额不支持本次的请求数量，如需继续发送，请至submail.cn > 商店页面购买更多发送许可后重试",
	904: "您的账户余额已用尽或您的余额不支持本次的请求数量，如需继续充值，请至submail.cn > 商店页面购买更多发送许可后重试",
	905: "您的账户余额已用尽或您的余额不支持本次的请求数量，如需继续充值，请至submail.cn > 商店页面购买更多发送许可后重试",
}
