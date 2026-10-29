package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
)

// ===== 提示词远程源同步（API.md 1.11）=====
//
// 源就是一个可公开访问的地址（GitHub raw 的 .md / .json 最常见）。
// 抓下来按 format 解析成条目，再按 external_key upsert 进 prompt 表：
// 新条目插入，老条目只更新正文和原文链接，运营在后台改过的标题 / 封面不会被冲掉。

// promptSyncTimeout 单个源的抓取超时。GitHub raw 偶尔会慢，但也不该拖住整轮同步。
const promptSyncTimeout = 20 * time.Second

// promptFetchMaxBytes 单个源最多读这么多，防止误配一个大文件把内存吃满。
const promptFetchMaxBytes = 4 << 20 // 4MB

// PromptItem 解析出来的一条提示词。
type PromptItem struct {
	Title   string
	Content string
	Cover   string
	Tags    []string
}

var promptHTTPClient = &http.Client{Timeout: promptSyncTimeout}

// fetchPromptSource 拉取源内容。
func fetchPromptSource(url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	// GitHub 对没有 UA 的请求会限得更狠
	req.Header.Set("User-Agent", "easytv-prompt-sync")
	res, err := promptHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("拉取失败：HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, promptFetchMaxBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

var (
	// 二级 / 三级标题都当作一条提示词的开头
	mdHeading = regexp.MustCompile(`(?m)^#{2,3}\s+(.+?)\s*$`)
	// ```lang ... ``` 代码块
	mdCodeBlock = regexp.MustCompile("(?s)```[a-zA-Z]*\\n(.*?)```")
	// ![alt](url) 里的图片地址
	mdImage = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)`)
)

// ParseMarkdownPrompts 按标题切段，每段取第一个代码块当正文；
// 没有代码块就退回段落纯文本。这是那几个开源提示词仓库 README 的通用长相。
func ParseMarkdownPrompts(md string) []PromptItem {
	locs := mdHeading.FindAllStringSubmatchIndex(md, -1)
	out := []PromptItem{}
	for i, loc := range locs {
		title := strings.TrimSpace(md[loc[2]:loc[3]])
		start := loc[1]
		end := len(md)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := md[start:end]
		content := ""
		if m := mdCodeBlock.FindStringSubmatch(body); len(m) > 1 {
			content = strings.TrimSpace(m[1])
		} else {
			content = strings.TrimSpace(stripMarkdownNoise(body))
		}
		if title == "" || content == "" {
			continue
		}
		item := PromptItem{Title: title, Content: content}
		if m := mdImage.FindStringSubmatch(body); len(m) > 1 {
			item.Cover = m[1]
		}
		out = append(out, item)
	}
	return out
}

// stripMarkdownNoise 去掉图片、链接包装和多余空行，留下能直接用的文字。
func stripMarkdownNoise(s string) string {
	s = mdImage.ReplaceAllString(s, "")
	lines := []string{}
	for _, line := range strings.Split(s, "\n") {
		v := strings.TrimSpace(line)
		if v == "" || strings.HasPrefix(v, "<") || strings.HasPrefix(v, "|") {
			continue
		}
		lines = append(lines, v)
	}
	return strings.Join(lines, "\n")
}

// ParseJSONPrompts 解析对象数组。字段名按常见写法做兼容：
// title/name、content/prompt/text、cover/cover_url/image。
func ParseJSONPrompts(raw string) ([]PromptItem, error) {
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("解析 JSON 失败：%w", err)
	}
	out := []PromptItem{}
	for _, row := range rows {
		item := PromptItem{
			Title:   firstString(row, "title", "name", "topic"),
			Content: firstString(row, "content", "prompt", "text"),
			Cover:   firstString(row, "cover", "cover_url", "image"),
		}
		if item.Title == "" || item.Content == "" {
			continue
		}
		if tags, ok := row["tags"].([]any); ok {
			for _, tag := range tags {
				if v, ok := tag.(string); ok && strings.TrimSpace(v) != "" {
					item.Tags = append(item.Tags, strings.TrimSpace(v))
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func firstString(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := row[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// SyncPromptSource 同步一个源，返回本轮解析到的条目数与新增数。
// 结果（含失败原因）会回写到 prompt_source 上，后台能直接看到。
func SyncPromptSource(s *model.PromptSource) (total, created int, err error) {
	defer func() {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		model.MarkPromptSourceSynced(s.Id, total, msg)
	}()

	raw, err := fetchPromptSource(s.Url)
	if err != nil {
		return 0, 0, err
	}
	var items []PromptItem
	if s.Format == model.PromptFormatJSON {
		items, err = ParseJSONPrompts(raw)
		if err != nil {
			return 0, 0, err
		}
	} else {
		items = ParseMarkdownPrompts(raw)
	}
	if len(items) == 0 {
		return 0, 0, fmt.Errorf("没有解析到提示词，检查地址与格式是否匹配")
	}

	for _, item := range items {
		tags := item.Tags
		if s.Tag != "" {
			tags = append([]string{s.Tag}, tags...)
		}
		row := &model.Prompt{
			Title:       trimPromptRunes(item.Title, model.PromptTitleMax),
			Content:     trimPromptRunes(item.Content, model.PromptContentMax),
			CoverUrl:    trimPromptRunes(item.Cover, 1000),
			Tags:        trimPromptRunes(strings.Join(dedupTags(tags), ","), 255),
			SourceId:    s.Id,
			SourceName:  s.Name,
			SourceUrl:   s.Url,
			ExternalKey: model.PromptExternalKey(s.Id, item.Title),
			Status:      model.PromptStatusOn,
		}
		isNew, upErr := model.UpsertSourcePrompt(row)
		if upErr != nil {
			common.SysError("upsert prompt failed: " + upErr.Error())
			continue
		}
		if isNew {
			created++
		}
	}
	return len(items), created, nil
}

func dedupTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, tag := range tags {
		v := strings.TrimSpace(tag)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func trimPromptRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return strings.TrimSpace(s)
	}
	return string(r[:max])
}

// RunPromptSyncJob 定时任务：同步所有启用的源。单个源失败只记日志，不影响其他源。
func RunPromptSyncJob() {
	list, err := model.GetPromptSources(true)
	if err != nil {
		common.SysError("list prompt sources failed: " + err.Error())
		return
	}
	for _, s := range list {
		total, created, err := SyncPromptSource(s)
		if err != nil {
			common.SysError(fmt.Sprintf("sync prompt source %d(%s) failed: %s", s.Id, s.Name, err.Error()))
			continue
		}
		common.SysLog(fmt.Sprintf("sync prompt source %d(%s): %d items, +%d new", s.Id, s.Name, total, created))
	}
}
