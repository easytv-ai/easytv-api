package router

import (
	"easytv-api/controller"
	"easytv-api/middleware"

	// 注册 OAuth providers（init() 自注册）
	_ "easytv-api/oauth"

	"github.com/gin-gonic/gin"
)

// SetApiRouter 用户侧路由（匿名 + UserAuth），路径与 easyapi-ai-api 契约兼容，便于前端切换。
// 创作域（项目/画布/资产/任务/作品）契约见 API.md。
func SetApiRouter(router *gin.Engine) {
	apiRouter := router.Group("/")
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	{
		// ===== 匿名：验证码 / 重置密码 =====
		apiRouter.GET("/verification", middleware.EmailVerificationRateLimit(), controller.SendEmailVerification)
		apiRouter.GET("/sms/verification", middleware.SmsVerificationRateLimit(), controller.SendSmsVerification)
		apiRouter.GET("/reset_password", middleware.CriticalRateLimit(), controller.SendPasswordResetEmail)
		apiRouter.POST("/user/reset", middleware.CriticalRateLimit(), controller.ResetPassword)

		// ===== OAuth（预留其它登录方式）=====
		apiRouter.GET("/oauth/state", middleware.CriticalRateLimit(), controller.GenerateOAuthCode)
		apiRouter.GET("/oauth/providers", controller.GetOAuthProviders)
		apiRouter.POST("/oauth/email/bind", middleware.UserAuth(), middleware.CriticalRateLimit(), controller.EmailBind)
		// Telegram（预留占位）
		apiRouter.GET("/oauth/telegram/login", middleware.CriticalRateLimit(), controller.TelegramLogin)
		apiRouter.GET("/oauth/telegram/bind", middleware.CriticalRateLimit(), controller.TelegramBind)
		// 微信扫码（与前端 oauth.ts 对齐）
		apiRouter.POST("/wechat/oauth/preauth", controller.WeChatOAuthPreAuth)
		apiRouter.GET("/wechat/oauth/callback", controller.WeChatOAuthCallback)
		apiRouter.GET("/wechat/oauth/login-status", middleware.LoginStatusRateLimit(), controller.WeChatOAuthLoginStatus)
		apiRouter.POST("/wechat/oauth/register", middleware.CriticalRateLimit(), controller.WeChatOAuthRegister)
		// 微信 OAuth 通用路由（前端 oauth.ts 的 /oauth/wechat、/oauth/wechat/bind）
		apiRouter.GET("/oauth/wechat", controller.WeChatOAuthPreAuth)
		apiRouter.GET("/oauth/wechat/bind", controller.WeChatOAuthBind)
		// 统一 OAuth provider 回调（github/discord/oidc 等，注册表分发）
		apiRouter.GET("/oauth/:provider", middleware.CriticalRateLimit(), controller.HandleOAuth)

		// ===== 支付回调（第三方 webhook，匿名）=====
		apiRouter.POST("/wechat/webhook", controller.WechatNativeWebhook)
		apiRouter.POST("/alipay/webhook", controller.AlipayWebhook)

		// ===== 公开：作品信息流 / 可用模型 =====
		apiRouter.GET("/app/config", controller.GetAppConfig)
		// 增长域（API.md 7）：前端事件上报，匿名可调
		apiRouter.POST("/track", middleware.TryUserAuth(), middleware.TrackRateLimit(), controller.TrackEvents)
		apiRouter.GET("/invite/:code", controller.ResolveInviteCode)
		apiRouter.GET("/bonus-code/preview", middleware.CriticalRateLimit(), controller.PreviewBonusCode)
		apiRouter.GET("/work", middleware.TryUserAuth(), controller.GetPublicWorks)
		apiRouter.GET("/work/:id", middleware.TryUserAuth(), controller.GetPublicWork)
		apiRouter.GET("/generation/models", controller.GetGenerationModels)
		apiRouter.GET("/generation/models/detail", controller.GetGenerationModelDetail)
		apiRouter.GET("/skill", middleware.TryUserAuth(), controller.GetPublicSkills)
		apiRouter.GET("/creator/leaderboard", controller.GetCreatorLeaderboard)
		apiRouter.GET("/campaign", controller.GetCampaigns)
		apiRouter.GET("/campaign/:slug", middleware.TryUserAuth(), controller.GetCampaign)
		apiRouter.GET("/campaign/:slug/entries", controller.GetCampaignEntries)
		// 榜单（API.md 9.2）
		apiRouter.GET("/rank/works", middleware.TryUserAuth(), controller.GetWorkRank)
		apiRouter.GET("/rank/authors", middleware.TryUserAuth(), controller.GetAuthorRank)
		apiRouter.GET("/rank/skills", controller.GetSkillRank)
		apiRouter.GET("/partner/:code", controller.GetPartnerLanding)
		apiRouter.GET("/langs", controller.GetLangs)
		apiRouter.GET("/skill/:id", middleware.TryUserAuth(), controller.GetPublicSkill)
		// 提示词库（API.md 1.11）：匿名可看，详情记一次复制
		apiRouter.GET("/prompt", controller.GetPublicPrompts)
		apiRouter.GET("/prompt/tags", controller.GetPublicPromptTags)
		apiRouter.GET("/prompt/sources", controller.GetPublicPromptSources)
		apiRouter.GET("/prompt/:id", controller.GetPublicPrompt)
		// 风格库（API.md 34）：匿名可看，套用记一次
		apiRouter.GET("/style", controller.GetPublicStyles)
		apiRouter.GET("/style/categories", controller.GetPublicStyleCategories)
		apiRouter.POST("/style/:id/use", middleware.UserAuth(), controller.UseStyle)
		apiRouter.GET("/work/:id/process", controller.GetWorkProcess)
		apiRouter.GET("/work/:id/clone-preview", middleware.TryUserAuth(), controller.GetWorkClonePreview)
		apiRouter.GET("/work/:id/comments", middleware.TryUserAuth(), controller.GetWorkComments)
		apiRouter.GET("/author/:id", middleware.TryUserAuth(), controller.GetAuthorProfile)
		apiRouter.GET("/share/canvas/:token", middleware.TryUserAuth(), controller.GetSharedCanvas)
		apiRouter.GET("/comment/:id/replies", middleware.TryUserAuth(), controller.GetCommentReplies)
		apiRouter.GET("/banner", controller.GetPublicBanners)
		apiRouter.GET("/category", controller.GetPublicCategories)
		apiRouter.GET("/notice", controller.GetPublicNotices)
		apiRouter.GET("/notice/:id", controller.GetPublicNotice)

		// ===== 付费域公开（API.md 6.2）=====
		apiRouter.GET("/billing/plans", controller.GetBillingPlans)
		apiRouter.GET("/billing/model-prices", controller.GetBillingModelPrices)
		apiRouter.POST("/billing/quote", middleware.TryUserAuth(), controller.BillingQuote)

		// ===== 用户中心 =====
		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/register", middleware.CriticalRateLimit(), controller.Register)
			userRoute.POST("/login", middleware.CriticalRateLimit(), controller.Login)
			userRoute.POST("/sms_login", middleware.CriticalRateLimit(), controller.SmsLogin)
			userRoute.POST("/login/2fa", middleware.CriticalRateLimit(), controller.Verify2FALogin)
			userRoute.POST("/passkey/login/begin", middleware.CriticalRateLimit(), controller.PasskeyLoginBegin)
			userRoute.POST("/passkey/login/finish", middleware.CriticalRateLimit(), controller.PasskeyLoginFinish)
			userRoute.POST("/console_access/exchange", middleware.CriticalRateLimit(), controller.ExchangeConsoleAccess)
			userRoute.GET("/logout", controller.Logout)
			userRoute.GET("/groups", controller.GetUserGroups)

			selfRoute := userRoute.Group("/")
			// TeamSpace：请求头 EasyTV-Team 选择个人 / 团队空间（API.md 20），必须在 UserAuth 之后
			selfRoute.Use(middleware.UserAuth(), middleware.TeamSpace())
			{
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.PUT("/self", controller.UpdateSelf)
				selfRoute.GET("/self/groups", controller.GetSelfGroup)
				selfRoute.POST("/self/mobile", middleware.CriticalRateLimit(), controller.BindMobile)
				selfRoute.GET("/quota", controller.GetMyQuota)

				// 充值
				selfRoute.GET("/recharge/info", controller.GetRechargeInfo)
				selfRoute.GET("/recharge/self", controller.GetUserRecharges)
				selfRoute.POST("/pay", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("pay"), controller.RequestRecharge)
				selfRoute.POST("/recharge/status", middleware.CriticalRateLimit(), controller.RequestTopupStatus)

				// 财务
				selfRoute.POST("/withdraw", middleware.CriticalRateLimit(), controller.ApplyWithdraw)
				selfRoute.GET("/withdraw/self", controller.GetMyWithdraws)
				selfRoute.GET("/bills/self", controller.GetMyBills)

				// 发票（API.md 21）：按账户开，不看团队空间头
				selfRoute.GET("/invoice/summary", controller.GetInvoiceSummary)
				selfRoute.GET("/invoice/access-token", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("invoice"), controller.GetInvoiceAccessToken)
				selfRoute.POST("/invoice/apply", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("invoice"), controller.ApplyInvoice)

				// ===== 创作域（API.md 2.1）=====
				// 项目
				selfRoute.GET("/project", controller.GetMyProjects)
				selfRoute.GET("/project/trash", controller.GetMyTrashProjects)
				selfRoute.POST("/project", controller.CreateProject)
				selfRoute.GET("/project/:id", controller.GetMyProject)
				selfRoute.PUT("/project/:id", controller.UpdateProject)
				selfRoute.DELETE("/project/:id", controller.TrashProject)
				selfRoute.POST("/project/:id/restore", controller.RestoreProject)
				selfRoute.POST("/project/:id/move", controller.MoveProject) // 项目换空间（API.md 20）
				// 项目评论批注（F7，API.md 10.10）
				selfRoute.GET("/project/:id/comment", controller.GetProjectComments)
				selfRoute.POST("/project/:id/comment", controller.CreateProjectComment)
				selfRoute.PUT("/project/comment/:id", controller.UpdateProjectComment)
				selfRoute.DELETE("/project/comment/:id", controller.DeleteProjectComment)

				// 画布
				selfRoute.GET("/canvas", controller.GetMyCanvases)
				selfRoute.GET("/canvas/trash", controller.GetMyTrashCanvases)
				selfRoute.POST("/canvas", controller.CreateCanvas)
				selfRoute.POST("/canvas/:id/restore", controller.RestoreMyCanvas)
				selfRoute.GET("/canvas/:id", controller.GetMyCanvas)
				selfRoute.GET("/canvas/:id/share", controller.GetMyCanvasShare)
				selfRoute.PUT("/canvas/:id/share", controller.SetMyCanvasShare)
				selfRoute.POST("/share/canvas/:token/copy", controller.CopySharedCanvas)
				selfRoute.GET("/canvas/:id/versions", controller.GetMyCanvasVersions)
				selfRoute.GET("/canvas/:id/versions/:vid", controller.GetMyCanvasVersion)
				selfRoute.POST("/canvas/:id/versions/:vid/restore", controller.RestoreMyCanvasVersion)
				selfRoute.PUT("/canvas/:id", controller.SaveCanvas)
				selfRoute.DELETE("/canvas/:id", controller.TrashCanvas)
				// 画布右侧对话：历史列表与单段会话（API.md 2.1）
				selfRoute.GET("/canvas/:id/chat", controller.GetMyCanvasChats)
				selfRoute.POST("/canvas/:id/chat", controller.CreateMyCanvasChat)
				selfRoute.DELETE("/canvas/:id/chat", controller.ClearMyCanvasChats)
				selfRoute.GET("/canvas/:id/chat/:sid", controller.GetMyCanvasChat)
				selfRoute.PUT("/canvas/:id/chat/:sid", controller.SaveMyCanvasChat)
				selfRoute.DELETE("/canvas/:id/chat/:sid", controller.DeleteMyCanvasChat)

				// 资产
				selfRoute.GET("/asset", controller.GetMyAssets)
				selfRoute.GET("/asset/trash", controller.GetMyTrashAssets)
				selfRoute.POST("/asset/upload", controller.UploadAsset)
				selfRoute.POST("/asset/import", controller.ImportMyAssets) // 跨空间引入（API.md 36.1）
				selfRoute.GET("/asset/:id", controller.GetMyAsset)
				selfRoute.GET("/asset/:id/refs", controller.GetMyAssetRefs)
				selfRoute.POST("/asset/:id/extend", middleware.UserCriticalRateLimit("task"), controller.ExtendMyAsset)
				selfRoute.POST("/asset/:id/upscale", middleware.UserCriticalRateLimit("task"), controller.UpscaleMyTask)
				selfRoute.POST("/asset/:id/restore", controller.RestoreMyAsset)
				selfRoute.DELETE("/asset/:id", controller.DeleteMyAsset)

				// 生成任务
				selfRoute.POST("/task", middleware.UserCriticalRateLimit("task"), controller.GuardClientChatFree(), controller.CreateTask)
				selfRoute.GET("/task", controller.GetMyTasks)
				selfRoute.GET("/task/summary", controller.GetMyTaskSummary)
				selfRoute.GET("/task/:id", controller.GetMyTask)
				selfRoute.POST("/task/:id/cancel", controller.CancelMyTask)
				// 重试不挂 GuardRetryChatFree：免积分标记的开关回滚由 RetryMyTask 按当前开关重估 free
				// （service.RewriteChatFreeFlag，口径同 NewAgentRetryTask），重估而不是拒绝，不打断用户在任务列表里的重试。
				selfRoute.POST("/task/:id/retry", middleware.UserCriticalRateLimit("task"), controller.RetryMyTask)

				// 作品
				selfRoute.GET("/work", controller.GetMyWorks)
				selfRoute.POST("/work", controller.CreateWork)
				selfRoute.PUT("/work/:id", controller.UpdateMyWork)
				selfRoute.DELETE("/work/:id", controller.DeleteMyWork)
				selfRoute.POST("/work/:id/like", controller.ToggleWorkLike)
				selfRoute.POST("/work/:id/clone", middleware.UserCriticalRateLimit("clone"), controller.CloneWork)
				// 成片工作室（API.md 8.2）
				selfRoute.POST("/screenplay", middleware.UserCriticalRateLimit("screenplay"), controller.CreateScreenplay)
				selfRoute.GET("/screenplay/:id", controller.GetMyScreenplay)
				selfRoute.PUT("/screenplay/:id", controller.UpdateMyScreenplay)
				selfRoute.POST("/screenplay/:id/estimate", controller.EstimateMyScreenplay)
				selfRoute.POST("/screenplay/:id/build", controller.BuildMyScreenplay)
				selfRoute.POST("/screenplay/:id/localize", middleware.UserCriticalRateLimit("translate"), controller.LocalizeScreenplay)
				selfRoute.POST("/screenplay/:id/dialogue-shots", controller.DialogueShotsMyScreenplay)
				selfRoute.PUT("/screenplay/:id/episode", controller.SetMyScreenplayEpisode)
				selfRoute.POST("/screenplay/:id/record", middleware.UserCriticalRateLimit("screenplay"), controller.RecordMyScreenplayEpisode)
				selfRoute.GET("/project/:id/bible", controller.GetMyProjectBible)
				selfRoute.PUT("/project/:id/bible", controller.SaveMyProjectBible)
				selfRoute.POST("/project/:id/bible/impact", controller.ImpactMyProjectBible)
				selfRoute.GET("/project/:id/episodes", controller.GetMyProjectEpisodes)
				selfRoute.POST("/subtitle/translate", middleware.UserCriticalRateLimit("translate"), controller.TranslateSubtitle)
				selfRoute.POST("/canvas/:id/compose", middleware.UserCriticalRateLimit("compose"), controller.CreateCompose)
				selfRoute.GET("/canvas/:id/shots", controller.GetMyCanvasShots)
				selfRoute.GET("/canvas/:id/timeline", controller.GetMyCanvasTimeline)
				selfRoute.PUT("/canvas/:id/timeline", controller.SaveMyCanvasTimeline)
				selfRoute.POST("/canvas/:id/timeline/lock", controller.LockMyCanvasTimeline)
				selfRoute.POST("/canvas/:id/review", controller.ReviewMyCanvasShot)
				selfRoute.GET("/canvas/:id/production-report", controller.ProductionReportMyCanvas)
				selfRoute.POST("/canvas/:id/batch", middleware.UserCriticalRateLimit("task"), controller.CreateMyCanvasBatch)
				selfRoute.GET("/canvas/:id/batch", controller.GetMyCanvasBatch)
				selfRoute.POST("/canvas/:id/assist", middleware.UserCriticalRateLimit("task"), controller.CreateCanvasAssist)
				selfRoute.GET("/canvas/:id/assist/:task_id", controller.GetCanvasAssistPlan)
				// 对话式工作流生成（API.md 32）
				selfRoute.POST("/canvas/:id/agent", middleware.UserCriticalRateLimit("task"), controller.CreateCanvasAgent)
				selfRoute.GET("/canvas/:id/agent/:task_id", controller.GetCanvasAgentPlan)
				selfRoute.POST("/canvas/:id/agent/:task_id/retry", middleware.UserCriticalRateLimit("task"), controller.RetryCanvasAgent)
				selfRoute.POST("/batch/:id/:action", controller.BatchAction)
				selfRoute.GET("/compose", controller.GetMyComposeList)
				selfRoute.GET("/compose/:id", controller.GetMyCompose)
				selfRoute.POST("/work/:id/report", controller.ReportWork)
				selfRoute.POST("/work/:id/comment", middleware.UserCriticalRateLimit("comment"), controller.CreateWorkComment)
				selfRoute.DELETE("/comment/:id", controller.DeleteMyComment)
				selfRoute.POST("/comment/:id/report", controller.ReportComment)

				// Skill：收藏 / 我的
				selfRoute.GET("/skill", controller.GetMySkills)
				selfRoute.GET("/skill/favorites", controller.GetMyFavoriteSkills)
				selfRoute.GET("/skill/:id", controller.GetMySkill)
				selfRoute.POST("/skill", controller.CreateMySkill)
				selfRoute.POST("/skill/:id/submit", controller.SubmitMySkill)
				selfRoute.POST("/skill/:id/withdraw", controller.WithdrawMySkill)
				selfRoute.GET("/creator/summary", controller.GetCreatorSummary)
				selfRoute.POST("/creator/exchange", middleware.UserCriticalRateLimit("exchange"), controller.ExchangeCreatorRevenue)
				selfRoute.POST("/creator/withdraw", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("creator_withdraw"), controller.ApplyCreatorWithdraw)
				selfRoute.GET("/creator/withdraws", controller.GetCreatorWithdraws)
				selfRoute.POST("/ugc", middleware.UserCriticalRateLimit("ugc"), controller.SubmitMyUgc)
				selfRoute.GET("/ugc", controller.GetMyUgc)
				selfRoute.POST("/work/:id/enter", controller.EnterCampaign)
				// 关注（API.md 9.2）
				selfRoute.POST("/author/:id/follow", controller.ToggleFollowAuthor)
				selfRoute.GET("/following", controller.GetMyFollowingFeed)
				selfRoute.GET("/follow/authors", controller.GetMyFollowedAuthors)
				// 渠道伙伴（API.md 9.3）
				// 主体库（API.md 10.2）
				selfRoute.GET("/subject", controller.GetMySubjects)
				selfRoute.POST("/subject", controller.CreateMySubject)
				selfRoute.POST("/subject/from-asset", controller.CreateSubjectFromAsset)
				selfRoute.GET("/subject/:id/versions", controller.GetSubjectVersions)
				selfRoute.PUT("/subject/:id", controller.UpdateMySubject)
				selfRoute.DELETE("/subject/:id", controller.DeleteMySubject)
				selfRoute.POST("/subject/:id/generate", middleware.UserCriticalRateLimit("task"), controller.GenerateSubjectViews)
				selfRoute.POST("/subject/:id/sync", controller.SyncMySubjectRefs)
				// 工作流模板（API.md 31，L3）
				selfRoute.GET("/workflow", controller.GetMyWorkflows)
				selfRoute.POST("/workflow", controller.CreateMyWorkflow)
				selfRoute.GET("/workflow/:id", controller.GetMyWorkflow)
				selfRoute.PUT("/workflow/:id", controller.UpdateMyWorkflow)
				selfRoute.DELETE("/workflow/:id", controller.DeleteMyWorkflow)
				// 我的风格（API.md 34.5，L18）
				selfRoute.GET("/style", controller.GetMyStyles)
				selfRoute.POST("/style", controller.CreateMyStyle)
				selfRoute.PUT("/style/:id", controller.UpdateMyStyle)
				selfRoute.DELETE("/style/:id", controller.DeleteMyStyle)
				selfRoute.POST("/style/:id/submit", controller.SubmitMyStyle)
				selfRoute.POST("/style/:id/withdraw", controller.WithdrawMyStyle)
				// 片段重拍 / 续写 / 超分（API.md 10.3）
				selfRoute.POST("/task/:id/retake", middleware.UserCriticalRateLimit("task"), controller.RetakeMyTask)
				selfRoute.POST("/task/:id/extend", middleware.UserCriticalRateLimit("task"), controller.ExtendMyTask)
				selfRoute.POST("/task/:id/upscale", middleware.UserCriticalRateLimit("task"), controller.UpscaleMyTask)
				// 媒体工具：裁切 / 截取 / 分离 / 抽帧 / 去字幕 / 抠像等统一入口（API.md 23）
				selfRoute.GET("/media/actions", controller.GetMediaActions)
				selfRoute.POST("/media/jobs", middleware.UserCriticalRateLimit("media"), controller.CreateMediaJob)
				selfRoute.GET("/media/jobs", controller.GetMyMediaJobs)
				selfRoute.GET("/media/jobs/:id", controller.GetMyMediaJob)
				selfRoute.POST("/partner/apply", middleware.UserCriticalRateLimit("partner"), controller.ApplyPartner)
				selfRoute.GET("/partner/me", controller.GetMyPartner)
				selfRoute.PUT("/skill/:id", controller.UpdateMySkill)
				selfRoute.DELETE("/skill/:id", controller.DeleteMySkill)
				selfRoute.POST("/skill/:id/favorite", controller.ToggleSkillFavorite)

				// 站内通知
				selfRoute.GET("/notification", controller.GetMyNotifications)
				selfRoute.POST("/notification/read", controller.ReadMyNotifications)
				selfRoute.POST("/self/deactivate", middleware.CriticalRateLimit(), controller.DeactivateSelf)
				selfRoute.GET("/notification/prefs", controller.GetMyNotificationPrefs)
				selfRoute.PUT("/notification/prefs", controller.UpdateMyNotificationPrefs)

				// ===== 付费域（API.md 6.2）=====
				selfRoute.GET("/wallet", controller.GetMyWallet)
				selfRoute.GET("/growth/tasks", controller.GetMyGrowthTasks) // 新手任务（区别于生成任务 /user/task）
				selfRoute.POST("/checkin", middleware.UserCriticalRateLimit("checkin"), controller.Checkin)
				selfRoute.GET("/checkin/status", controller.GetCheckinStatus)
				selfRoute.GET("/invite/summary", controller.GetInviteSummary)
				selfRoute.GET("/invite/records", controller.GetInviteRecords)
				selfRoute.POST("/bonus-code/claim", middleware.UserCriticalRateLimit("bonus"), controller.ClaimBonusCode)
				selfRoute.GET("/bonus-code/record", controller.GetMyBonusRecord)
				selfRoute.GET("/usage", controller.GetMyUsage)
				selfRoute.GET("/ai-chat/quota", controller.GetMyChatQuota)                                          // AI 对话每日次数（与 /user/usage 同组，UserAuth + TeamSpace）
				selfRoute.POST("/ai-chat", middleware.UserCriticalRateLimit("task"), controller.CreateDirectorChat) // 导演助手：单轮文本，和画布对话共用次数
				selfRoute.GET("/preference", controller.GetMyPreferences)                                           // 用户偏好（API.md 33）：运镜收藏 / 最近等跨设备同步
				selfRoute.PUT("/preference/:key", controller.SetMyPreference)
				selfRoute.GET("/team/usage", controller.GetMyTeamUsage)
				selfRoute.GET("/membership", controller.GetMyMembership)
				selfRoute.GET("/points/log", controller.GetMyPointLog)
				selfRoute.GET("/points/lots", controller.GetMyPointLots)
				selfRoute.POST("/order", middleware.CriticalRateLimit(), controller.CreateMyOrder)
				selfRoute.GET("/order", controller.GetMyOrders)
				selfRoute.GET("/order/:id", controller.GetMyOrder)
				selfRoute.POST("/order/:id/pay", middleware.CriticalRateLimit(), controller.PayMyOrder)
				selfRoute.POST("/order/:id/close", controller.CloseMyOrder)

				// ===== 团队（API.md 6.7）=====
				selfRoute.GET("/team", controller.GetMyTeam)
				selfRoute.GET("/team/mine", controller.GetMyTeams) // 空间切换器（API.md 20）
				selfRoute.PUT("/team", controller.RenameMyTeam)
				selfRoute.POST("/team/invite", controller.RegenerateTeamInvite)
				selfRoute.POST("/team/join", middleware.CriticalRateLimit(), controller.JoinTeam)
				selfRoute.POST("/team/quit", controller.QuitTeam)
				selfRoute.DELETE("/team/member/:uid", middleware.CriticalRateLimit(), controller.KickTeamMember)
				selfRoute.PUT("/team/member/:uid/limit", controller.SetTeamMemberLimit)
				selfRoute.PUT("/team/member/:uid/permission", controller.SetTeamMemberPermission)

				// ===== 七牛云（UserAuth，前端直传凭证；镜像母版 easyapi-ai-api）=====
				selfRoute.GET("/qiniu/upload-token", controller.GetQiniuUploadToken)
				selfRoute.GET("/qiniu/key", controller.GetQiniuKey)
			}
		}
	}
}
