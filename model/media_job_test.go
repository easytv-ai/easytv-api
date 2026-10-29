package model

import (
	"encoding/json"
	"testing"

	"easytv-api/common"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func mockMediaDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new failed: %v", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open failed: %v", err)
	}
	DB = db
	return mock
}

// TestClaimMediaJobSkipsModelJobs 本地 worker 只领本地作业：
// 模型作业的壳行永远停在「排队 = 0」，被领走只会白跑一趟并把它标成失败。
func TestClaimMediaJobSkipsModelJobs(t *testing.T) {
	mock := mockMediaDB(t)
	mock.ExpectQuery("SELECT \\* FROM `media_job` WHERE engine = \\? AND status = \\? AND worker_tag = \\? ORDER BY id asc LIMIT 1").
		WithArgs(MediaEngineLocal, MediaStatusQueued, "").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if job := ClaimMediaJob(); job != nil {
		t.Fatalf("没有排队作业时应返回 nil，实际 %+v", job)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("领取条件不对：%v", err)
	}
}

// TestQueuedMediaJobsOnlyLocal 队列额度只按本地 / 复合作业算：
// 模型作业的状态由生成任务承载，算进来的话用户跑完几次模型动作就再也提交不了本地处理了。
func TestQueuedMediaJobsOnlyLocal(t *testing.T) {
	mock := mockMediaDB(t)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `media_job` WHERE user_id = \\? AND engine IN \\(\\?,\\?\\) AND status IN \\(\\?,\\?\\)").
		WithArgs(7, MediaEngineLocal, MediaEnginePipeline, MediaStatusQueued, MediaStatusRunning).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	if n := CountQueuedMediaJobsByUser(7); n != 2 {
		t.Fatalf("排队数 = %d，want 2", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("统计条件不对：%v", err)
	}
}

// TestActiveMediaJobsOnlyLocal active=1 只筛本地 / 复合作业（模型作业壳行永远停在 0，按状态筛会把它们一直算成进行中）。
func TestActiveMediaJobsOnlyLocal(t *testing.T) {
	mock := mockMediaDB(t)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `media_job` WHERE user_id = \\? AND \\(engine IN \\(\\?,\\?\\) AND status IN \\(\\?,\\?\\)\\)").
		WithArgs(7, MediaEngineLocal, MediaEnginePipeline, MediaStatusQueued, MediaStatusRunning).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT \\* FROM `media_job` WHERE user_id = \\? AND \\(engine IN \\(\\?,\\?\\) AND status IN \\(\\?,\\?\\)\\) ORDER BY id desc LIMIT 10").
		WithArgs(7, MediaEngineLocal, MediaEnginePipeline, MediaStatusQueued, MediaStatusRunning).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	list, total, err := GetMediaJobs(MediaJobFilter{UserId: 7, OnlyActive: true}, &common.PageInfo{Page: 1, PageSize: 10})
	if err != nil || total != 0 || len(list) != 0 {
		t.Fatalf("查询失败：err=%v total=%d list=%d", err, total, len(list))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("过滤条件不对：%v", err)
	}
}

// TestListQueuedPipelineJobs 复合作业只领「排队且已挂生成任务」且同 worker 标签的行（生产标签为空串）。
func TestListQueuedPipelineJobs(t *testing.T) {
	mock := mockMediaDB(t)
	mock.ExpectQuery("SELECT \\* FROM `media_job` WHERE engine = \\? AND status = \\? AND task_id > 0 AND worker_tag = \\? ORDER BY id asc LIMIT 20").
		WithArgs(MediaEnginePipeline, MediaStatusQueued, "").
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id"}).AddRow(5, 77))
	list := ListQueuedPipelineJobs(20)
	if len(list) != 1 || list[0].TaskId != 77 {
		t.Fatalf("应返回 1 条复合作业，实际 %+v", list)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("查询条件不对：%v", err)
	}
}

// TestClaimMediaJobWorkerTag 联调实例（WORKER_TAG）只领自己标签的本地作业。
func TestClaimMediaJobWorkerTag(t *testing.T) {
	t.Setenv("WORKER_TAG", "dev-1")
	mock := mockMediaDB(t)
	mock.ExpectQuery("SELECT \\* FROM `media_job` WHERE engine = \\? AND status = \\? AND worker_tag = \\? ORDER BY id asc LIMIT 1").
		WithArgs(MediaEngineLocal, MediaStatusQueued, "dev-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if j := ClaimMediaJob(); j != nil {
		t.Fatalf("没有同标签作业时应返回 nil：%+v", j)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("查询条件不对：%v", err)
	}
}

func TestMediaOutputWarningJSON(t *testing.T) {
	out := MediaOutput{
		AssetId: 9, Type: AssetTypeText, Name: "字幕.srt", Label: "字幕",
		Warning:       "素材长 40 分钟，只转写了前 20 分钟",
		WarningCode:   MediaWarningTranscribeTruncated,
		WarningParams: map[string]any{"minutes": 20},
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var got MediaOutput
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.WarningCode != MediaWarningTranscribeTruncated {
		t.Fatalf("warning_code = %q", got.WarningCode)
	}
	if minutes, _ := got.WarningParams["minutes"].(float64); minutes != 20 {
		t.Fatalf("warning_params.minutes = %v", got.WarningParams["minutes"])
	}
	if got.Warning == "" {
		t.Fatal("中文 warning 应保留")
	}
}
