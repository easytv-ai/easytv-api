package model

import "testing"

func TestMediaCancelBlocked(t *testing.T) {
	if MediaCancelBlocked(nil) {
		t.Fatal("nil")
	}
	if MediaCancelBlocked(&GenerationTask{Type: TaskTypeText, Status: TaskStatusRunning}) {
		t.Fatal("文本任务生成中仍可取消")
	}
	if MediaCancelBlocked(&GenerationTask{Type: TaskTypeVideo, Status: TaskStatusPending}) {
		t.Fatal("还在本地排队的视频可以取消")
	}
	if !MediaCancelBlocked(&GenerationTask{Type: TaskTypeVideo, Status: TaskStatusRunning}) {
		t.Fatal("生成中的视频不能取消")
	}
	if !MediaCancelBlocked(&GenerationTask{Type: TaskTypeImage, Status: TaskStatusPending, ProviderTaskId: "job_1"}) {
		t.Fatal("已经拿到网关任务号的图片不能取消")
	}
	if !MediaCancelBlocked(&GenerationTask{Type: TaskTypeAudio, Status: TaskStatusRunning}) {
		t.Fatal("生成中的音频不能取消")
	}
}
