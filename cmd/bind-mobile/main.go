// bind-mobile 给已有账号绑定手机号，之后可以用这个号短信登录进同一个账号（演示账号配合 SMS_TEST_ACCOUNTS 本地登录用）。
//
//	go run ./cmd/bind-mobile --user demo@easytv.ai --mobile 13800000000
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
)

func main() {
	username := flag.String("user", "demo@easytv.ai", "要绑定的账号（username）")
	mobile := flag.String("mobile", "", "手机号（11 位）")
	envFile := flag.String("env-file", "", "环境变量文件路径（默认 .env）")
	flag.Parse()
	if *envFile != "" {
		common.EnvFile = *envFile
	}
	if err := common.LoadEnvFile(); err != nil {
		fmt.Println("load env failed:", err)
	}
	common.InitEnv()
	m := strings.TrimSpace(*mobile)
	if !model.IsMobileFormat(m) {
		fmt.Println("手机号格式不正确")
		os.Exit(1)
	}
	if err := model.InitDB(); err != nil {
		panic(err)
	}
	user := &model.User{}
	if err := model.DB.Where("username = ?", strings.TrimSpace(*username)).First(user).Error; err != nil {
		fmt.Println("账号不存在:", *username)
		os.Exit(1)
	}
	if user.Mobile == m {
		fmt.Printf("user #%d %s 已绑定 %s\n", user.Id, user.Username, m)
		return
	}
	var other model.User
	if model.DB.Unscoped().Where("(username = ? OR mobile = ?) AND id <> ?", m, m, user.Id).Limit(1).Find(&other).RowsAffected > 0 {
		fmt.Printf("手机号 %s 已被账号 #%d %s 占用\n", m, other.Id, other.Username)
		os.Exit(1)
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("mobile", m).Error; err != nil {
		panic(err)
	}
	fmt.Printf("user #%d %s 已绑定手机号 %s\n", user.Id, user.Username, m)
}
