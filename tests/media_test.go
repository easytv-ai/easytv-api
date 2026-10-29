package tests

import (
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestMediaActionsList 媒体工具动作清单：本地动作可用、模型动作给出不可用原因（API.md 23）。
func TestMediaActionsList(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	// 开关一次、定价一次；定价为空 → 没有任何能力模型
	mock.ExpectQuery("SELECT .* FROM `setting`").WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery("SELECT .* FROM `model_price`").WillReturnRows(sqlmock.NewRows([]string{"model"}))

	code, resp := do(t, r, "GET", "/user/media/actions", "", authHeaders())
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("actions: code=%d resp=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	list, _ := data["actions"].([]any)
	if len(list) == 0 {
		t.Fatalf("动作清单为空：%v", resp)
	}
	byAction := map[string]map[string]any{}
	for _, it := range list {
		m, _ := it.(map[string]any)
		byAction[m["action"].(string)] = m
	}
	// 工具条上的入口都要在清单里
	for _, want := range []string{"crop", "trim", "split_av", "extract_frames", "storyboard", "upscale", "remove_subtitle", "retake"} {
		if byAction[want] == nil {
			t.Errorf("清单缺少动作 %s", want)
		}
	}
	// 本地动作免费；模型动作没有模型时必须带原因
	if crop := byAction["crop"]; crop != nil {
		if crop["engine"] != "local" || crop["free"] != true {
			t.Errorf("crop 应是本地免费动作：%v", crop)
		}
	}
	for _, name := range []string{"upscale", "remove_subtitle", "vocal_separation", "matting"} {
		a := byAction[name]
		if a == nil {
			continue
		}
		if a["engine"] != "model" || a["available"] != false {
			t.Errorf("%s 没有能力模型时不应可用：%v", name, a)
		}
		if reason, _ := a["reason"].(string); reason == "" {
			t.Errorf("%s 应给出不可用原因：%v", name, a)
		}
	}
	// 既有动作指路到已实现的接口
	if retake := byAction["retake"]; retake != nil {
		if retake["engine"] != "existing" || retake["endpoint"] != "POST /user/task/:id/retake" {
			t.Errorf("retake 应指向既有接口：%v", retake)
		}
	}
}

func TestAdminMediaActionsList(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthAdmin(mock)
	mock.ExpectQuery("SELECT .* FROM `setting`").WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery("SELECT .* FROM `model_price`").WillReturnRows(sqlmock.NewRows([]string{"model"}))

	code, resp := do(t, r, "GET", "/admin/media/actions", "", map[string]string{
		"Authorization": "Bearer admintoken", "EasyTV-User": "2",
	})
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("admin actions: code=%d resp=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if _, ok := data["ffmpeg"].(bool); !ok {
		t.Fatalf("admin 盘点应带 ffmpeg：%v", data)
	}
	list, _ := data["actions"].([]any)
	if len(list) == 0 {
		t.Fatalf("admin 动作清单为空：%v", resp)
	}
	found := false
	for _, it := range list {
		m, _ := it.(map[string]any)
		if m["action"] == "remove_subtitle" {
			found = true
			if m["available"] != false || m["gap"] != "no_model" {
				t.Errorf("没模型时 remove_subtitle 应是 no_model：%v", m)
			}
		}
	}
	if !found {
		t.Fatal("admin 清单缺少 remove_subtitle")
	}
}

// TestCreateMediaJobRejectsUnknownAction 未知动作直接拒绝，不落库。
func TestCreateMediaJobRejectsUnknownAction(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	code, resp := do(t, r, "POST", "/user/media/jobs", `{"action":"不存在","asset_id":1}`, authHeaders())
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("未知动作应被拒绝：code=%d resp=%v", code, resp)
	}
}

// TestCreateMediaJobRejectsForeignAsset 别人的素材不能处理（越权防护）。
func TestCreateMediaJobRejectsForeignAsset(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	// 动作开关
	mock.ExpectQuery("SELECT .* FROM `setting`").WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	// 资产查不到（不是当前用户的）→ 素材不存在
	mock.ExpectQuery("SELECT .* FROM `asset`").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	code, resp := do(t, r, "POST", "/user/media/jobs", `{"action":"crop","asset_id":999}`, authHeaders())
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("越权素材应被拒绝：code=%d resp=%v", code, resp)
	}
	if msg, _ := resp["message"].(string); msg != "素材不存在" {
		t.Errorf("错误文案 = %q", msg)
	}
}

// TestMediaJobDetailNotFound 不是自己的作业查不到。
func TestMediaJobDetailNotFound(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	expectAuthUser(mock)
	mock.ExpectQuery("SELECT .* FROM `media_job`").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	code, resp := do(t, r, "GET", "/user/media/jobs/123", "", authHeaders())
	if code != http.StatusOK || resp["success"] != false {
		t.Fatalf("越权作业应查不到：code=%d resp=%v", code, resp)
	}
}
