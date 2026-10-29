package service

import (
	"strings"
	"testing"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestBatchCycle(t *testing.T) {
	ok := []BatchItemRequest{
		{NodeId: "image-1"},
		{NodeId: "image-2", Depends: []model.BatchDepend{{NodeId: "image-1", Role: "character"}}},
		{NodeId: "video-1", Depends: []model.BatchDepend{{NodeId: "image-2", Role: "first_frame"}}},
	}
	if c := batchCycle(ok); c != "" {
		t.Fatalf("无环却报环: %s", c)
	}
	bad := append(ok, BatchItemRequest{NodeId: "image-1", Depends: []model.BatchDepend{{NodeId: "video-1"}}})
	bad[0].Depends = []model.BatchDepend{{NodeId: "video-1"}}
	if c := batchCycle(bad); c == "" {
		t.Fatal("成环应报错")
	}
}

func batchItemRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "batch_id", "node_id", "layer", "type", "model", "prompt", "params", "inputs", "depends", "status", "estimate", "task_id", "asset_id", "attempts", "seq"})
}

// TestAdvanceBatchDependencyOrder 依赖没就绪不提交；上游成功后下游带着上游产物（按依赖角色）提交。
func TestAdvanceBatchDependencyOrder(t *testing.T) {
	mock := mockDB(t)
	var submitted []*model.GenerationTask
	old := batchSubmitter
	batchSubmitter = func(task *model.GenerationTask) (*model.GenerationTask, error) {
		task.Id, task.PointsFrozen, task.Status = 100+len(submitted), 2, model.TaskStatusPending
		submitted = append(submitted, task)
		return task, nil
	}
	defer func() { batchSubmitter = old }()
	b := &model.GenerationBatch{Id: 7, UserId: 8, CanvasId: 29, Status: model.BatchRunning}

	// 第 1 轮：角色图可以提交，分镜图等角色图
	mock.ExpectQuery("SELECT .* FROM `generation_batch_item`").WillReturnRows(batchItemRows().
		AddRow(1, 7, "image-1", "character", "image", "gpt-image-2", "角色", "{}", `[]`, `[]`, "waiting", 2, 0, 0, 0, 0).
		AddRow(2, 7, "image-2", "shot_image", "image", "gpt-image-2", "分镜", "{}", `[{"asset_id":9,"role":"style"}]`, `[{"node_id":"image-1","role":"character"}]`, "waiting", 2, 0, 0, 0, 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch_item`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch`").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	advanceBatch(b)
	if len(submitted) != 1 || submitted[0].NodeId != "image-1" {
		t.Fatalf("第 1 轮应只提交 image-1: %+v", submitted)
	}

	// 第 2 轮：角色图任务成功（产物 555）→ 分镜图带 style 静态输入 + character=555 提交
	mock.ExpectQuery("SELECT .* FROM `generation_batch_item`").WillReturnRows(batchItemRows().
		AddRow(1, 7, "image-1", "character", "image", "gpt-image-2", "角色", "{}", `[]`, `[]`, "submitted", 2, 100, 0, 1, 0).
		AddRow(2, 7, "image-2", "shot_image", "image", "gpt-image-2", "分镜", "{}", `[{"asset_id":9,"role":"style"}]`, `[{"node_id":"image-1","role":"character"}]`, "waiting", 2, 0, 0, 0, 1))
	mock.ExpectQuery("SELECT .* FROM `generation_task`").WillReturnRows(sqlmock.NewRows([]string{"id", "status", "result_asset_id"}).AddRow(100, "succeeded", 555))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch_item`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch_item`").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch`").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	b.SpentPoints = 2
	advanceBatch(b)
	if len(submitted) != 2 || submitted[1].NodeId != "image-2" {
		t.Fatalf("第 2 轮应提交 image-2: %+v", submitted)
	}
	if !strings.Contains(submitted[1].Inputs, `{"asset_id":9,"role":"style"}`) || !strings.Contains(submitted[1].Inputs, `{"asset_id":555,"role":"character"}`) {
		t.Fatalf("分镜图输入 = %s", submitted[1].Inputs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestAdvanceBatchBudgetPause 再提交会超预算就暂停批次，不提交。
func TestAdvanceBatchBudgetPause(t *testing.T) {
	mock := mockDB(t)
	old := batchSubmitter
	batchSubmitter = func(task *model.GenerationTask) (*model.GenerationTask, error) {
		t.Fatal("超预算不该提交")
		return nil, nil
	}
	defer func() { batchSubmitter = old }()
	b := &model.GenerationBatch{Id: 7, UserId: 8, CanvasId: 29, Status: model.BatchRunning, BudgetPoints: 10, SpentPoints: 8}
	mock.ExpectQuery("SELECT .* FROM `generation_batch_item`").WillReturnRows(batchItemRows().
		AddRow(3, 7, "video-1", "shot_video", "video", "hailuo-02", "镜头", "{}", `[]`, `[]`, "waiting", 18, 0, 0, 0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch` SET .*pause_reason").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch` SET").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	advanceBatch(b)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestAdvanceBatchSkipDownstream 上游失败，下游级联标跳过。
func TestAdvanceBatchSkipDownstream(t *testing.T) {
	mock := mockDB(t)
	mock.MatchExpectationsInOrder(true)
	b := &model.GenerationBatch{Id: 7, UserId: 8, CanvasId: 29, Status: model.BatchPaused}
	mock.ExpectQuery("SELECT .* FROM `generation_batch_item`").WillReturnRows(batchItemRows().
		AddRow(1, 7, "image-2", "shot_image", "image", "m", "分镜", "{}", `[]`, `[]`, "failed", 2, 100, 0, 1, 0).
		AddRow(2, 7, "video-1", "shot_video", "video", "m", "镜头", "{}", `[]`, `[{"node_id":"image-2","role":"first_frame"}]`, "waiting", 2, 0, 0, 0, 1).
		AddRow(3, 7, "audio-1", "voice", "audio", "m", "配音", "{}", `[]`, `[{"node_id":"video-1"}]`, "waiting", 2, 0, 0, 0, 2))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch_item` SET `error`=\\?,`status`=\\?").WithArgs("上游节点 image-2 没有生成成功", "skipped", sqlmock.AnyArg(), 2, "waiting").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch_item` SET `error`=\\?,`status`=\\?").WithArgs("上游节点 video-1 没有生成成功", "skipped", sqlmock.AnyArg(), 3, "waiting").WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch` SET").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	// 全部结束 → 批次标 done（后面的埋点 / 站内信写库不在这里断言）
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_batch` SET .*finished_at").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	advanceBatch(b)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}
