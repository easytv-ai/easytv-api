package setting

// 应用功能开关（DB setting 表，PascalCase 键，管理端可热更新）。
const (
	// Director3DEnabled 用户端是否显示 3D 导演台入口（"true"/"1" 显示，空或缺省 = 显示）。
	Director3DEnabled = "Director3DEnabled"
)

// IsDirector3DEnabled 3D 导演台入口是否对用户端可见。
func IsDirector3DEnabled() bool {
	v := GetSettingString(Director3DEnabled)
	return v == "" || v == "true" || v == "1"
}
