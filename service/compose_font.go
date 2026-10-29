package service

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"easytv-api/common"
	"easytv-api/setting"
)

// 成片字幕 / 水印用的中文字体。
//
// 线上事故背景：服务器没装中文字体时，libass 找不到字形，中文字幕烧进去是空白或方块，
// drawtext 水印则直接让整条 ffmpeg 失败（回退成不带字幕的成片）。这里做三件事：
//  1. 运营可以用 setting 明确指定（ComposeFontFile 字体文件、ComposeSubtitleFont 字体名）；
//  2. 没指定就在常见路径里找一款中日韩字体，字幕额外带 fontsdir，这样不依赖 fontconfig 是否登记过；
//  3. 一款都找不到时写一条日志，并在成片 warning 里提示运营装字体。
//
// 装字体（Debian/Ubuntu）：apt-get install -y fonts-noto-cjk

// cjkFontCandidates 常见的中日韩字体路径，按优先级排；第一个存在的就用。
var cjkFontCandidates = []struct {
	Path string
	Name string // libass 的 FontName
}{
	{"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc", "Noto Sans CJK SC"},
	{"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc", "Noto Sans CJK SC"},
	{"/usr/share/fonts/opentype/noto/NotoSerifCJK-Regular.ttc", "Noto Serif CJK SC"},
	{"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc", "WenQuanYi Zen Hei"},
	{"/usr/share/fonts/wqy-zenhei/wqy-zenhei.ttc", "WenQuanYi Zen Hei"},
	{"/usr/share/fonts/truetype/arphic/uming.ttc", "AR PL UMing CN"},
	{"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf", "Droid Sans Fallback"},
	{"/System/Library/Fonts/PingFang.ttc", "PingFang SC"},
	{"/System/Library/Fonts/STHeiti Medium.ttc", "Heiti SC"},
	{"C:\\\\Windows\\\\Fonts\\\\msyh.ttc", "Microsoft YaHei"},
}

type composeFont struct {
	File string // 字体文件绝对路径，空 = 没找到
	Name string // libass FontName
}

var (
	detectedFontOnce sync.Once
	detectedFont     composeFont
)

// detectCJKFont 扫一遍候选路径，只扫一次（进程内缓存）。
func detectCJKFont() composeFont {
	detectedFontOnce.Do(func() {
		for _, c := range cjkFontCandidates {
			if st, err := os.Stat(c.Path); err == nil && !st.IsDir() {
				detectedFont = composeFont{File: c.Path, Name: c.Name}
				common.SysLog("compose: 使用中文字体 " + c.Path)
				return
			}
		}
		common.SysError("compose: 服务器没有找到中日韩字体，中文字幕可能显示为空白；请安装字体（apt-get install -y fonts-noto-cjk）或配置 setting ComposeFontFile")
	})
	return detectedFont
}

// ComposeFont 成片使用的字体：setting 指定优先，其次自动探测。
func ComposeFont() (file, name string) {
	file = strings.TrimSpace(setting.GetSettingString("ComposeFontFile"))
	name = strings.TrimSpace(setting.GetSettingString("ComposeSubtitleFont"))
	if file != "" && name != "" {
		return file, name
	}
	found := detectCJKFont()
	if file == "" {
		file = found.File
	}
	if name == "" {
		name = found.Name
	}
	return file, name
}

// hasCJK 文本里是否有中日韩字符（判断要不要提醒缺字体）。
func hasCJK(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// missingCJKFontWarning 字幕里有中文但服务器没有可用中文字体时的提示，正常返回空串。
func missingCJKFontWarning(srtPath string) string {
	if srtPath == "" {
		return ""
	}
	data, err := os.ReadFile(srtPath)
	if err != nil || !hasCJK(string(data)) {
		return ""
	}
	if file, name := ComposeFont(); file != "" || name != "" {
		return ""
	}
	return "服务器缺少中文字体，中文字幕可能显示为空白，请安装 fonts-noto-cjk"
}

// fontsDirOf 字体文件所在目录，给 libass 的 fontsdir 用（不依赖系统 fontconfig 登记）。
func fontsDirOf(file string) string {
	if file == "" {
		return ""
	}
	return filepath.Dir(file)
}
