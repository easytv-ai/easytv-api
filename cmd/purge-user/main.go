// purge-user 物理清理某个（已注销）用户名下的内容：作品、评论、点赞、举报、画布及版本、项目、资产（含存储文件）、
// 生成任务、通知、Skill 与收藏、团队成员关系。用户行本身保留（软删状态），以便审计。
//
//	go run ./cmd/purge-user --user 13            # 使用 .env
//	go run ./cmd/purge-user --user 13 --dry-run   # 只统计不删除
package main

import (
	"flag"
	"fmt"
	"os"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
)

func main() {
	userId := flag.Int("user", 0, "用户 ID")
	dryRun := flag.Bool("dry-run", false, "只统计不删除")
	envFile := flag.String("env-file", "", "环境变量文件路径（默认 .env）")
	flag.Parse()
	if *userId <= 0 {
		fmt.Println("usage: purge-user --user <id> [--dry-run]")
		os.Exit(2)
	}
	if *envFile != "" {
		common.EnvFile = *envFile
	}
	if err := common.LoadEnvFile(); err != nil {
		fmt.Println("load env failed:", err)
	}
	common.InitEnv()
	if err := model.InitDB(); err != nil {
		panic(err)
	}
	service.InitStorage()

	var u model.User
	if err := model.DB.Unscoped().Where("id = ?", *userId).First(&u).Error; err != nil {
		fmt.Println("user not found:", err)
		os.Exit(1)
	}
	if u.DeletedAt.Valid {
		fmt.Printf("user %d (%s) is deactivated\n", u.Id, u.Username)
	} else {
		fmt.Printf("warning: user %d (%s) is ACTIVE; purging anyway\n", u.Id, u.Username)
	}

	type job struct {
		name  string
		model any
		where string
	}
	jobs := []job{
		{"work_like", &model.WorkLike{}, "user_id = ?"},
		{"work_comment", &model.WorkComment{}, "user_id = ?"},
		{"work_report", &model.WorkReport{}, "user_id = ?"},
		{"work", &model.Work{}, "user_id = ?"},
		{"canvas_version", &model.CanvasVersion{}, "user_id = ?"},
		{"canvas", &model.Canvas{}, "user_id = ?"},
		{"project", &model.Project{}, "user_id = ?"},
		{"generation_task", &model.GenerationTask{}, "user_id = ?"},
		{"notification", &model.Notification{}, "user_id = ?"},
		{"user_notification_pref", &model.UserNotificationPref{}, "user_id = ?"},
		{"skill_favorite", &model.SkillFavorite{}, "user_id = ?"},
		{"skill", &model.Skill{}, "user_id = ?"},
		{"team_member", &model.TeamMember{}, "user_id = ?"},
	}

	// 资产：先删存储文件再删记录
	var assets []*model.Asset
	model.DB.Unscoped().Where("user_id = ?", *userId).Find(&assets)
	fmt.Printf("asset: %d rows\n", len(assets))
	if !*dryRun {
		for _, a := range assets {
			if a.StorageKey != "" && service.DefaultStorage != nil {
				if err := service.DefaultStorage.Remove(a.StorageKey); err != nil {
					fmt.Printf("  remove file %s failed: %v\n", a.StorageKey, err)
				}
			}
		}
		model.DB.Unscoped().Where("user_id = ?", *userId).Delete(&model.Asset{})
	}

	for _, j := range jobs {
		var n int64
		model.DB.Unscoped().Model(j.model).Where(j.where, *userId).Count(&n)
		fmt.Printf("%s: %d rows\n", j.name, n)
		if !*dryRun && n > 0 {
			if err := model.DB.Unscoped().Where(j.where, *userId).Delete(j.model).Error; err != nil {
				fmt.Printf("  delete failed: %v\n", err)
			}
		}
	}
	if *dryRun {
		fmt.Println("dry-run: nothing deleted")
	} else {
		fmt.Println("purge done")
	}
}
