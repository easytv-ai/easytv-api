package model

import (
	"time"

	"easytv-api/common"

	"gorm.io/gorm"
)

// UserOAuth stores the relationship between users and third-party OAuth providers (table user_oauth).
// 简化版：以 provider 字符串标识登录方式（easyapi 用 ProviderId 指向 custom_oauth_provider 配置表，
// 本表不引入配置表，新增登录方式直接写死注册到 oauth 包即可）；索引语义与 easyapi 对齐。
type UserOAuth struct {
	Id int `json:"id" gorm:"primaryKey;comment:用户与OAuth关系ID"`
	// UserId 用户 ID - one row per user per provider
	UserId int `json:"user_id" gorm:"not null;uniqueIndex:ux_user_provider;comment:用户 ID"`
	// Provider 登录方式标识（github/oidc/自定义 slug 等）
	Provider string `json:"provider" gorm:"type:varchar(64);not null;uniqueIndex:ux_user_provider;uniqueIndex:ux_provider_userid;comment:登录方式标识"`
	// ProviderUserId Provider 侧唯一用户 id
	ProviderUserId string    `json:"provider_user_id" gorm:"type:varchar(256);not null;uniqueIndex:ux_provider_userid;comment:第三方侧唯一用户 id"`
	CreatedAt      time.Time `json:"created_at" gorm:"comment:绑定时间"`
}

func (UserOAuth) TableName() string { return "user_oauth" }

// Insert 创建绑定关系。
func (uo *UserOAuth) Insert() error {
	return DB.Create(uo).Error
}

// GetUserOAuthByProvider 查询某 provider 下的绑定用户。
func GetUserOAuthByProvider(provider string, providerUserId string) (*UserOAuth, error) {
	uo := &UserOAuth{}
	err := DB.Where("provider = ? AND provider_user_id = ?", provider, providerUserId).First(uo).Error
	if err != nil {
		return nil, err
	}
	return uo, nil
}

// GetUserOAuthsByUserId 查询用户全部绑定。
func GetUserOAuthsByUserId(userId int) ([]*UserOAuth, error) {
	var list []*UserOAuth
	err := DB.Where("user_id = ?", userId).Find(&list).Error
	return list, err
}

// DeleteUserOAuth 解绑指定 provider。
func DeleteUserOAuth(userId int, provider string) error {
	return DB.Where("user_id = ? AND provider = ?", userId, provider).Delete(&UserOAuth{}).Error
}

// CreateUserWithOAuth 事务内创建用户并写入 OAuth 绑定关系。
func CreateUserWithOAuth(user *User, provider string, providerUserId string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// 该路径绕过 User.Insert()，需同样保证 aff_code 非空（列有唯一索引）
		if user.AffCode == "" {
			user.AffCode = common.GetRandomString(4)
		}
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		uo := &UserOAuth{
			UserId:         user.Id,
			Provider:       provider,
			ProviderUserId: providerUserId,
		}
		return tx.Create(uo).Error
	})
}

// deleteUserOAuthsByUserId 删除用户全部绑定（事务内）。
func deleteUserOAuthsByUserId(tx *gorm.DB, userId int) error {
	return tx.Where("user_id = ?", userId).Delete(&UserOAuth{}).Error
}
