package common

// 平台品牌名。
var SystemName = "EasyTV"

// Version 服务版本号，可通过 VERSION 环境变量覆盖。
var Version = "1.0.0"

// QuotaPerUnit 每 1 元人民币对应的额度（quota）倍数。参考 easyapi 体系换算关系。
var QuotaPerUnit = 500 * 1000.0

// RequestIdKey 请求 id 在 context 与响应头中的键名（复刻 easyapi 日志体系）。
const RequestIdKey = "X-EasyAPI-Request-Id"

// 通用请求头前缀。
const HeaderPrefix = "EasyTV"

// 认证请求头：标识当前用户 id，配合 Authorization Bearer access_token 校验。
const HeaderEasyTVUser = "EasyTV-User"
