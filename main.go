package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/logger"
	"easytv-api/middleware"
	"easytv-api/model"
	"easytv-api/router"
	"easytv-api/service"
	"easytv-api/service/providers"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

func main() {
	startTime := time.Now()
	port := flag.Int("port", 0, "服务端口（覆盖 PORT 环境变量）")
	envFile := flag.String("env-file", "", "环境变量文件路径（默认 .env）")
	logDir := flag.String("log-dir", "./logs", "日志目录（默认 ./logs）")
	flag.Parse()

	if *envFile != "" {
		common.EnvFile = *envFile
	}
	common.LogDir = *logDir
	if err := common.LoadEnvFile(); err != nil {
		common.SysError("load env file failed: " + err.Error())
	}
	common.InitEnv()
	logger.SetupLogger()
	if *port != 0 {
		common.Port = *port
	}

	// ===== 基础设施 =====
	if err := model.InitDB(); err != nil {
		common.FatalLog("init db failed: " + err.Error())
	}
	defer closeDB()

	if err := common.InitRedisClient(); err != nil {
		// Redis 不可用时降级运行（验证码走内存兜底，限流放行）
		logger.LogWarn(nil, "init redis failed, fallback to in-memory mode: "+err.Error())
	}

	if err := model.InitSettingMap(); err != nil {
		common.FatalLog("init setting map failed: " + err.Error())
	}
	setting.LoadSmsConfigFromDB()

	// ===== 初始化 root 账号（首个管理员，供管理端登录）=====
	if err := ensureRootUser(); err != nil {
		common.FatalLog(err.Error())
	}

	// ===== 文件存储（本地磁盘；UPLOAD_DIR / UPLOAD_MAX_MB，对外前缀 UPLOAD_BASE_URL 见 setting）=====
	if err := service.InitStorage(); err != nil {
		common.FatalLog("init storage failed: " + err.Error())
	}

	// ===== 媒体处理（ffmpeg / ffprobe 路径，API.md 23）=====
	service.ApplyMediaConfig()

	// ===== 生成网关（配置经 DB setting，管理端 /admin/gateway/config 热更新）=====
	// 存量迁移：model_price 为空时用旧 env EASYAPI_*_MODELS 补种；随后按 setting 注册 provider。
	model.SeedModelsFromEnvIfEmpty()
	providers.RegisterProviderFromSettings()

	// ===== 风格库（API.md 34）：表为空时写入官方内置风格，已有数据不动 =====
	if n, err := model.SeedStyles(); err != nil {
		common.SysError("seed styles failed: " + err.Error())
	} else if n > 0 {
		common.SysLog(fmt.Sprintf("seeded official styles: %d", n))
	}

	// ===== 后台任务 =====
	go startBackgroundTasks()

	// ===== HTTP 服务 =====
	gin.SetMode(gin.ReleaseMode)
	if common.DebugEnabled {
		gin.SetMode(gin.DebugMode)
	}
	server := gin.New()
	server.Use(middleware.CustomRecovery())
	server.Use(middleware.RequestId())
	server.Use(middleware.CORS())
	middleware.SetUpLogger(server)
	// 本地上传文件静态服务（换 OSS 后可去掉）
	server.Static("/upload", service.UploadDir)
	router.SetRouter(server)

	addr := fmt.Sprintf(":%d", common.Port)
	common.SysLog(common.SystemName + " " + common.Version + " started")
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}
	common.LogStartupSuccess(startTime, fmt.Sprintf("%d", common.Port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		common.SysError("server stopped: " + err.Error())
	}
}

// ensureRootUser 若不存在管理员则创建 root 账号（环境变量 ROOT_USERNAME/ROOT_PASSWORD）。
// 系统无管理员且未设置 ROOT_PASSWORD 时拒绝启动，避免默认弱口令后门。
func ensureRootUser() error {
	var count int64
	model.DB.Model(&model.User{}).Where("role >= ?", constant.RoleAdminUser).Count(&count)
	if count > 0 {
		return nil
	}
	username := common.GetEnvOrDefaultString("ROOT_USERNAME", "admin")
	password := os.Getenv("ROOT_PASSWORD")
	if password == "" {
		return fmt.Errorf("系统中无管理员且未设置 ROOT_PASSWORD：请在 .env 配置初始密码（参考 .env.example）")
	}
	if len(password) < 8 {
		return fmt.Errorf("ROOT_PASSWORD 长度不足 8 位，请设置更强的初始密码")
	}
	hashed, err := common.Password2Hash(password)
	if err != nil {
		return fmt.Errorf("hash root password failed: %w", err)
	}
	user := &model.User{
		Username: username,
		Password: hashed,
		Nickname: "Root",
		Role:     constant.RoleRootUser,
		Status:   constant.UserStatusEnabled,
		Group:    "default",
	}
	if err := user.Insert(); err != nil {
		return fmt.Errorf("create root user failed: %w", err)
	}
	common.SysLog(fmt.Sprintf("root user created: %s (please change password after first login)", username))
	return nil
}

// startBackgroundTasks 后台任务：setting 热同步、充值订单过期。
// 联调实例（WORKER_TAG 非空）只跑配置同步与三个 worker（各自只领同标签作业），全局维护任务交给生产进程。
func startBackgroundTasks() {
	dev := common.IsDevInstance()
	if dev {
		common.SysLog("WORKER_TAG=" + common.WorkerTag() + "：联调实例，只处理同标签的媒体 / 合成 / 批量作业，不跑账单 / 超时退分 / 触达 / 清理等全局任务")
	}
	// setting 热同步（每 60 秒）；同步后重建网关 provider 与存储（配置热更新生效）
	go func() {
		for {
			time.Sleep(60 * time.Second)
			if err := model.SyncSettings(); err != nil {
				common.SysError("sync settings failed: " + err.Error())
			}
			setting.LoadSmsConfigFromDB()
			providers.RefreshProviderFromSettings()
			service.ApplyStorageConfig()
			service.ApplyMediaConfig()
		}
	}()
	if !dev {
		// 充值订单过期（每 5 分钟，pending 超过 30 分钟置 expired）
		go func() {
			for {
				time.Sleep(5 * time.Minute)
				expirePendingRecharges()
			}
		}()
		// 付费域：关单 / 会员月发积分 / 会员过期 / 积分批次过期（每 10 分钟；启动 30 秒后先跑一次）
		go func() {
			time.Sleep(30 * time.Second)
			for {
				service.RunBillingJobs()
				time.Sleep(10 * time.Minute)
			}
		}()
		// 生成任务超时兜底（每 2 分钟）：pending / running 超过阈值的任务标失败并退积分
		go func() {
			time.Sleep(45 * time.Second)
			for {
				service.ExpireStaleTasks()
				time.Sleep(2 * time.Minute)
			}
		}()
		// 模型健康 + 网关余额提醒（每 6 小时；启动 5 分钟后先跑一次）：建议下架的模型、余额低于阈值都通知管理员（API.md 37.3 / 38）
		go func() {
			time.Sleep(5 * time.Minute)
			for {
				service.RunModelHealthAlerts()
				service.RunGatewayBalanceAlert()
				time.Sleep(6 * time.Hour)
			}
		}()
		// 生命周期触达（每 6 小时；启动 3 分钟后先跑一次）：回归礼、会员续费提醒、积分过期提醒
		go func() {
			time.Sleep(3 * time.Minute)
			for {
				service.RunLifecycleJobs()
				time.Sleep(6 * time.Hour)
			}
		}()
	}
	// 成片合成 worker（每 5 秒领取排队任务，并发 2）；每分钟回收超时仍停在合成中的任务
	// （进程重启 / 协程崩溃留下的，只在启动时清一次会漏掉重启前 10 分钟内开始的任务）
	go func() {
		for i := 0; ; i++ {
			if i%12 == 0 {
				service.ResetStaleComposeJobs()
			}
			service.RunComposeWorker()
			time.Sleep(5 * time.Second)
		}
	}()
	// 服务端批量生成编排（API.md 13）：每 5 秒推进一轮
	go func() {
		time.Sleep(20 * time.Second)
		for {
			service.RunBatchWorker()
			time.Sleep(5 * time.Second)
		}
	}()
	// 媒体工具 worker（API.md 23）：每 5 秒领取排队作业，并发 4；每分钟回收超时未完成的
	// （本地 ffmpeg 处理不走生成任务，进程重启后靠这里判失败）
	go func() {
		for i := 0; ; i++ {
			if i%12 == 0 {
				service.ResetStaleMediaJobs()
			}
			service.RunMediaWorker()
			time.Sleep(5 * time.Second)
		}
	}()
	if dev {
		return
	}
	// 提示词远程源同步（每 12 小时；启动 5 分钟后先跑一次，避开启动高峰）
	go func() {
		time.Sleep(5 * time.Minute)
		for {
			service.RunPromptSyncJob()
			time.Sleep(12 * time.Hour)
		}
	}()
	// 审计日志保留期清理（每 24 小时；启动 10 分钟后先跑一次）：AuditLogRetentionDays 天前的记录分批删除，0 = 永久保留
	go func() {
		time.Sleep(10 * time.Minute)
		for {
			purgeAuditLogs()
			time.Sleep(24 * time.Hour)
		}
	}()
	// 回收站清理（每 6 小时，删除超过 30 天的项目/画布）
	go func() {
		for {
			time.Sleep(6 * time.Hour)
			purgeTrash()
		}
	}()
}

// purgeTrash 物理删除回收站中超过 30 天的项目与画布。
func purgeTrash() {
	cutoff := time.Now().Add(-30 * 24 * time.Hour).Unix()
	count, err := model.PurgeTrashedBefore(cutoff)
	if err != nil {
		common.SysError("purge trash failed: " + err.Error())
		return
	}
	if count > 0 {
		common.SysLog(fmt.Sprintf("purged %d trashed projects", count))
	}
	keys, err := model.PurgeTrashedAssetsBefore(cutoff)
	if err != nil {
		common.SysError("purge trashed assets failed: " + err.Error())
	}
	for _, key := range keys {
		if service.DefaultStorage != nil {
			_ = service.DefaultStorage.Remove(key)
		}
	}
	if len(keys) > 0 {
		common.SysLog(fmt.Sprintf("purged %d trashed assets", len(keys)))
	}
}

// purgeAuditLogs 按 AuditLogRetentionDays 删除过期审计日志（每批 5000 条）。
func purgeAuditLogs() {
	days := setting.GetAuditLogRetentionDays()
	if days <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	count, err := model.PurgeAuditLogsBefore(cutoff, model.AuditLogPurgeBatchSize)
	if err != nil {
		common.SysError(fmt.Sprintf("purge audit logs failed after %d rows: %v", count, err))
		return
	}
	if count > 0 {
		common.SysLog(fmt.Sprintf("purged %d audit logs older than %d days", count, days))
	}
}

// expirePendingRecharges 将超过 30 分钟的 pending 充值订单置为 expired。
func expirePendingRecharges() {
	cutoff := time.Now().Add(-30 * time.Minute).Unix()
	err := model.DB.Model(&model.Recharge{}).
		Where("status = ? AND create_time < ?", common.RechargeStatusPending, cutoff).
		Update("status", common.RechargeStatusExpired).Error
	if err != nil {
		common.SysError("expire pending recharges failed: " + err.Error())
	}
}

func closeDB() {
	// 先把队列里还没落库的审计日志写完（最多等 5 秒）
	model.FlushAuditLogs(5 * time.Second)
	if sqlDB, err := model.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
