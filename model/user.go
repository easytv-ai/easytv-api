package model

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"

	"gorm.io/gorm"
)

const UserNameMaxLength = 50 // username 即账号邮箱，需容纳邮箱长度

// User if you add sensitive fields, don't forget to clean them in setupLogin function.
// Otherwise, the sensitive information will be saved on local storage in plain text!
// 字段与 easyapi 完全一致（含第三方登录 ID 列、推广/分销/Stripe 字段），见 easyapi-ai-api model/user.go。
type User struct {
	Id int `json:"id" gorm:"comment:用户ID"`
	// Username 即账号邮箱（邮箱格式，小写归一化存储）；微信/OAuth 待绑定用户及 root 为历史生成名
	Username         string         `json:"username" gorm:"type:varchar(191);unique;index;comment:账号邮箱（小写归一化）" validate:"max=50"`
	Password         string         `json:"password" gorm:"type:varchar(200);not null;comment:密码哈希（bcrypt）" validate:"min=8,max=20"`
	OriginalPassword string         `json:"original_password" gorm:"-:all"` // this field is only for Password change verification, don't save it to database!
	Nickname         string         `json:"nickname" gorm:"type:varchar(191);column:nickname;index;comment:昵称" validate:"max=20"`
	Mobile           string         `json:"mobile" gorm:"type:varchar(20);index;default:'';comment:绑定手机号（短信注册用户 username 也是手机号）"`
	Role             int            `json:"role" gorm:"type:int;default:1;comment:角色：1=普通用户 10=管理员 100=超级管理员"` // admin, common
	Status           int            `json:"status" gorm:"type:int;default:1;comment:状态：1=启用 2=禁用"`             // enabled, disabled
	GitHubId         string         `json:"github_id" gorm:"type:varchar(191);column:github_id;index;comment:GitHub OAuth 绑定 ID"`
	DiscordId        string         `json:"discord_id" gorm:"type:varchar(191);column:discord_id;index;comment:Discord OAuth 绑定 ID"`
	OidcId           string         `json:"oidc_id" gorm:"type:varchar(191);column:oidc_id;index;comment:OIDC OAuth 绑定 ID"`
	WeChatId         string         `json:"wechat_id" gorm:"type:varchar(191);column:wechat_id;index;comment:微信 unionid"`
	WeChatNickname   string         `json:"wechat_nickname" gorm:"type:varchar(191);column:wechat_nickname;comment:微信昵称"`
	TelegramId       string         `json:"telegram_id" gorm:"type:varchar(191);column:telegram_id;index;comment:Telegram 绑定 ID"`
	VerificationCode string         `json:"verification_code" gorm:"-:all"`                                                         // this field is only for Email verification, don't save it to database!
	PartnerCode      string         `json:"pcode" gorm:"-:all"`                                                                     // 注册时携带的伙伴邀请码，仅传参不入库
	AccessToken      *string        `json:"access_token" gorm:"type:char(32);column:access_token;uniqueIndex;comment:控制台/API 会话令牌"` // this token is for system management
	Quota            int            `json:"quota" gorm:"type:int;default:0;comment:余额（额度）"`
	UsedQuota        int            `json:"used_quota" gorm:"type:int;default:0;column:used_quota;comment:已用额度"` // used quota
	RequestCount     int            `json:"request_count" gorm:"type:int;default:0;comment:请求次数"`                // request number
	Group            string         `json:"group" gorm:"type:varchar(64);default:'default';comment:分组"`
	AffCode          string         `json:"aff_code" gorm:"type:varchar(32);column:aff_code;uniqueIndex;comment:邀请码"`
	AffCount         int            `json:"aff_count" gorm:"type:int;default:0;column:aff_count;comment:邀请人数"`
	AffQuota         int            `json:"aff_quota" gorm:"type:int;default:0;column:aff_quota;comment:邀请剩余额度"`
	AffHistoryQuota  int            `json:"aff_history_quota" gorm:"type:int;default:0;column:aff_history;comment:邀请历史额度"`
	InviterId        int            `json:"inviter_id" gorm:"type:int;column:inviter_id;index;comment:邀请人"`
	InviteBlocked    bool           `json:"invite_blocked" gorm:"default:false;comment:冻结邀请奖励（后台反作弊）"`
	Source           string         `json:"source" gorm:"type:varchar(40);index;default:'';comment:注册来源 utm_source"`
	Medium           string         `json:"medium" gorm:"type:varchar(40);default:'';comment:utm_medium"`
	Campaign         string         `json:"campaign" gorm:"type:varchar(64);default:'';comment:utm_campaign"`
	BonusCode        string         `json:"bonus_code" gorm:"type:varchar(32);index;default:'';comment:注册时携带的口令"`
	DeletedAt        gorm.DeletedAt `gorm:"index;comment:软删除时间"`
	LinuxDOId        string         `json:"linux_do_id" gorm:"type:varchar(191);column:linux_do_id;index;comment:LinuxDO OAuth 绑定 ID"`
	Setting          string         `json:"setting" gorm:"type:text;column:setting;comment:用户设置 JSON"`
	Remark           string         `json:"remark,omitempty" gorm:"type:varchar(255);comment:备注" validate:"max=255"`
	Identity         string         `json:"identity" gorm:"type:varchar(64);default:'';index;comment:运营身份 easytv_official/enterprise/easytv_teacher/easytv_teaching_assistant"`
	PartnerId        int            `json:"partner_id" gorm:"index;default:0;comment:归属伙伴缓存列（真相在 partner_customer）"`
	BillingDiscount  float64        `json:"billing_discount" gorm:"type:decimal(6,4);default:1;comment:伙伴折扣缓存，仅 partner_id>0 时生效"`
	StripeCustomer   string         `json:"stripe_customer" gorm:"type:varchar(64);column:stripe_customer;index;comment:Stripe 客户 ID"`
	CreatedAt        int64          `json:"created_at" gorm:"autoCreateTime;column:created_at;comment:创建时间（Unix 秒）"`
	LastLoginAt      int64          `json:"last_login_at" gorm:"default:0;column:last_login_at;comment:最近登录时间"`
}

func (User) TableName() string { return "user" }

// ErrEmailAlreadyTaken 邮箱已被占用。
var ErrEmailAlreadyTaken = errors.New("email already taken")

// ===== 登录/找回 =====

// ValidateAndFill 校验用户名(邮箱)与密码，并校验用户状态；校验失败返回关闭的错误。
func (user *User) ValidateAndFill() (err error) {
	password := user.Password
	username := strings.TrimSpace(user.Username)
	if username == "" || password == "" {
		return common.ErrInvalidCredentials
	}
	if IsEmailFormat(username) {
		username = NormalizeEmail(username)
	}
	err = DB.Where("username = ?", username).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.ErrInvalidCredentials
		}
		return fmt.Errorf("%w: %v", common.ErrDatabase, err)
	}
	if user.Password == "" {
		return common.ErrInvalidCredentials
	}
	okay := common.ValidatePasswordAndHash(password, user.Password)
	if !okay || user.Status != constant.UserStatusEnabled {
		return common.ErrInvalidCredentials
	}
	return nil
}

// FillUserById 按主键加载用户。
func (user *User) FillUserById() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	return DB.Where("id = ?", user.Id).First(user).Error
}

// FillUserByWeChatId 按微信 unionid 加载用户。
func (user *User) FillUserByWeChatId() error {
	if user.WeChatId == "" {
		return errors.New("WeChat id 为空！")
	}
	return DB.Where("wechat_id = ?", user.WeChatId).First(user).Error
}

// FillUserByGitHubId 按 GitHub ID 加载用户。
func (user *User) FillUserByGitHubId() error {
	if user.GitHubId == "" {
		return errors.New("GitHub id 为空！")
	}
	return DB.Where("github_id = ?", user.GitHubId).First(user).Error
}

// FillUserByDiscordId 按 Discord ID 加载用户。
func (user *User) FillUserByDiscordId() error {
	if user.DiscordId == "" {
		return errors.New("Discord id 为空！")
	}
	return DB.Where("discord_id = ?", user.DiscordId).First(user).Error
}

// FillUserByOidcId 按 OIDC ID 加载用户。
func (user *User) FillUserByOidcId() error {
	if user.OidcId == "" {
		return errors.New("OIDC id 为空！")
	}
	return DB.Where("oidc_id = ?", user.OidcId).First(user).Error
}

// FillUserByTelegramId 按 Telegram ID 加载用户。
func (user *User) FillUserByTelegramId() error {
	if user.TelegramId == "" {
		return errors.New("Telegram id 为空！")
	}
	return DB.Where("telegram_id = ?", user.TelegramId).First(user).Error
}

// FillUserByLinuxDOId 按 LinuxDO ID 加载用户。
func (user *User) FillUserByLinuxDOId() error {
	if user.LinuxDOId == "" {
		return errors.New("LinuxDO id 为空！")
	}
	return DB.Where("linux_do_id = ?", user.LinuxDOId).First(user).Error
}

// FillUserByEmail 按邮箱(username)加载用户。
func (user *User) FillUserByEmail() error {
	if user.Username == "" {
		return errors.New("邮箱为空！")
	}
	return DB.Where("username = ?", NormalizeEmail(user.Username)).First(user).Error
}

// FillUserByMobile 按手机号加载用户（手机号直接存于 username 列，与 easyapi 表结构一致）。
func (user *User) FillUserByMobile() error {
	return DB.Where("username = ? OR mobile = ?", user.Username, user.Username).First(user).Error
}

// ===== 创建/更新 =====

// Insert 创建一条用户记录。aff_code 列有唯一索引，未设置时生成随机邀请码，避免空串重复冲突（与 easyapi 一致）。
func (user *User) Insert() error {
	if user.AffCode == "" {
		user.AffCode = NewAffCode()
	}
	return DB.Create(user).Error
}

// NewAffCode 生成 6 位大写字母数字邀请码并保证唯一（去掉易混淆的 0/O/1/I）。
func NewAffCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for i := 0; i < 20; i++ {
		b := make([]byte, 6)
		for j := range b {
			b[j] = alphabet[rand.Intn(len(alphabet))]
		}
		code := string(b)
		var n int64
		DB.Model(&User{}).Where("aff_code = ?", code).Count(&n)
		if n == 0 {
			return code
		}
	}
	return strings.ToUpper(common.GetRandomString(8))
}

// GetUserIdByAffCode 邀请码 → 用户 ID（不区分大小写），不存在返回 0。
func GetUserIdByAffCode(code string) int {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return 0
	}
	var id int
	DB.Model(&User{}).Select("id").Where("aff_code = ? AND deleted_at IS NULL", code).Scan(&id)
	return id
}

// Update 全量更新用户。
func (user *User) Update() error {
	return DB.Save(user).Error
}

// ===== access_token =====

// NormalizeUserAccessToken 去掉 Bearer 前缀。
func NormalizeUserAccessToken(token string) string {
	token = strings.TrimSpace(token)
	if len(token) >= 7 && strings.EqualFold(token[:7], "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	return token
}

// ValidateAccessToken 按 access_token 加载用户。
func ValidateAccessToken(token string) (*User, error) {
	token = NormalizeUserAccessToken(token)
	if token == "" {
		return nil, nil
	}
	user := &User{}
	err := DB.Where("access_token = ?", token).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %v", common.ErrDatabase, err)
	}
	return user, nil
}

// UpdateUserAccessToken 轮换用户 access_token。
func UpdateUserAccessToken(id int, token string) error {
	if id == 0 {
		return errors.New("id 为空！")
	}
	return DB.Model(&User{}).Where("id = ?", id).Update("access_token", token).Error
}

// EnsureUserAccessToken 返回用户 access_token，缺失时生成一个新的。
func EnsureUserAccessToken(userId int) (string, error) {
	user, err := GetUserById(userId)
	if err != nil {
		return "", err
	}
	if token := user.GetAccessToken(); token != "" {
		return token, nil
	}
	for i := 0; i < 5; i++ {
		key, genErr := common.GenerateRandomKey(32)
		if genErr != nil {
			return "", genErr
		}
		var existing User
		dupErr := DB.Where("access_token = ? AND id <> ?", key, userId).First(&existing).Error
		if dupErr == nil {
			continue
		}
		if !errors.Is(dupErr, gorm.ErrRecordNotFound) {
			return "", dupErr
		}
		if err := UpdateUserAccessToken(userId, key); err != nil {
			return "", err
		}
		return key, nil
	}
	return "", fmt.Errorf("failed to generate access token")
}

// InvalidateUserAccessToken 登出时清除 access_token。
func InvalidateUserAccessToken(userId int) error {
	return DB.Model(&User{}).Where("id = ?", userId).Update("access_token", nil).Error
}

// ===== 查询 =====

// AllEnabledUserIds 全部启用用户的 ID（用于站内通知全员推送）。
func AllEnabledUserIds() ([]int, error) {
	var ids []int
	if err := DB.Model(&User{}).Where("status = ?", constant.UserStatusEnabled).Order("id asc").Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// GetUserById 按 ID 加载用户。
func GetUserById(id int) (*User, error) {
	user := &User{}
	err := DB.Where("id = ?", id).First(user).Error
	if err != nil {
		return nil, err
	}
	return user, nil
}

// GetUserByEmail 按邮箱加载用户。
func GetUserByEmail(email string) (*User, error) {
	user := &User{}
	err := DB.Where("username = ?", NormalizeEmail(email)).First(user).Error
	if err != nil {
		return nil, err
	}
	return user, nil
}

// GetUserByWeChatId 按微信 unionid 加载用户。
func GetUserByWeChatId(wechatId string) (*User, error) {
	user := &User{}
	err := DB.Where("wechat_id = ?", wechatId).First(user).Error
	if err != nil {
		return nil, err
	}
	return user, nil
}

// IsEmailAlreadyTaken 判断邮箱是否已被占用（含软删账号）。
func IsEmailAlreadyTaken(email string) bool {
	return DB.Unscoped().Where("username = ?", NormalizeEmail(email)).Find(&User{}).RowsAffected > 0
}

// IsMobileAlreadyTaken 判断手机号是否已被占用（含软删账号，手机号存于 username 列）。
func IsMobileAlreadyTaken(mobile string) bool {
	return DB.Unscoped().Where("username = ? OR mobile = ?", mobile, mobile).Find(&User{}).RowsAffected > 0
}

// IsWeChatIdAlreadyTaken 判断微信 unionid 是否已被占用（含软删账号）。
func IsWeChatIdAlreadyTaken(wechatId string) bool {
	return DB.Unscoped().Where("wechat_id = ?", wechatId).Find(&User{}).RowsAffected > 0
}

// IsGitHubIdAlreadyTaken 判断 GitHub ID 是否已被占用（含软删账号）。
func IsGitHubIdAlreadyTaken(githubId string) bool {
	return DB.Unscoped().Where("github_id = ?", githubId).Find(&User{}).RowsAffected == 1
}

// IsDiscordIdAlreadyTaken 判断 Discord ID 是否已被占用（含软删账号）。
func IsDiscordIdAlreadyTaken(discordId string) bool {
	return DB.Unscoped().Where("discord_id = ?", discordId).Find(&User{}).RowsAffected == 1
}

// IsOidcIdAlreadyTaken 判断 OIDC ID 是否已被占用。
func IsOidcIdAlreadyTaken(oidcId string) bool {
	return DB.Where("oidc_id = ?", oidcId).Find(&User{}).RowsAffected == 1
}

// IsTelegramIdAlreadyTaken 判断 Telegram ID 是否已被占用（含软删账号）。
func IsTelegramIdAlreadyTaken(telegramId string) bool {
	return DB.Unscoped().Where("telegram_id = ?", telegramId).Find(&User{}).RowsAffected == 1
}

// ResetUserPasswordByEmail 按邮箱设置新密码（bcrypt）。
func ResetUserPasswordByEmail(email string, password string) error {
	if email == "" || password == "" {
		return errors.New("邮箱地址或密码为空！")
	}
	user, err := GetUserByEmail(email)
	if err != nil {
		return err
	}
	hashedPassword, err := common.Password2Hash(password)
	if err != nil {
		return err
	}
	return DB.Model(&User{}).Where("id = ?", user.Id).Update("password", hashedPassword).Error
}

// IsAdmin 判断用户是否为管理员。
func IsAdmin(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	err := DB.Where("id = ?", userId).Select("role").Find(&user).Error
	if err != nil {
		return false
	}
	return user.Role >= constant.RoleAdminUser
}

// IsRootUser 判断用户是否为超级管理员。
func IsRootUser(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	err := DB.Where("id = ?", userId).Select("role").Find(&user).Error
	if err != nil {
		return false
	}
	return user.Role >= constant.RoleRootUser
}

// ===== quota =====

// IncreaseUserQuota 增加用户额度。
func IncreaseUserQuota(id int, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	return DB.Model(&User{}).Where("id = ?", id).
		Update("quota", gorm.Expr("quota + ?", quota)).Error
}

// DecreaseUserQuota 减少用户额度。
func DecreaseUserQuota(id int, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	return DB.Model(&User{}).Where("id = ?", id).
		Update("quota", gorm.Expr("quota - ?", quota)).Error
}

// GetUserQuota 读取用户额度。
func GetUserQuota(id int) (int, error) {
	var quota int
	err := DB.Model(&User{}).Where("id = ?", id).Select("quota").Scan(&quota).Error
	return quota, err
}

// UpdateUserLastLoginAt 记录最近登录时间。
func UpdateUserLastLoginAt(id int) {
	if err := DB.Model(&User{}).Where("id = ?", id).Update("last_login_at", common.GetTimestamp()).Error; err != nil {
		common.SysLog("failed to update user last_login_at: " + err.Error())
	}
}

// GetAccessToken 返回用户 access_token。
func (user *User) GetAccessToken() string {
	if user.AccessToken == nil {
		return ""
	}
	return *user.AccessToken
}

// SetAccessToken 设置用户 access_token。
func (user *User) SetAccessToken(token string) {
	user.AccessToken = &token
}

// ===== 邮箱工具 =====

// IsEmailFormat 粗略判断是否为邮箱格式。
func IsEmailFormat(s string) bool {
	return strings.Contains(s, "@") && strings.Contains(s, ".")
}

// NormalizeEmail 邮箱小写归一化。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// IsMobileFormat 判断是否为大陆 11 位手机号（1[3-9] 开头）。
func IsMobileFormat(s string) bool {
	if len(s) != 11 || s[0] != '1' {
		return false
	}
	if s[1] < '3' || s[1] > '9' {
		return false
	}
	for i := 2; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
