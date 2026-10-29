package model

import (
	"fmt"
	"os"
	"strings"
	"time"

	"easytv-api/common"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// DB 全局主库句柄。
var DB *gorm.DB

// UsingPostgreSQL 是否使用 PostgreSQL（当前仅支持 MySQL，字段保留以兼容参考项目扩展）。
var UsingPostgreSQL = false

// InitDB 初始化数据库连接并执行迁移。
func InitDB() (err error) {
	dsn := os.Getenv("SQL_DSN")
	if dsn == "" {
		return fmt.Errorf("SQL_DSN 未设置：请在 .env 或系统环境变量中配置 MySQL DSN（参考 .env.example）")
	}

	var dialector gorm.Dialector
	if strings.HasPrefix(dsn, "postgres://") {
		UsingPostgreSQL = true
		return fmt.Errorf("postgres not supported yet, use mysql")
	}
	dialector = mysql.Open(dsn)

	logLevel := gormlogger.Warn
	if common.DebugEnabled {
		logLevel = gormlogger.Info
	}
	DB, err = gorm.Open(dialector, &gorm.Config{
		Logger: gormlogger.Default.LogMode(logLevel),
	})
	if err != nil {
		common.SysError("failed to connect database: " + err.Error())
		return err
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxIdleConns(common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))
	sqlDB.SetMaxOpenConns(common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000))
	sqlDB.SetConnMaxLifetime(time.Hour)

	// 主节点执行迁移
	if err := migrateDB(); err != nil {
		common.SysError("failed to migrate database: " + err.Error())
		return err
	}
	common.SysLog("database connected and migrated")
	return nil
}

// renamedTables 历史表名 → 现表名（表名统一单数，2026-09-12 去掉 points_ 的 s）。
// 老库存在旧表且新表尚未创建时先 RENAME，再交给 AutoMigrate 补列，避免数据留在旧表。
var renamedTables = map[string]string{
	"points_lot":  "point_lot",
	"points_log":  "point_log",
	"points_pack": "point_pack",
}

// renameLegacyTables 按 renamedTables 迁移旧表名，幂等。
func renameLegacyTables() error {
	m := DB.Migrator()
	for oldName, newName := range renamedTables {
		if m.HasTable(oldName) && !m.HasTable(newName) {
			if err := m.RenameTable(oldName, newName); err != nil {
				return fmt.Errorf("rename table %s -> %s: %w", oldName, newName, err)
			}
			common.SysLog("renamed table " + oldName + " -> " + newName)
		}
	}
	return nil
}

// migrateDB 注册全部表模型的 AutoMigrate。
func migrateDB() error {
	if err := renameLegacyTables(); err != nil {
		return err
	}
	if err := DB.AutoMigrate(
		&User{},
		&AuditLog{},
		&UserOAuth{},
		&Recharge{},
		&Withdraw{},
		&PromoterBill{},
		&Setting{},
		// 创作域（API.md）
		&Project{},
		&Canvas{},
		&CanvasVersion{},
		&Asset{},
		&GenerationTask{},
		&Work{},
		&WorkLike{},
		&WorkReport{},
		&WorkComment{},
		&Skill{},
		&SkillFavorite{},
		&Notification{},
		&UserNotificationPref{},
		&Category{},
		&Prompt{},
		&PromptSource{},
		&Style{},
		// 增长域（API.md 7）
		&TrackEvent{},
		&UserTask{},
		&Checkin{},
		&BonusCode{},
		&BonusCodeClaim{},
		// 成片与社区（API.md 8）
		&Screenplay{},
		&ComposeJob{},
		&MediaJob{},
		&CanvasTimeline{},
		&ShotReview{},
		&SeriesBible{},
		&GenerationBatch{},
		&GenerationBatchItem{},
		&SubjectVersion{},
		&UgcSubmission{},
		&Campaign{},
		&CampaignEntry{},
		&UserFollow{},
		&Partner{},
		&PartnerCustomer{},
		&Subject{},
		&CanvasWorkflow{},
		&CanvasAgentStep{},
		&CanvasChat{},
		&ProjectComment{},
		// 付费域（API.md 6）
		&MembershipPlan{},
		&PointPack{},
		&ModelPrice{},
		&Order{},
		&Membership{},
		&PointLot{},
		&PointLog{},
		// 内容运营
		&Banner{},
		&Notice{},
		// 团队版
		&Team{},
		&TeamMember{},
		&TeamInvite{},
		&AIChatUsage{},
		&UserPreference{},
	); err != nil {
		return err
	}
	return backfillRechargeInvoiceable()
}

// backfillRechargeInvoiceable 历史充值单从未写过可开票金额：未开票且待支付/成功的按实付补上。
// 已开完票（invoiced=1 且余额 0）和已退款的不动，避免把扣完的余额又写回 money。
func backfillRechargeInvoiceable() error {
	return DB.Exec(`UPDATE recharge SET invoiceable_amount = money
		WHERE (invoiceable_amount IS NULL OR invoiceable_amount = 0)
		  AND invoiced = 0
		  AND money > 0
		  AND status IN (?, ?)`, common.RechargeStatusPending, common.RechargeStatusSuccess).Error
}

// GetDBTimestamp 返回当前 Unix 秒（兼容参考项目调用）。
func GetDBTimestamp() int64 {
	return time.Now().Unix()
}
