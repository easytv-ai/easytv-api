package service

import (
	"encoding/json"
	"strings"
	"testing"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestJSONReferencesAsset(t *testing.T) {
	var data map[string]any
	_ = json.Unmarshal([]byte(`{"kind":"video","refAssetId":12,"refItems":[{"assetId":34,"url":"u"}],"scene":{"assets":[{"modelAssetId":56}]},"taskId":78}`), &data)
	for _, id := range []int{12, 34, 56} {
		if !jsonReferencesAsset(data, id) {
			t.Fatalf("应识别引用 %d", id)
		}
	}
	if jsonReferencesAsset(data, 78) {
		t.Fatal("taskId 不是资产引用")
	}
	if got := AssetRefsSummary([]AssetRef{{Type: "canvas"}, {Type: "canvas"}, {Type: "subject"}}); got != "该素材正在被 2 张画布、1 个主体 使用" {
		t.Fatalf("summary = %q", got)
	}
}

type previewProvider struct{ models []ModelInfo }

func (p previewProvider) Name() string                            { return "preview" }
func (p previewProvider) Models() []ModelInfo                     { return p.models }
func (p previewProvider) Submit(task *model.GenerationTask) error { return nil }

// TestBuildClonePreview 模型调价 / 下线提示、按节点估算、只剩地址的参考素材与主体引用都要列出来。
func TestBuildClonePreview(t *testing.T) {
	mock := mockDB(t)
	mock.MatchExpectationsInOrder(false)
	RegisterProvider(previewProvider{models: []ModelInfo{{Provider: "preview", Model: "gpt-image-2", Type: "image"}}})
	priceRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "status"}).
			AddRow(1, "gpt-image-2", "image", "GPT Image 2", "image", 3, 3, 1)
	}
	// 自由画布节点没有 data.model，只有 taskId：按作者的那次任务取模型
	mock.ExpectQuery("SELECT .* FROM `generation_task`").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "node_id", "type", "model", "params"}).
		AddRow(60, 8, "image-2", "image", "gpt-image-2", `{"count":4,"ratio":"16:9"}`))
	for i := 0; i < 3; i++ {
		mock.ExpectQuery("SELECT .* FROM `model_price`").WithArgs("gpt-image-2", 1).WillReturnRows(priceRows())
	}
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT .* FROM `model_price`").WithArgs("old-video", 1).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	}
	src := &model.Canvas{Graph: `{"nodes":[
		{"id":"image-1","data":{"kind":"image","model":"gpt-image-2","label":"分镜 1","refUrl":"https://cdn/r.png"}},
		{"id":"video-1","data":{"kind":"video","model":"old-video","seconds":6,"subjectIds":[3]}},
		{"id":"image-2","data":{"kind":"image","taskId":60}},
		{"id":"text-1","data":{"kind":"note"}}]}`, UserId: 8}
	snap, _ := json.Marshal(TemplateMeta{CapturedAt: 1, Models: []TemplateModel{{Model: "gpt-image-2", Type: "image", PointsPerUnit: 2, Unit: "image"}}})
	w := &model.Work{Id: 9, TemplateMeta: string(snap)}
	p := BuildClonePreview(w, src, 0)

	if !p.HasSnapshot || p.NodeCount != 4 || p.GenerativeNodes != 3 || p.EstimatePoints != 6 || p.Unpriced != 1 || p.SubjectNodes != 1 {
		t.Fatalf("preview = %+v", p)
	}
	byModel := map[string]ClonePreviewModel{}
	for _, m := range p.Models {
		byModel[m.Model] = m
	}
	if img := byModel["gpt-image-2"]; !img.Available || img.Nodes != 2 || !strings.Contains(strings.Join(img.Changes, ","), "2 → 3") {
		t.Fatalf("gpt-image-2 = %+v", img)
	}
	if vid := byModel["old-video"]; vid.Available || !strings.Contains(strings.Join(vid.Changes, ","), "已下线") {
		t.Fatalf("old-video = %+v", vid)
	}
	if len(p.MissingRefs) != 2 {
		t.Fatalf("missing = %+v", p.MissingRefs)
	}
}
