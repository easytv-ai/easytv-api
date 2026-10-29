package router

import (
	"easytv-api/controller"
	"easytv-api/middleware"

	"github.com/gin-gonic/gin"
)

// SetAdminRouter 管理侧路由。创作域契约见 API.md 2.3，付费域见 6.2。
//
// 权限分两档：
//   - AdminAuth（role ≥ 10）：查看列表、内容运营（作品/技能/Banner/公告/项目/画布/资产）、用户启停/身份、关单
//   - RootAuth（role ≥ 100）：系统设置、提现打款、退款/手动收款、积分调整、用户注销/换绑/额度、会员计划/积分包/促销/模型定价的增删改
func SetAdminRouter(router *gin.Engine) {
	adminRouter := router.Group("/admin")
	adminRouter.Use(middleware.AdminAuth())
	adminRouter.Use(middleware.AdminAudit()) // 管理员操作自动审计
	{
		// 审计日志
		adminRouter.GET("/audit", controller.AdminGetAuditLogs)

		// 系统概况
		adminRouter.GET("/stats", controller.AdminGetSystemStats)
		adminRouter.GET("/stats/funnel", controller.AdminFunnel)
		adminRouter.GET("/stats/creation", controller.AdminCreationMetrics)

		// 用户管理（查看 / 启停）
		adminRouter.GET("/user", controller.AdminGetUsers)
		adminRouter.GET("/user/:id", controller.AdminGetUser)
		adminRouter.PUT("/user/:id/status", controller.AdminUpdateUserStatus)
		adminRouter.PUT("/user/:id/identity", controller.AdminUpdateUserIdentity)
		adminRouter.POST("/user/:id/console_access", controller.AdminGenerateConsoleAccess)
		adminRouter.GET("/user/:id/invites", controller.AdminGetUserInvites)
		adminRouter.GET("/bonus-code/:id/stats", controller.AdminBonusCodeStats)
		adminRouter.GET("/bonus-code/export", controller.AdminExportBonusCodes)

		// 充值 / 提现 / 流水（只读）
		adminRouter.GET("/recharge", controller.AdminGetRecharges)
		adminRouter.GET("/withdraw", controller.AdminGetWithdraws)
		adminRouter.GET("/bills", controller.AdminGetBills)

		// 创作域：项目
		adminRouter.GET("/project", controller.AdminGetProjects)
		adminRouter.PUT("/project/:id/status", controller.AdminSetProjectStatus)
		adminRouter.DELETE("/project/:id", controller.AdminDeleteProject)

		// 创作域：画布
		adminRouter.GET("/canvas", controller.AdminGetCanvases)
		adminRouter.PUT("/canvas/:id/status", controller.AdminSetCanvasStatus)
		adminRouter.DELETE("/canvas/:id", controller.AdminDeleteCanvas)

		// 创作域：资产
		adminRouter.GET("/asset", controller.AdminGetAssets)
		adminRouter.PUT("/asset/:id/status", controller.AdminSetAssetStatus)
		adminRouter.DELETE("/asset/:id", controller.AdminDeleteAsset)

		// 创作域：任务
		adminRouter.GET("/task", controller.AdminGetTasks)

		// 创作域：作品
		adminRouter.GET("/work", controller.AdminGetWorks)
		adminRouter.PUT("/work/:id", controller.AdminUpdateWork)
		adminRouter.PUT("/work/:id/status", controller.AdminSetWorkStatus)
		adminRouter.PUT("/work/:id/review", controller.AdminReviewWork)
		adminRouter.DELETE("/work/:id", controller.AdminDeleteWork)

		// 社区审核：举报
		adminRouter.GET("/work-report", controller.AdminGetWorkReports)
		adminRouter.PUT("/work-report/:id/status", controller.AdminSetWorkReportStatus)

		// 社区审核：评论
		adminRouter.GET("/comment", controller.AdminGetComments)
		adminRouter.PUT("/comment/:id/status", controller.AdminSetCommentStatus)
		adminRouter.DELETE("/comment/:id", controller.AdminDeleteComment)

		// 内容运营：分类
		adminRouter.GET("/category", controller.AdminGetCategories)
		adminRouter.POST("/category", controller.AdminCreateCategory)
		adminRouter.PUT("/category/:id", controller.AdminUpdateCategory)
		adminRouter.DELETE("/category/:id", controller.AdminDeleteCategory)

		// 内容运营：技能模板
		adminRouter.GET("/skill", controller.AdminGetSkills)
		adminRouter.PUT("/skill/:id/review", controller.AdminReviewSkill)
		// 站外投稿与活动（API.md 8.5 / 8.6）
		// 迭代四：主体库 / 团队额度（API.md 10.9）
		adminRouter.GET("/subject", controller.AdminGetSubjects)
		adminRouter.GET("/team", controller.AdminGetTeams)
		adminRouter.GET("/team/:id/members", controller.AdminGetTeamMembers)
		adminRouter.GET("/partner", controller.AdminGetPartners)
		adminRouter.GET("/partner/:id/customers", controller.AdminGetPartnerCustomers)
		adminRouter.GET("/ugc", controller.AdminGetUgc)
		adminRouter.PUT("/ugc/:id/review", controller.AdminReviewUgc)
		adminRouter.GET("/campaign/:id/entries", controller.AdminGetCampaignEntries)
		adminRouter.PUT("/campaign/entry/:id/review", controller.AdminReviewCampaignEntry)
		adminRouter.POST("/skill", controller.AdminCreateSkill)
		adminRouter.PUT("/skill/:id", controller.AdminUpdateSkill)
		adminRouter.DELETE("/skill/:id", controller.AdminDeleteSkill)

		// 内容运营：提示词库与远程源（API.md 1.11）
		adminRouter.GET("/prompt", controller.AdminGetPrompts)
		adminRouter.POST("/prompt", controller.AdminCreatePrompt)
		adminRouter.PUT("/prompt/:id", controller.AdminUpdatePrompt)
		adminRouter.DELETE("/prompt/:id", controller.AdminDeletePrompt)
		adminRouter.GET("/prompt-source", controller.AdminGetPromptSources)
		adminRouter.POST("/prompt-source", controller.AdminCreatePromptSource)
		adminRouter.PUT("/prompt-source/:id", controller.AdminUpdatePromptSource)
		adminRouter.DELETE("/prompt-source/:id", controller.AdminDeletePromptSource)
		adminRouter.POST("/prompt-source/:id/sync", controller.AdminSyncPromptSource)
		// 内容运营：风格库（API.md 34）
		adminRouter.GET("/style", controller.AdminGetStyles)
		adminRouter.POST("/style", controller.AdminCreateStyle)
		adminRouter.PUT("/style/:id", controller.AdminUpdateStyle)
		adminRouter.DELETE("/style/:id", controller.AdminDeleteStyle)
		adminRouter.POST("/style/:id/review", controller.AdminReviewStyle)

		// 内容运营：Banner / 公告
		adminRouter.GET("/banner", controller.AdminGetBanners)
		adminRouter.POST("/banner", controller.AdminCreateBanner)
		adminRouter.PUT("/banner/:id", controller.AdminUpdateBanner)
		adminRouter.DELETE("/banner/:id", controller.AdminDeleteBanner)
		adminRouter.GET("/notice", controller.AdminGetNotices)
		adminRouter.POST("/notice", controller.AdminCreateNotice)
		adminRouter.PUT("/notice/:id", controller.AdminUpdateNotice)
		adminRouter.DELETE("/notice/:id", controller.AdminDeleteNotice)

		// 内容运营：站内通知（管理 / 推送）
		adminRouter.GET("/notification", controller.AdminGetNotifications)
		adminRouter.GET("/notification/types", controller.AdminGetNotificationTypes)
		adminRouter.DELETE("/notification/:id", controller.AdminDeleteNotification)
		adminRouter.POST("/notification/send", controller.AdminSendNotification)

		// 付费域（只读 + 关单）
		adminRouter.GET("/plan", controller.AdminGetPlans)
		adminRouter.GET("/points-pack", controller.AdminGetPacks)
		adminRouter.GET("/billing/promo", controller.AdminGetBillingPromo)
		adminRouter.GET("/media/actions", controller.AdminGetMediaActions) // 画布媒体工具盘点（API.md 23.2）
		adminRouter.GET("/model-price", controller.AdminGetModelPrices)
		adminRouter.GET("/model-price/health", controller.AdminModelPriceHealth) // 模型健康（API.md 37），须在 /:id 之前
		adminRouter.GET("/gateway/balance", controller.AdminGatewayBalance)      // 网关余额（API.md 38）
		adminRouter.GET("/model-price/:id", controller.AdminGetModelPrice)
		adminRouter.GET("/order", controller.AdminGetOrders)
		adminRouter.POST("/order/:id/close", controller.AdminCloseOrder)
		adminRouter.GET("/wallet", controller.AdminGetWallets)
		adminRouter.GET("/points/log", controller.AdminGetPointLogs)
	}

	rootRouter := router.Group("/admin")
	rootRouter.Use(middleware.RootAuth())
	rootRouter.Use(middleware.AdminAudit()) // 管理员操作自动审计
	{
		// 系统设置
		rootRouter.GET("/setting", controller.AdminGetSettings)
		rootRouter.PUT("/setting/:key", controller.AdminUpdateSetting)

		// 用户：注销 / 额度
		rootRouter.DELETE("/user/:id", controller.AdminDeleteUser)
		rootRouter.PUT("/user/:id/bind", controller.AdminRebindUser)
		rootRouter.PUT("/user/:id/invite-block", controller.AdminSetInviteBlock)
		// 口令红包（T15）
		rootRouter.GET("/bonus-code", controller.AdminGetBonusCodes)
		rootRouter.POST("/bonus-code", controller.AdminCreateBonusCode)
		rootRouter.POST("/bonus-code/batch", controller.AdminBatchBonusCodes)
		rootRouter.GET("/campaign", controller.AdminGetCampaigns)
		rootRouter.POST("/campaign", controller.AdminCreateCampaign)
		rootRouter.PUT("/campaign/:id", controller.AdminUpdateCampaign)
		rootRouter.DELETE("/campaign/:id", controller.AdminDeleteCampaign)
		rootRouter.POST("/campaign/:id/settle", controller.AdminSettleCampaign)
		rootRouter.PUT("/partner/:id/review", controller.AdminReviewPartner)
		rootRouter.DELETE("/subject/:id", controller.AdminDeleteSubject)
		rootRouter.PUT("/team/member/:id/limit", controller.AdminSetTeamMemberLimit)
		rootRouter.PUT("/bonus-code/:id", controller.AdminUpdateBonusCode)
		rootRouter.DELETE("/bonus-code/:id", controller.AdminDeleteBonusCode)
		rootRouter.POST("/user/:id/quota", controller.AdminAdjustUserQuota)

		// 提现打款
		rootRouter.POST("/withdraw/:id/audit", controller.AdminAuditWithdraw)
		rootRouter.POST("/withdraw/:id/paid", controller.AdminMarkWithdrawPaid)
		rootRouter.POST("/withdraw/:id/failed", controller.AdminMarkWithdrawFailed)

		// 付费域：资金相关操作与定价配置
		rootRouter.POST("/order/:id/mark-paid", controller.AdminMarkOrderPaid)
		rootRouter.POST("/order/:id/refund", controller.AdminRefundOrder)
		rootRouter.POST("/wallet/:user_id/adjust", controller.AdminAdjustWallet)
		rootRouter.POST("/plan", controller.AdminCreatePlan)
		rootRouter.PUT("/plan/:id", controller.AdminUpdatePlan)
		rootRouter.DELETE("/plan/:id", controller.AdminDeletePlan)
		rootRouter.POST("/points-pack", controller.AdminCreatePack)
		rootRouter.PUT("/points-pack/:id", controller.AdminUpdatePack)
		rootRouter.DELETE("/points-pack/:id", controller.AdminDeletePack)
		rootRouter.PUT("/billing/promo", controller.AdminUpdateBillingPromo)
		rootRouter.POST("/model-price", controller.AdminCreateModelPrice)
		rootRouter.PUT("/model-price/:id", controller.AdminUpdateModelPrice)
		rootRouter.POST("/model-price/sync-gateway", controller.AdminSyncAllModelPricesGateway)
		rootRouter.POST("/model-price/:id/sync-gateway", controller.AdminSyncModelPriceGateway)
		rootRouter.DELETE("/model-price/:id", controller.AdminDeleteModelPrice)

		// 生成网关与存储（EasyAPI 网关配置 / 七牛 / 上传前缀，热更新）
		rootRouter.GET("/gateway/config", controller.AdminGetGatewayConfig)
		rootRouter.PUT("/gateway/config", controller.AdminUpdateGatewayConfig)
		rootRouter.GET("/gateway/fetch-models", controller.AdminFetchGatewayModels)
		rootRouter.POST("/gateway/models/import", controller.AdminImportGatewayModels)

		// AI 对话（免费开关 / 模型 / 思考参数 / 每日免费次数 / 平台知识，热更新）
		rootRouter.GET("/ai-chat/config", controller.AdminGetAIChatConfig)
		rootRouter.PUT("/ai-chat/config", controller.AdminUpdateAIChatConfig)
	}
}
