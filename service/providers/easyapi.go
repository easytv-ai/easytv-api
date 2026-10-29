// Package providers 生成厂商接入。EasyTV 只接自家的 EasyAPI 大模型网关（new-api 体系），
// 不直连任何模型厂商；网关侧负责路由到 Kling / 即梦 / Vidu / Sora 等。
//
// 网关契约（OpenAI 兼容 + new-api 统一视频接口）：
//   - 文本：POST /v1/chat/completions
//   - 图片：POST /v1/images/generations
//   - 语音：POST /v1/audio/speech（返回二进制音频）
//   - 视频：POST /v1/videos，GET /v1/videos/{job_id} 轮询；响应兼容包裹格式 {code,data:{job_id,status,download_url}}
//     与直通格式 {id,status,progress,error}，成功但没有地址时取 GET /v1/videos/{job_id}/content，
//     素材用 input_references[{type,url,role}] 数组：first_frame/last_frame/reference（image）、reference_video、reference_audio。
//
// 网关配置（DB setting 表，管理端 /admin/gateway/config 热更新）：
//   - EasyAPIGatewayKey       网关令牌（必填，缺省不注册 provider）
//   - EasyAPIGatewayBaseUrl   默认 https://api.easyapi.com
//   - EasyAPIGatewayEnabled   false/0 关闭；缺省有 Key 即开启
//
// 模型清单 = model_price 表（status=1），单一数据源，管理端从网关拉取 / 自行定价。
package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"
)

const (
	easyAPIProviderName  = "easyapi"
	easyAPIPollInterval  = 8 * time.Second
	easyAPIPollTimeout   = 30 * time.Minute
	easyAPIDownloadLimit = 512 << 20 // 512 MB
	// videoNotFoundLimit 轮询连续 404 这么多次（约 40 秒）判定任务不存在
	videoNotFoundLimit = 5
)

// resultHTTP 下载网关产物用的客户端。默认 http.Get 没有总超时，产物地址挂起就永远占着协程。
var resultHTTP = &http.Client{Timeout: 10 * time.Minute}

var errResultTooLarge = fmt.Errorf("产物超过 %d MB 上限", easyAPIDownloadLimit>>20)

// EasyAPIProvider 通过 EasyAPI 网关执行生成任务。
type EasyAPIProvider struct {
	baseUrl string
	apiKey  string
	models  []service.ModelInfo
	client  *http.Client
}

// RegisterProviderFromSettings 按 DB setting 注册网关 provider（main() 在 InitDB / InitSettingMap 之后调用）。
func RegisterProviderFromSettings() {
	p := NewFromSettings()
	if p == nil {
		return
	}
	service.RegisterProvider(p)
	common.SysLog("generation provider registered: easyapi (models read from model_price)")
	p.ResumeVideoTasks()
}

// RefreshProviderFromSettings 按当前 setting 重建 provider 并替换注册（后台周期调用）。
// 网关关闭则注销；重建后新任务走新配置，在跑任务沿用旧实例不打断。
func RefreshProviderFromSettings() {
	if !setting.IsGatewayEnabled() {
		service.RemoveProvider(easyAPIProviderName)
		return
	}
	p := NewFromSettings()
	if p == nil {
		service.RemoveProvider(easyAPIProviderName)
		return
	}
	service.ReplaceProvider(easyAPIProviderName, p)
}

// NewFromSettings 依据 DB setting 构造 provider；未启用返回 nil。
func NewFromSettings() *EasyAPIProvider {
	if !setting.IsGatewayEnabled() {
		return nil
	}
	return NewEasyAPIProvider(
		setting.GetGatewayBaseUrl(),
		setting.GetGatewayKey(),
		nil, // 模型清单由 Models() 从 model_price 读取
	)
}

// NewEasyAPIProvider 构造 provider（测试可注入自定义网关地址）。
func NewEasyAPIProvider(baseUrl, apiKey string, models []service.ModelInfo) *EasyAPIProvider {
	return &EasyAPIProvider{
		baseUrl: strings.TrimRight(baseUrl, "/"),
		apiKey:  apiKey,
		models:  models,
		client:  &http.Client{Timeout: 180 * time.Second},
	}
}

// ModelsFromEnv 读取四类环境变量模型清单（仅存量迁移用，见 model.SeedModelsFromEnvIfEmpty）。
func ModelsFromEnv() []service.ModelInfo {
	var list []service.ModelInfo
	list = append(list, ParseModelList(model.TaskTypeText, common.GetEnvOrDefaultString("EASYAPI_TEXT_MODELS", ""))...)
	list = append(list, ParseModelList(model.TaskTypeImage, common.GetEnvOrDefaultString("EASYAPI_IMAGE_MODELS", ""))...)
	list = append(list, ParseModelList(model.TaskTypeVideo, common.GetEnvOrDefaultString("EASYAPI_VIDEO_MODELS", ""))...)
	list = append(list, ParseModelList(model.TaskTypeAudio, common.GetEnvOrDefaultString("EASYAPI_AUDIO_MODELS", ""))...)
	return list
}

// ParseModelList 解析 "model:label,model2" 形式的清单。
func ParseModelList(taskType, raw string) []service.ModelInfo {
	var list []service.ModelInfo
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, label := item, item
		if idx := strings.Index(item, ":"); idx > 0 {
			name = strings.TrimSpace(item[:idx])
			label = strings.TrimSpace(item[idx+1:])
			if label == "" {
				label = name
			}
		}
		list = append(list, service.ModelInfo{Provider: easyAPIProviderName, Model: name, Type: taskType, Label: label})
	}
	return list
}

func (p *EasyAPIProvider) Name() string { return easyAPIProviderName }

// Models 可用模型清单：构造时显式注入的优先（测试用），否则读 model_price（status=1）作为单一数据源。
func (p *EasyAPIProvider) Models() []service.ModelInfo {
	if len(p.models) > 0 {
		return p.models
	}
	prices, err := model.GetModelPrices(true)
	if err != nil {
		return nil
	}
	list := make([]service.ModelInfo, 0, len(prices))
	for _, mp := range prices {
		list = append(list, service.ModelInfo{
			Provider: easyAPIProviderName,
			Model:    mp.Model,
			Type:     mp.Type,
			Label:    mp.Label,
		})
	}
	return list
}

// Submit 按任务类型分发；全部异步执行，状态通过 model.SetTask* 推进。
func (p *EasyAPIProvider) Submit(task *model.GenerationTask) error {
	if strings.TrimSpace(task.Prompt) == "" {
		return errors.New("请填写提示词")
	}
	if service.DefaultStorage == nil && task.Type != model.TaskTypeText {
		return errors.New("存储未初始化")
	}
	if err := model.SetTaskRunning(task.Id); err != nil {
		return err
	}
	switch task.Type {
	case model.TaskTypeText:
		go p.runText(task)
	case model.TaskTypeImage:
		go p.runImage(task)
	case model.TaskTypeVideo:
		go p.runVideo(task)
	case model.TaskTypeAudio:
		go p.runAudio(task)
	default:
		return errors.New("不支持的任务类型")
	}
	return nil
}

// ===== 任务参数 =====

type taskParams map[string]any

func (t taskParams) str(key string) string {
	switch v := t[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func (t taskParams) num(key string) float64 {
	switch v := t[key].(type) {
	case float64:
		return v
	case string:
		f, _ := strconv.ParseFloat(strings.TrimRight(v, "sS"), 64)
		return f
	}
	return 0
}

// boolVal 读取布尔参数；"true"/"1"/"false"/"0" 字符串与 JSON bool 均可，缺失返回 (false, false)。
func (t taskParams) boolVal(key string) (bool, bool) {
	switch v := t[key].(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}

// intVal 读取整数参数；JSON 数字与整数字符串均可，缺失返回 (0, false)。
func (t taskParams) intVal(key string) (int, bool) {
	switch v := t[key].(type) {
	case float64:
		return int(v), true
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i, true
		}
	}
	return 0, false
}

func parseParams(raw string) taskParams {
	var p taskParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil || p == nil {
		return taskParams{}
	}
	return p
}

// imageSizeFor 按模型族与清晰度推图片尺寸（任务没显式传 size 时）。
//   - gpt-image 系只收 1024x1024 / 1536x1024 / 1024x1536（1792 是 DALL·E 3 的尺寸，edits 会回 400）；
//   - dall-e 沿用 1792x1024 / 1024x1792；
//   - 其他（seedream / qwen-image / gemini 等）：清晰度 2K 时给 2K 像素尺寸，否则沿用原来的映射。
func imageSizeFor(modelName, ratio, clarity string) string {
	m := strings.ToLower(modelName)
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	orientation := ratioOrientation(ratio)
	switch {
	case strings.HasPrefix(m, "gpt-image"), strings.HasPrefix(m, "chatgpt-image"):
		switch orientation {
		case "landscape":
			return "1536x1024"
		case "portrait":
			return "1024x1536"
		}
		return "1024x1024"
	case strings.HasPrefix(m, "dall-e"):
		return sizeFromRatio(ratio)
	}
	if strings.EqualFold(strings.TrimSpace(clarity), "2K") {
		switch orientation {
		case "landscape":
			return "2560x1440"
		case "portrait":
			return "1440x2560"
		}
		return "2048x2048"
	}
	return sizeFromRatio(ratio)
}

// ratioOrientation 比例 → landscape / portrait / square。
func ratioOrientation(ratio string) string {
	switch ratio {
	case "16:9", "21:9", "3:2", "4:3", "2:1", "5:4":
		return "landscape"
	case "9:16", "9:21", "2:3", "3:4", "1:2", "4:5":
		return "portrait"
	}
	return "square"
}

// sizeFromRatio 比例 → OpenAI 图片 size。
func sizeFromRatio(ratio string) string {
	switch ratio {
	case "16:9", "21:9", "3:2", "4:3", "2:1", "5:4":
		return "1792x1024"
	case "9:16", "9:21", "2:3", "3:4", "1:2", "4:5":
		return "1024x1792"
	default:
		return "1024x1024"
	}
}

// ===== 视频素材（/v1/videos 的 input_references）=====

// InputReference 视频 API 的参考素材项（input_references[]）。
type InputReference struct {
	Type string `json:"type"` // image / video / audio
	Url  string `json:"url"`
	Role string `json:"role"` // first_frame / last_frame / reference / reference_video / reference_audio
}

// VideoRoleType 素材角色 → (input_references.type, 归一化 role)；"image" 向后兼容归一为 first_frame。
// 不支持的返回 ("", "", false)。
func VideoRoleType(role string) (string, string, bool) {
	switch role {
	case "first_frame", "image":
		return "image", "first_frame", true
	case "last_frame":
		return "image", "last_frame", true
	case "reference", "character", "scene", "style", "subject":
		// 画布「角色 / 场景 / 风格」参考与主体库参考图：网关只认 reference
		return "image", "reference", true
	case "reference_video":
		return "video", "reference_video", true
	case "reference_audio", "voice":
		return "audio", "reference_audio", true
	}
	return "", "", false
}

// taskInputAsset 取任务输入素材：自己的资产，或任务所在团队空间里的资产 / 该团队成员的资产
// （团队主体库的参考图属于创建主体的队友，可能是他个人空间上传的，API.md 20）。
// 用户直接提交的 inputs 已在创建接口按任务空间校验过，这里放宽只影响服务端并入的主体参考图。个人空间任务不放宽。
func taskInputAsset(task *model.GenerationTask, assetId int) (*model.Asset, error) {
	if a, err := model.GetUserAsset(task.UserId, assetId); err == nil {
		return a, nil
	}
	a, err := model.GetAssetByIdUnscoped(assetId)
	if err != nil || a.DeletedAt.Valid {
		return nil, fmt.Errorf("输入素材不存在：%d", assetId)
	}
	if task.TeamId <= 0 || (a.TeamId != task.TeamId && service.TeamIdOf(a.UserId) != task.TeamId) {
		return nil, fmt.Errorf("输入素材不存在：%d", assetId)
	}
	return a, nil
}

// ExtractInputReferences 从任务 inputs（[{asset_id, role}]）构建参考素材列表：
// 校验素材归属当前用户、URL 可公网访问、资产类型与角色匹配，并按角色推导 input_references.type。
func ExtractInputReferences(task *model.GenerationTask) ([]InputReference, error) {
	var inputs []struct {
		AssetId int    `json:"asset_id"`
		Role    string `json:"role"`
	}
	_ = json.Unmarshal([]byte(task.Inputs), &inputs)
	refs := make([]InputReference, 0, len(inputs))
	for _, in := range inputs {
		if in.Role == "mask" {
			// 蒙版只对图片局部修改有意义，视频任务忽略
			continue
		}
		refType, refRole, ok := VideoRoleType(in.Role)
		if !ok {
			return nil, fmt.Errorf("不支持的素材角色：%s", in.Role)
		}
		asset, err := taskInputAsset(task, in.AssetId)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(asset.Url, "http") {
			return nil, errors.New("视频生成的素材必须是可公网访问的地址（请配置 UPLOAD_BASE_URL）")
		}
		if asset.Type != refType {
			return nil, fmt.Errorf("素材 %d 类型不符：角色 %s 需要 %s 素材", in.AssetId, in.Role, refType)
		}
		refs = append(refs, InputReference{Type: refType, Url: asset.Url, Role: refRole})
	}
	return refs, nil
}

// ===== 文本 =====

// Chat 调用 /v1/chat/completions，返回首个回复文本。
func (p *EasyAPIProvider) Chat(modelName, prompt string) (string, error) {
	return p.ChatWithOptions(modelName, prompt, service.ChatParams{})
}

// chatRequestBody 组装 /v1/chat/completions 请求体。ChatParams 全零时只有 model + messages，
// 与历史请求逐字段一致；只有配置非空时才追加可选字段（网关（OpenAI 兼容）对未知字段是静默忽略）。
func chatRequestBody(modelName, prompt string, opts service.ChatParams) map[string]any {
	body := map[string]any{
		"model":    modelName,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	}
	if opts.MaxTokens > 0 {
		body["max_tokens"] = opts.MaxTokens
	}
	if opts.ThinkingEffort != "" {
		body["reasoning_effort"] = opts.ThinkingEffort
	}
	switch opts.ThinkingMode {
	case "on":
		body["thinking"] = map[string]string{"type": "enabled"}
	case "off":
		body["thinking"] = map[string]string{"type": "disabled"}
	}
	return body
}

// ChatWithOptions 调用 /v1/chat/completions，返回首个回复文本。
// opts 非零时追加 max_tokens / reasoning_effort / thinking（来自 ChatMaxTokens / ChatThinkingEffort / ChatThinkingMode）。
func (p *EasyAPIProvider) ChatWithOptions(modelName, prompt string, opts service.ChatParams) (string, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := p.call(http.MethodPost, "/v1/chat/completions", chatRequestBody(modelName, prompt, opts), &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("网关未返回内容")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// chatImageMax 文本任务随消息带的图片上限（反推提示词等看图场景）
const chatImageMax = 4

// ChatWithImages 带图片的 /v1/chat/completions：content 用 text + image_url 数组（OpenAI 视觉格式）。
// 图片地址是公网 http(s) 时直接给 URL，否则读字节转 data URL。
func (p *EasyAPIProvider) ChatWithImages(modelName, prompt string, images []*model.Asset) (string, error) {
	if len(images) == 0 {
		return p.Chat(modelName, prompt)
	}
	content := []map[string]any{{"type": "text", "text": prompt}}
	for _, a := range images {
		url, err := chatImageUrl(a)
		if err != nil {
			return "", err
		}
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}})
	}
	body := map[string]any{
		"model":    modelName,
		"messages": []map[string]any{{"role": "user", "content": content}},
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := p.call(http.MethodPost, "/v1/chat/completions", body, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("网关未返回内容")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func chatImageUrl(a *model.Asset) (string, error) {
	if strings.HasPrefix(a.Url, "https://") || strings.HasPrefix(a.Url, "http://") {
		return a.Url, nil
	}
	img, err := readAssetImage(a)
	if err != nil {
		return "", err
	}
	return "data:" + http.DetectContentType(img.Data) + ";base64," + base64.StdEncoding.EncodeToString(img.Data), nil
}

func (p *EasyAPIProvider) runText(task *model.GenerationTask) {
	// 对话式工作流生成（API.md 32）：一个任务里跑多轮 ReAct，循环自己结束任务（成功/失败/退差）。
	if service.IsAgentTask(task) {
		service.RunAgentLoop(task, p) // *EasyAPIProvider 满足 service.AgentChatter（Chat，easyapi.go:397）
		return
	}
	refs, _, err := imageTaskRefs(task)
	if err != nil {
		_ = service.FailTask(task.Id, err.Error())
		return
	}
	if len(refs) > chatImageMax {
		refs = refs[:chatImageMax]
	}
	text, err := p.ChatWithImages(task.Model, task.Prompt, refs)
	if err != nil {
		_ = service.FailTask(task.Id, err.Error())
		return
	}
	_ = service.CompleteTask(task.Id, 0, "", text)
}

// ===== 图片 =====

// GenerateImage 调用 /v1/images/generations，返回图片 URL 或 base64 数据。
func (p *EasyAPIProvider) GenerateImage(modelName, prompt, size string) (url string, b64 string, err error) {
	return p.generateImage(modelName, prompt, size, "", nil)
}

// generateImage 文生图；imageUrls 非空时按 url 方式带参考图（seedream / qwen-image / gemini image 等，
// 单张给字符串、多张给数组，与火山方舟 / 通义的 image 参数一致）。
func (p *EasyAPIProvider) generateImage(modelName, prompt, size, quality string, imageUrls []string) (string, string, error) {
	body := map[string]any{
		"model":  modelName,
		"prompt": prompt,
		"n":      1,
	}
	if size != "" {
		body["size"] = size
	}
	if quality != "" {
		body["quality"] = quality
	}
	switch len(imageUrls) {
	case 0:
	case 1:
		body["image"] = imageUrls[0]
	default:
		body["image"] = imageUrls
	}
	var resp imageResponse
	if err := p.call(http.MethodPost, "/v1/images/generations", body, &resp); err != nil {
		return "", "", err
	}
	return resp.first()
}

type imageResponse struct {
	Data []struct {
		Url     string `json:"url"`
		B64Json string `json:"b64_json"`
	} `json:"data"`
}

func (r imageResponse) first() (string, string, error) {
	if len(r.Data) == 0 {
		return "", "", errors.New("网关未返回图片")
	}
	return r.Data[0].Url, r.Data[0].B64Json, nil
}

// ImageFile 表单上传的一张图。
type ImageFile struct {
	Name string
	Data []byte
}

// EditImage 调用 /v1/images/edits（OpenAI 图像模型）：multipart 上传原图 / 参考图与可选蒙版。
// 一张图用 image 字段，多张用 image[]（与 OpenAI SDK 一致）；每个文件带真实 Content-Type，否则 OpenAI 回 unsupported mimetype。
func (p *EasyAPIProvider) EditImage(modelName, prompt, size, quality string, images []ImageFile, mask *ImageFile) (string, string, error) {
	if len(images) == 0 {
		return "", "", errors.New("图片编辑缺少原图")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields := map[string]string{"model": modelName, "prompt": prompt, "n": "1"}
	if size != "" {
		fields["size"] = size
	}
	if quality != "" {
		fields["quality"] = quality
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return "", "", err
		}
	}
	field := "image"
	if len(images) > 1 {
		field = "image[]"
	}
	for _, img := range images {
		if err := writeImagePart(w, field, img); err != nil {
			return "", "", err
		}
	}
	if mask != nil {
		if err := writeImagePart(w, "mask", *mask); err != nil {
			return "", "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", "", err
	}
	raw, err := p.doRaw(http.MethodPost, "/v1/images/edits", &buf, w.FormDataContentType())
	if err != nil {
		return "", "", err
	}
	var resp imageResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", fmt.Errorf("网关响应解析失败：%s", strings.TrimSpace(string(raw)))
	}
	return resp.first()
}

func writeImagePart(w *multipart.Writer, field string, img ImageFile) error {
	ct := http.DetectContentType(img.Data)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, img.Name))
	h.Set("Content-Type", ct)
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = part.Write(img.Data)
	return err
}

// OpenAIMaskFromMark 画布「标记区域」导出的是涂抹处不透明白色、其余全透明的 PNG（CanvasMarkModal），
// OpenAI images/edits 的约定正好相反：透明处才是要改的区域。这里把 alpha 反转后重新编码成 PNG。
func OpenAIMaskFromMark(data []byte) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("蒙版不是有效的 PNG：%v", err)
	}
	b := src.Bounds()
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			if a > 0x0c0c {
				out.SetNRGBA(x, y, color.NRGBA{}) // 标记处 → 透明（要改）
			} else {
				out.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 255}) // 其余 → 不透明（保留）
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// 图片任务里当作「原图 / 参考图」的素材角色；mask 单独处理。
var imageRefRoles = map[string]bool{
	"image": true, "reference": true, "first_frame": true, "character": true, "scene": true, "style": true, "subject": true,
}

const (
	imageRefMax      = 10
	imageRefMaxBytes = 50 << 20
)

// imageTaskRefs 取图片任务的参考图与蒙版素材（本人或同团队资产，类型必须是图片）。
func imageTaskRefs(task *model.GenerationTask) (refs []*model.Asset, mask *model.Asset, err error) {
	var inputs []struct {
		AssetId int    `json:"asset_id"`
		Role    string `json:"role"`
	}
	_ = json.Unmarshal([]byte(task.Inputs), &inputs)
	seen := map[int]bool{}
	for _, in := range inputs {
		if in.AssetId <= 0 || seen[in.AssetId] || (in.Role != "mask" && !imageRefRoles[in.Role]) {
			continue
		}
		seen[in.AssetId] = true
		asset, err := taskInputAsset(task, in.AssetId)
		if err != nil {
			return nil, nil, err
		}
		if asset.Type != model.AssetTypeImage {
			return nil, nil, fmt.Errorf("素材 %d 不是图片，不能作为参考图", in.AssetId)
		}
		if in.Role == "mask" {
			mask = asset
			continue
		}
		if len(refs) < imageRefMax {
			refs = append(refs, asset)
		}
	}
	return refs, mask, nil
}

// readAssetImage 读素材字节（edits 表单上传用）：本地存储读盘，绝对地址下载。
func readAssetImage(a *model.Asset) (ImageFile, error) {
	name := path.Base(a.Url)
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	if name == "" || name == "." || name == "/" {
		name = fmt.Sprintf("asset-%d.png", a.Id)
	}
	var r io.ReadCloser
	if strings.HasPrefix(a.Url, "/upload/") {
		key := strings.TrimPrefix(a.Url, "/upload/")
		if strings.Contains(key, "..") {
			return ImageFile{}, errors.New("无效的素材地址")
		}
		f, err := os.Open(filepath.Join(service.UploadDir, filepath.FromSlash(key)))
		if err != nil {
			return ImageFile{}, fmt.Errorf("读取素材 %d 失败：%v", a.Id, err)
		}
		r = f
	} else {
		body, err := openResult(a.Url)
		if err != nil {
			return ImageFile{}, fmt.Errorf("读取素材 %d 失败：%v", a.Id, err)
		}
		r = body
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, imageRefMaxBytes+1))
	if err != nil {
		return ImageFile{}, err
	}
	if len(data) > imageRefMaxBytes {
		return ImageFile{}, fmt.Errorf("素材 %d 超过 %d MB", a.Id, imageRefMaxBytes>>20)
	}
	return ImageFile{Name: name, Data: data}, nil
}

// imageQuality 画质档位 → OpenAI 图像模型的 quality；其他家模型不认这个参数，不传。
func imageQuality(mode string, params taskParams) string {
	if mode != model.ImageInputEdits {
		return ""
	}
	// 画布深度提取等传的清晰度：gpt-image 系尺寸封顶 1536，2K 用高画质代替
	if strings.EqualFold(params.str("clarity"), "2K") && params.str("quality") == "" {
		return "high"
	}
	switch params.str("quality") {
	case "low":
		return "low"
	case "std", "medium":
		return "medium"
	case "high", "hd":
		return "high"
	}
	return ""
}

func (p *EasyAPIProvider) runImage(task *model.GenerationTask) {
	params := parseParams(task.Params)
	size := params.str("size")
	if size == "" {
		size = imageSizeFor(task.Model, params.str("ratio"), params.str("clarity"))
	}
	mode := service.ImageInputModeOf(task.Model)
	refs, mask, err := imageTaskRefs(task)
	if err != nil {
		_ = service.FailTask(task.Id, err.Error())
		return
	}
	quality := imageQuality(model.InferImageInput(task.Model), params)
	var url, b64 string
	switch {
	case len(refs) == 0 && mask != nil:
		err = errors.New("局部修改缺少原图")
	case len(refs) == 0 || mode == model.ImageInputNone:
		// 不支持参考图的模型：用户直接提交的参考图在创建接口已拒绝，走到这里的只有主体库并入的参考图，保留提示词里的主体描述
		url, b64, err = p.generateImage(task.Model, task.Prompt, size, quality, nil)
	case mode == model.ImageInputEdits:
		files := make([]ImageFile, 0, len(refs))
		for _, a := range refs {
			f, readErr := readAssetImage(a)
			if readErr != nil {
				err = readErr
				break
			}
			files = append(files, f)
		}
		var maskFile *ImageFile
		if err == nil && mask != nil {
			f, readErr := readAssetImage(mask)
			if readErr == nil {
				f.Data, readErr = OpenAIMaskFromMark(f.Data)
			}
			if readErr != nil {
				err = readErr
			} else {
				f.Name = "mask.png"
				maskFile = &f
			}
		}
		if err == nil {
			url, b64, err = p.EditImage(task.Model, task.Prompt, size, quality, files, maskFile)
		}
	default:
		urls := make([]string, 0, len(refs))
		for _, a := range refs {
			if !strings.HasPrefix(a.Url, "http") {
				err = errors.New("参考图必须是可公网访问的地址（请配置 UPLOAD_BASE_URL）")
				break
			}
			urls = append(urls, a.Url)
		}
		if err == nil && mask != nil {
			err = fmt.Errorf("模型 %s 不支持蒙版局部修改，请换 gpt-image 系模型", task.Model)
		}
		if err == nil {
			url, b64, err = p.generateImage(task.Model, task.Prompt, size, quality, urls)
		}
	}
	if err != nil {
		_ = service.FailTask(task.Id, err.Error())
		return
	}
	var reader io.Reader
	if b64 != "" {
		data, decErr := base64.StdEncoding.DecodeString(b64)
		if decErr != nil {
			_ = service.FailTask(task.Id, "图片数据解码失败")
			return
		}
		reader = bytes.NewReader(data)
	} else {
		body, getErr := openResult(url)
		if getErr != nil {
			_ = service.FailTask(task.Id, "下载图片失败："+getErr.Error())
			return
		}
		defer body.Close()
		reader = body
	}
	p.finishWithAsset(task, reader, ".png", "image/png", model.AssetTypeImage)
}

// ===== 视频 =====

// videoTask 网关视频任务状态（已归一化，由 /v1/videos 响应解析而来）。
type videoTask struct {
	TaskId    string
	RawStatus string
	Status    string // videoStatus* 之一
	Url       string // 成片下载地址（download_url 等，见 videoResultUrl）；成功但为空时走 /v1/videos/{id}/content
	Error     string
	Progress  int // 网关给的 0-100 进度，没给为 -1
}

const (
	videoStatusQueued    = "queued"
	videoStatusRunning   = "running"
	videoStatusSucceeded = "succeeded"
	videoStatusFailed    = "failed"
	videoStatusUnknown   = "unknown"
)

// normalizeVideoStatus 各家状态字归一化。
func normalizeVideoStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "queued", "pending", "submitted", "not_start", "waiting", "in_queue":
		return videoStatusQueued
	case "processing", "in_progress", "running", "generating", "started":
		return videoStatusRunning
	case "succeeded", "success", "completed", "complete", "finished", "done":
		return videoStatusSucceeded
	case "failed", "failure", "error", "cancelled", "canceled", "expired":
		return videoStatusFailed
	}
	return videoStatusUnknown
}

// videosAPIData /v1/videos 创建 / 查询响应里用得到的字段（已从各种格式里归一化）。
type videosAPIData struct {
	JobId       string
	Status      string
	DownloadUrl string
	Message     string
	Progress    int // 没给为 -1
}

// parseVideosAPIResponse 解析 /v1/videos 响应。网关实际出现过、或同类网关常见的格式都兼容：
//   - 包裹格式：{"code":200|"200"|"success", "msg":"", "data":{"job_id":"...", "download_url":"..."}}
//   - 直通格式（OpenAI / new-api 视频对象）：{"id":"task_xxx", "status":"completed", "progress":100,
//     "error":{"message":"..."}, "metadata":{"url":"..."}}
//
// 字段类型不做强约束（code/id 可能是字符串或数字），避免一个类型不符就整条解析失败、任务白等 30 分钟。
// 包裹格式 code 非成功时返回带 msg 的错误。
func parseVideosAPIResponse(raw []byte) (*videosAPIData, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	obj := top
	if inner, ok := top["data"].(map[string]any); ok {
		if code, has := top["code"]; has && !videoCodeOK(code) {
			return nil, errors.New(firstNonEmpty(anyString(top["msg"]), anyString(top["message"]), "网关返回错误码 "+anyString(code)))
		}
		obj = inner
	}
	data := &videosAPIData{
		JobId:       firstNonEmpty(anyString(obj["job_id"]), anyString(obj["id"]), anyString(obj["task_id"])),
		Status:      firstNonEmpty(anyString(obj["status"]), anyString(obj["state"])),
		DownloadUrl: videoResultUrl(obj),
		Message:     videoErrorMessage(obj),
		Progress:    -1,
	}
	if p, ok := anyNumber(obj["progress"]); ok {
		data.Progress = int(p)
	}
	if data.JobId == "" && data.Status == "" {
		return nil, errors.New("网关未返回任务 ID")
	}
	return data, nil
}

func videoCodeOK(code any) bool {
	switch strings.ToLower(anyString(code)) {
	case "200", "0", "success", "ok":
		return true
	}
	return false
}

// videoResultUrl 按常见字段名找成片地址。
func videoResultUrl(obj map[string]any) string {
	for _, k := range []string{"download_url", "url", "video_url", "result_url"} {
		if s := anyString(obj[k]); strings.HasPrefix(s, "http") {
			return s
		}
	}
	for _, k := range []string{"metadata", "output", "result"} {
		if m, ok := obj[k].(map[string]any); ok {
			if s := videoResultUrl(m); s != "" {
				return s
			}
		}
	}
	for _, k := range []string{"unsigned_urls", "urls", "video_urls"} {
		if arr, ok := obj[k].([]any); ok && len(arr) > 0 {
			if s := anyString(arr[0]); strings.HasPrefix(s, "http") {
				return s
			}
		}
	}
	return ""
}

// videoErrorMessage 失败原因：error 可能是对象或字符串，也可能放在 message / fail_reason。
func videoErrorMessage(obj map[string]any) string {
	switch e := obj["error"].(type) {
	case map[string]any:
		if s := firstNonEmpty(anyString(e["message"]), anyString(e["code"])); s != "" {
			return s
		}
	case string:
		if e != "" {
			return e
		}
	}
	if s := firstNonEmpty(anyString(obj["fail_reason"]), anyString(obj["failure_reason"]), anyString(obj["error_message"]),
		anyString(obj["err_msg"]), anyString(obj["reason"]), anyString(obj["status_message"]),
		anyString(obj["message"]), anyString(obj["msg"])); s != "" {
		return s
	}
	// 有的上游把原因放在 metadata / result / output 里
	for _, k := range []string{"metadata", "result", "output"} {
		if m, ok := obj[k].(map[string]any); ok {
			if s := videoErrorMessage(m); s != "" {
				return s
			}
		}
	}
	return ""
}

func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func anyNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

// videoMetadataKeys 允许透传进 metadata 的参数。其余（count / mode / edit 等前端业务字段）不发：
// 部分渠道会把 metadata 合并进上游请求体，多余字段可能被上游当未知参数拒绝。
var videoMetadataKeys = []string{"negative_prompt", "camera_fixed", "watermark"}

// CreateVideosAPITask 调用 POST /v1/videos，返回任务 job_id。
func (p *EasyAPIProvider) CreateVideosAPITask(modelName, prompt string, inputRefs []InputReference, params taskParams) (string, error) {
	body := map[string]any{
		"model":  modelName,
		"prompt": prompt,
	}
	if len(inputRefs) > 0 {
		refs := make([]map[string]any, 0, len(inputRefs))
		for _, r := range inputRefs {
			refs = append(refs, map[string]any{"type": r.Type, "url": r.Url, "role": r.Role})
		}
		body["input_references"] = refs
	}
	if d := params.num("duration"); d > 0 {
		body["duration"] = d
	}
	if size := params.str("size"); size != "" {
		body["size"] = size
	}
	if res := params.str("resolution"); res != "" {
		body["resolution"] = strings.ToLower(res)
	}
	ratio := params.str("aspect_ratio")
	if ratio == "" {
		ratio = params.str("ratio") // 旧参数名向后兼容
	}
	if ratio != "" {
		body["aspect_ratio"] = ratio
	}
	if v, ok := params.boolVal("generate_audio"); ok {
		body["generate_audio"] = v
	} else if v, ok := params.boolVal("audio"); ok {
		body["generate_audio"] = v // 画布 / 向导的「生成音频」开关传的是 audio
	}
	if v, ok := params.intVal("seed"); ok {
		body["seed"] = v
	}
	meta := map[string]any{}
	for _, k := range videoMetadataKeys {
		if v, ok := params[k]; ok {
			meta[k] = v
		}
	}
	if len(meta) > 0 {
		body["metadata"] = meta
	}
	raw, err := p.callRaw(http.MethodPost, "/v1/videos", body)
	if err != nil && body["generate_audio"] != nil && strings.Contains(err.Error(), "generate_audio") {
		// 网关对多数模型（实测 sora-2 / hailuo-02）不收 generate_audio，直接 400；
		// 这些模型本来就原生出声或不出声，去掉开关重提一次比让用户看到 400 好。
		common.SysLog(fmt.Sprintf("gateway rejected generate_audio for %s, retrying without it", modelName))
		delete(body, "generate_audio")
		raw, err = p.callRaw(http.MethodPost, "/v1/videos", body)
	}
	if err != nil {
		return "", err
	}
	data, err := parseVideosAPIResponse(raw)
	if err != nil {
		return "", fmt.Errorf("网关响应解析失败：%s", truncateForLog(string(raw)))
	}
	// 先看失败再看 ID：失败响应常常没有 ID，别把失败原因吞成「未返回任务 ID」
	if normalizeVideoStatus(data.Status) == videoStatusFailed {
		common.SysLog(fmt.Sprintf("gateway video create failed (%s): %s", modelName, truncateForLog(string(raw))))
		return "", errors.New(videoFailMessage(data.Message, data.Status))
	}
	if data.JobId == "" {
		return "", errors.New("网关未返回任务 ID")
	}
	return data.JobId, nil
}

// videoFailMessage 网关异步失败原因：能翻成人话的翻（与同步报错同一套规则），否则保留原文。
func videoFailMessage(msg, rawStatus string) string {
	if friendly := friendlyGatewayError(0, msg); friendly != "" {
		common.SysLog("gateway video failed: " + truncateForLog(msg))
		return friendly
	}
	if strings.TrimSpace(msg) == "" {
		// 上游只回了一个 failed 状态、没给任何原因（实测 Kling 系列带参考视频时就是这样）：
		// 原始响应已在查询处记日志，给用户一句能照着做的话，别把 "failed" 这个词丢给他
		return "上游模型生成失败且没有给出原因，积分已退回；可以换个模型，或去掉参考视频 / 缩短时长后重试"
	}
	return "网关生成失败：" + msg
}

// QueryVideosAPITask 调用 GET /v1/videos/{job_id}，归一化为 videoTask。
func (p *EasyAPIProvider) QueryVideosAPITask(remoteId string) (*videoTask, error) {
	raw, err := p.callRaw(http.MethodGet, "/v1/videos/"+remoteId, nil)
	if err != nil {
		return nil, err
	}
	data, err := parseVideosAPIResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("网关响应解析失败：%s", truncateForLog(string(raw)))
	}
	// 失败时把原始响应记下来：上游常常只回 failed 不带原因，没有原文就无从排查
	if normalizeVideoStatus(data.Status) == videoStatusFailed {
		common.SysLog(fmt.Sprintf("gateway video %s failed: %s", remoteId, truncateForLog(string(raw))))
	}
	return &videoTask{
		TaskId:    firstNonEmpty(data.JobId, remoteId),
		RawStatus: data.Status,
		Status:    normalizeVideoStatus(data.Status),
		Url:       data.DownloadUrl,
		Error:     data.Message,
		Progress:  data.Progress,
	}, nil
}

// openVideoContent 成功但响应里没有地址时（OpenAI / new-api 视频对象就是这样），
// 带鉴权取 GET /v1/videos/{id}/content。
func (p *EasyAPIProvider) openVideoContent(remoteId string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, p.baseUrl+"/v1/videos/"+remoteId+"/content", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	res, err := resultHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	return checkResultResponse(res)
}

// runVideo 提交视频任务：素材经 input_references 传给网关，成功后轮询下载成片。
func (p *EasyAPIProvider) runVideo(task *model.GenerationTask) {
	refs, err := ExtractInputReferences(task)
	if err != nil {
		_ = service.FailTask(task.Id, err.Error())
		return
	}
	remoteId, err := p.CreateVideosAPITask(task.Model, task.Prompt, refs, parseParams(task.Params))
	if err != nil {
		_ = service.FailTask(task.Id, err.Error())
		return
	}
	_ = model.SetTaskProviderRef(task.Id, remoteId)
	_ = model.SetTaskProgress(task.Id, 10)
	p.pollVideo(task.Id, remoteId, time.Now().Add(easyAPIPollTimeout))
}

// ResumeVideoTasks 进程重启后恢复仍在网关执行的视频任务轮询（provider_task_id 非空的 running 任务）。
// 超过 easyAPIPollTimeout 的由后台超时任务标失败退积分，这里不再接手。
func (p *EasyAPIProvider) ResumeVideoTasks() {
	tasks, err := model.GetRunningTasksWithProviderRef(model.TaskTypeVideo)
	if err != nil || len(tasks) == 0 {
		return
	}
	for _, t := range tasks {
		started := time.Unix(t.StartedAt, 0)
		if t.StartedAt <= 0 {
			started = time.Now()
		}
		deadline := started.Add(easyAPIPollTimeout)
		if !deadline.After(time.Now()) {
			continue
		}
		common.SysLog(fmt.Sprintf("easyapi: resume polling video task #%d (%s)", t.Id, t.ProviderTaskId))
		go p.pollVideo(t.Id, t.ProviderTaskId, deadline)
	}
}

// pollVideo 轮询 /v1/videos 任务直到成功 / 失败 / 超时；任务被用户取消则停止。
func (p *EasyAPIProvider) pollVideo(taskId int, remoteId string, deadline time.Time) {
	unknownLogged := false
	notFound := 0
	for time.Now().Before(deadline) {
		time.Sleep(easyAPIPollInterval)
		if t, err := model.GetTaskById(taskId); err != nil || t.Status != model.TaskStatusRunning {
			return
		}
		state, err := p.QueryVideosAPITask(remoteId)
		if err != nil {
			common.SysError("easyapi video poll failed: " + err.Error())
			// 网关连续明确回 404：任务 ID 不存在（发布前旧端点提交的任务等），别白等到 30 分钟超时
			if isGatewayNotFound(err) {
				notFound++
				if notFound >= videoNotFoundLimit {
					_ = service.FailTask(taskId, "网关查不到这个视频任务，积分已全额退回，请重新生成")
					return
				}
			} else {
				notFound = 0
			}
			continue
		}
		notFound = 0
		switch state.Status {
		case videoStatusQueued:
			_ = model.SetTaskProgress(taskId, 20)
		case videoStatusRunning:
			_ = model.SetTaskProgress(taskId, videoRunningProgress(state.Progress))
		case videoStatusSucceeded:
			task, err := model.GetTaskById(taskId)
			if err != nil {
				return
			}
			if task.Status != model.TaskStatusRunning {
				return
			}
			var body io.ReadCloser
			var getErr error
			if state.Url != "" {
				body, getErr = openResult(state.Url)
			} else {
				body, getErr = p.openVideoContent(remoteId)
			}
			if getErr != nil {
				_ = service.FailTask(taskId, "下载视频失败："+getErr.Error())
				return
			}
			p.finishWithAsset(task, body, ".mp4", "video/mp4", model.AssetTypeVideo)
			body.Close()
			return
		case videoStatusFailed:
			_ = service.FailTask(taskId, videoFailMessage(state.Error, state.RawStatus))
			return
		default:
			if !unknownLogged {
				common.SysError(fmt.Sprintf("easyapi video poll: unknown status %q for task #%d, keep waiting", state.RawStatus, taskId))
				unknownLogged = true
			}
		}
	}
	_ = service.FailTask(taskId, "视频生成超时")
}

// ===== 语音 =====

// Speech 调用 /v1/audio/speech，返回音频二进制。
func (p *EasyAPIProvider) Speech(modelName, input, voice string) ([]byte, error) {
	if voice == "" {
		voice = "alloy"
	}
	body := map[string]any{
		"model":           modelName,
		"input":           input,
		"voice":           voice,
		"response_format": "mp3",
	}
	raw, err := p.callRaw(http.MethodPost, "/v1/audio/speech", body)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errors.New("网关未返回音频")
	}
	return raw, nil
}

// transcribeHTTP 转写请求单独的客户端：20 分钟音频上传 + 转写可能超过普通调用的 180 秒。
var transcribeHTTP = &http.Client{Timeout: 10 * time.Minute}

// Transcribe 调用 /v1/audio/transcriptions（API.md 35）：multipart 上传音频，要 verbose_json 拿逐段时间戳；
// 网关不认 timestamp_granularities / response_format 时退回 json（只有整段文本，Segments 为空，由调用方均分时间）。
// language 为空或 auto 时不传，由模型自动识别；diarize 只对名字里带 diarize 的模型有意义，这里只透传。
func (p *EasyAPIProvider) Transcribe(modelName string, file io.Reader, filename, language string, diarize bool) (service.TranscribeResult, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return service.TranscribeResult{}, err
	}
	if len(data) == 0 {
		return service.TranscribeResult{}, errors.New("音频为空")
	}
	build := func(verbose bool) (*bytes.Buffer, string, error) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		fields := map[string]string{"model": modelName}
		if verbose {
			fields["response_format"] = "verbose_json"
		} else {
			fields["response_format"] = "json"
		}
		lang := strings.ToLower(strings.TrimSpace(language))
		if lang != "" && lang != "auto" {
			fields["language"] = lang
		}
		for k, v := range fields {
			if err := w.WriteField(k, v); err != nil {
				return nil, "", err
			}
		}
		if verbose {
			if err := w.WriteField("timestamp_granularities[]", "segment"); err != nil {
				return nil, "", err
			}
		}
		if diarize {
			if err := w.WriteField("diarize", "true"); err != nil {
				return nil, "", err
			}
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
		h.Set("Content-Type", transcribeMime(filename))
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(data); err != nil {
			return nil, "", err
		}
		if err := w.Close(); err != nil {
			return nil, "", err
		}
		return &buf, w.FormDataContentType(), nil
	}
	body, ct, err := build(true)
	if err != nil {
		return service.TranscribeResult{}, err
	}
	raw, err := p.doRawWith(transcribeHTTP, http.MethodPost, "/v1/audio/transcriptions", body, ct)
	degraded := false
	if err != nil {
		var ge *gatewayStatusError
		low := strings.ToLower(err.Error())
		if errors.As(err, &ge) && ge.Status == 400 && (strings.Contains(low, "timestamp_granularities") || strings.Contains(low, "response_format")) {
			degraded = true
			body, ct, err = build(false)
			if err != nil {
				return service.TranscribeResult{}, err
			}
			raw, err = p.doRawWith(transcribeHTTP, http.MethodPost, "/v1/audio/transcriptions", body, ct)
		}
		if err != nil {
			return service.TranscribeResult{}, err
		}
	}
	var resp struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Segments []struct {
			Start   float64 `json:"start"`
			End     float64 `json:"end"`
			Text    string  `json:"text"`
			Speaker string  `json:"speaker"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return service.TranscribeResult{}, fmt.Errorf("网关响应解析失败：%s", truncateForLog(string(raw)))
	}
	out := service.TranscribeResult{Text: strings.TrimSpace(resp.Text), Language: resp.Language, Duration: resp.Duration, Degraded: degraded}
	for _, s := range resp.Segments {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		out.Segments = append(out.Segments, service.TranscribeSegment{Start: s.Start, End: s.End, Text: strings.TrimSpace(s.Text), Speaker: s.Speaker})
	}
	if out.Text == "" && len(out.Segments) == 0 {
		return out, errors.New("网关没有返回转写文本")
	}
	return out, nil
}

func transcribeMime(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	}
	return "application/octet-stream"
}

func (p *EasyAPIProvider) runAudio(task *model.GenerationTask) {
	params := parseParams(task.Params)
	// 分段配音（API.md 25.1）：params.segments 每段各自的音色与文本，逐段 TTS 后拼成一条，并记下各段起止
	if segs := parseVoiceSegments(params); len(segs) > 1 || (len(segs) == 1 && segs[0].Voice != "") {
		if service.FFmpegAvailable() {
			p.runAudioSegments(task, params, segs)
			return
		}
		// 没有 ffmpeg 拼不了：整段合成，音色取第一段的
		texts := make([]string, 0, len(segs))
		for _, sg := range segs {
			texts = append(texts, sg.Text)
		}
		data, err := p.Speech(task.Model, strings.Join(texts, " "), firstNonEmpty(segs[0].Voice, params.str("voice")))
		if err != nil {
			_ = service.FailTask(task.Id, err.Error())
			return
		}
		p.finishWithAsset(task, bytes.NewReader(data), ".mp3", "audio/mpeg", model.AssetTypeAudio)
		return
	}
	data, err := p.Speech(task.Model, task.Prompt, params.str("voice"))
	if err != nil {
		_ = service.FailTask(task.Id, err.Error())
		return
	}
	p.finishWithAsset(task, bytes.NewReader(data), ".mp3", "audio/mpeg", model.AssetTypeAudio)
}

// voiceSegment params.segments 的一项。
type voiceSegment struct {
	Speaker string
	Voice   string
	Text    string
	Pause   float64
}

// parseVoiceSegments 读 params.segments：[{speaker, voice, text, pause}]，空文本跳过。
func parseVoiceSegments(params taskParams) []voiceSegment {
	raw, ok := params["segments"].([]any)
	if !ok {
		return nil
	}
	out := make([]voiceSegment, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["text"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		sg := voiceSegment{Text: text}
		sg.Speaker, _ = m["speaker"].(string)
		sg.Voice, _ = m["voice"].(string)
		sg.Voice = strings.TrimSpace(sg.Voice)
		if v, ok := m["pause"].(float64); ok && v >= 0 && v <= 3 {
			sg.Pause = v
		}
		out = append(out, sg)
	}
	return out
}

const voiceSegmentPause = 0.35

// runAudioSegments 逐段 TTS → ffmpeg 拼接（段间静音）→ 入库，并把各段起止写进 result_text（成片按条出字幕）。
func (p *EasyAPIProvider) runAudioSegments(task *model.GenerationTask, params taskParams, segs []voiceSegment) {
	dir, err := os.MkdirTemp("", "easytv-voice-")
	if err != nil {
		_ = service.FailTask(task.Id, "创建临时目录失败")
		return
	}
	defer os.RemoveAll(dir)
	defaultVoice := params.str("voice")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	files := make([]string, 0, len(segs))
	cues := make([]model.VoiceCue, 0, len(segs))
	at := 0.0
	for i, sg := range segs {
		data, err := p.Speech(task.Model, sg.Text, firstNonEmpty(sg.Voice, defaultVoice))
		if err != nil {
			_ = service.FailTask(task.Id, fmt.Sprintf("第 %d 段配音失败：%s", i+1, err.Error()))
			return
		}
		path := filepath.Join(dir, fmt.Sprintf("seg-%d.mp3", i))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			_ = service.FailTask(task.Id, "写入配音片段失败")
			return
		}
		dur, _ := service.ProbeMedia(ctx, path)
		if dur <= 0 {
			_ = service.FailTask(task.Id, fmt.Sprintf("第 %d 段配音读不到时长", i+1))
			return
		}
		files = append(files, path)
		cues = append(cues, model.VoiceCue{Speaker: sg.Speaker, Text: sg.Text, Start: round2(at), Dur: round2(dur)})
		pause := voiceSegmentPause
		if sg.Pause > 0 {
			pause = sg.Pause
		}
		at += dur + pause
	}
	out := filepath.Join(dir, "voice.mp3")
	if err := concatAudioSegments(ctx, files, segs, out); err != nil {
		_ = service.FailTask(task.Id, "拼接配音失败："+err.Error())
		return
	}
	f, err := os.Open(out)
	if err != nil {
		_ = service.FailTask(task.Id, "读取配音失败")
		return
	}
	defer f.Close()
	p.finishWithAsset(task, f, ".mp3", "audio/mpeg", model.AssetTypeAudio)
	if cur, err := model.GetTaskById(task.Id); err == nil && cur.Status == model.TaskStatusSucceeded {
		b, _ := json.Marshal(model.VoiceCuesResult{Cues: cues})
		_ = model.SetTaskResultText(task.Id, string(b))
	}
}

// concatAudioSegments 段尾各补一段静音再顺序拼接（最后一段不补），统一 48k 立体声 mp3。
func concatAudioSegments(ctx context.Context, files []string, segs []voiceSegment, out string) error {
	args := []string{"-y"}
	for _, f := range files {
		args = append(args, "-i", f)
	}
	var fc strings.Builder
	labels := make([]string, 0, len(files))
	for i := range files {
		pause := voiceSegmentPause
		if segs[i].Pause > 0 {
			pause = segs[i].Pause
		}
		if i == len(files)-1 {
			pause = 0
		}
		fmt.Fprintf(&fc, "[%d:a]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,apad=pad_dur=%.2f[a%d];", i, pause, i)
		labels = append(labels, fmt.Sprintf("[a%d]", i))
	}
	fmt.Fprintf(&fc, "%sconcat=n=%d:v=0:a=1[out]", strings.Join(labels, ""), len(files))
	args = append(args, "-filter_complex", fc.String(), "-map", "[out]", "-c:a", "libmp3lame", "-b:a", "128k", out)
	if _, err := service.RunFFmpeg(ctx, args...); err != nil {
		return err
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return errors.New("没有生成输出文件")
	}
	return nil
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }

// ===== 结果落盘 =====

// finishWithAsset 把生成结果保存为资产并把任务置为成功。
// 任务在生成期间被取消 / 判超时（积分已退）时不入库：存之前先看一眼状态省掉上传，
// 真正的判定在 CompleteTaskWithAsset 的事务里，状态不对就回滚并删掉文件。
func (p *EasyAPIProvider) finishWithAsset(task *model.GenerationTask, r io.Reader, ext, mime, assetType string) {
	if cur, err := model.GetTaskById(task.Id); err == nil && cur.Status != model.TaskStatusRunning {
		common.SysLog(fmt.Sprintf("easyapi: task %s is %s, drop generated result", task.TaskNo, cur.Status))
		return
	}
	key := service.NewStorageKey(task.TaskNo + ext)
	counter := &limitedCounter{r: r, limit: easyAPIDownloadLimit}
	url, err := service.DefaultStorage.Save(key, counter)
	if err != nil {
		_ = service.DefaultStorage.Remove(key)
		_ = service.FailTask(task.Id, "保存生成结果失败："+err.Error())
		return
	}
	if counter.n == 0 {
		_ = service.DefaultStorage.Remove(key)
		_ = service.FailTask(task.Id, "网关返回的产物为空")
		return
	}
	asset := &model.Asset{
		UserId:     task.UserId,
		TeamId:     task.TeamId, // 团队空间任务的产物进团队资产库（API.md 20）
		ProjectId:  task.ProjectId,
		Type:       assetType,
		Name:       task.TaskNo + ext,
		Url:        url,
		StorageKey: key,
		Mime:       mime,
		Size:       counter.n,
		Source:     model.AssetSourceGenerated,
		TaskId:     task.Id,
	}
	if err := service.CompleteTaskWithAsset(task.Id, asset); err != nil {
		_ = service.DefaultStorage.Remove(key)
		if errors.Is(err, model.ErrTaskStatus) {
			common.SysLog(fmt.Sprintf("easyapi: task %s left running before result saved, drop asset", task.TaskNo))
			return
		}
		_ = service.FailTask(task.Id, "写入资产失败："+err.Error())
		return
	}
	go func() {
		service.EnsureAssetMediaInfo(asset)
		if assetType == model.AssetTypeVideo {
			service.EnsureAssetCover(asset)
		}
	}()
}

// openResult 取网关产物。非 2xx（产物地址过期回的 403/404 错误页）和 HTML/JSON 错误页都算失败，
// 不能当成 png/mp4 入库并结算扣分。
func openResult(url string) (io.ReadCloser, error) {
	res, err := resultHTTP.Get(url)
	if err != nil {
		return nil, err
	}
	return checkResultResponse(res)
}

func checkResultResponse(res *http.Response) (io.ReadCloser, error) {
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		return nil, fmt.Errorf("产物地址返回 HTTP %d", res.StatusCode)
	}
	ct := strings.ToLower(res.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/json") {
		res.Body.Close()
		return nil, fmt.Errorf("产物地址返回的不是媒体文件（%s）", ct)
	}
	return res.Body, nil
}

// limitedCounter 统计写入字节数（资产 Size，存储配额按它算），超过上限直接报错而不是静默截断。
type limitedCounter struct {
	r     io.Reader
	n     int64
	limit int64
}

func (c *limitedCounter) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += int64(n)
	if c.n > c.limit {
		return n, errResultTooLarge
	}
	return n, err
}

// ===== HTTP =====

func (p *EasyAPIProvider) call(method, path string, body any, out any) error {
	raw, err := p.callRaw(method, path, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("网关响应解析失败：%s", strings.TrimSpace(string(raw)))
	}
	return nil
}

func (p *EasyAPIProvider) callRaw(method, path string, body any) ([]byte, error) {
	var reader io.Reader
	contentType := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
		contentType = "application/json"
	}
	return p.doRaw(method, path, reader, contentType)
}

// doRaw 发请求并统一处理网关错误（JSON 与 multipart 共用）。
func (p *EasyAPIProvider) doRaw(method, path string, reader io.Reader, contentType string) ([]byte, error) {
	return p.doRawWith(p.client, method, path, reader, contentType)
}

// 网关限流 / 不可用重试：429 与 503 在几秒内重试两次（认 Retry-After，封顶 10 秒）。
// 实测 gpt-4o-mini 常回 429「排队较多」，定风格 / 提示词优化 / agent 一次就失败很伤体验。
// 只挑这两个「网关没受理」的状态码：502 / 504 可能是上游已建了任务才挂的，重发会重复建。变量留给测试调小。
var (
	gatewayRetryMax      = 2
	gatewayRetryBase     = 1500 * time.Millisecond
	gatewayRetryAfterCap = 10 * time.Second
)

func gatewayShouldRetry(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

// gatewayRetryDelay 第 attempt 次重试（从 1 起）等多久：Retry-After 优先，否则按次数线性拉长。
func gatewayRetryDelay(retryAfter string, attempt int) time.Duration {
	if sec, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && sec > 0 {
		d := time.Duration(sec) * time.Second
		if d > gatewayRetryAfterCap {
			d = gatewayRetryAfterCap
		}
		return d
	}
	return gatewayRetryBase * time.Duration(attempt)
}

// doRawWith 用指定客户端发请求（转写等长调用换更长超时的客户端）。
// 请求体先整个读进内存（调用方给的本来就是内存缓冲），重试时重建 reader。
func (p *EasyAPIProvider) doRawWith(client *http.Client, method, path string, reader io.Reader, contentType string) ([]byte, error) {
	var payload []byte
	if reader != nil {
		var err error
		if payload, err = io.ReadAll(reader); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequest(method, p.baseUrl+path, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("请求网关失败：%w", err)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, easyAPIDownloadLimit))
		res.Body.Close()
		if err != nil {
			return nil, err
		}
		if res.StatusCode < 400 {
			return data, nil
		}
		if gatewayShouldRetry(res.StatusCode) && attempt < gatewayRetryMax {
			delay := gatewayRetryDelay(res.Header.Get("Retry-After"), attempt+1)
			common.SysLog(fmt.Sprintf("gateway %d %s: retry %d/%d after %s", res.StatusCode, path, attempt+1, gatewayRetryMax, delay))
			time.Sleep(delay)
			continue
		}
		// 已知错误翻成人话给创作者，原文写日志（见 gateway_error.go）
		return nil, gatewayCallError(res.StatusCode, path, gatewayErrorMessage(data))
	}
}

// gatewayErrorMessage 从 OpenAI 风格错误体提取 message（兼容 /v1/videos 的 {code,msg} 包裹错误）。
func gatewayErrorMessage(data []byte) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(data, &body); err == nil {
		if body.Error.Message != "" {
			return body.Error.Message
		}
		if body.Message != "" {
			return body.Message
		}
		if body.Msg != "" {
			return body.Msg
		}
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// videoRunningProgress 生成中进度：网关给了就用（压在 30-95，给前端留出下载入库的余量），没给停在 30。
func videoRunningProgress(p int) int {
	if p < 0 {
		return 30
	}
	return min(max(p, 30), 95)
}
