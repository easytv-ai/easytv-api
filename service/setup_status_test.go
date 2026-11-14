package service

import (
	"testing"

	"easytv-api/model"
)

func TestBuildSetupStatus(t *testing.T) {
	prices := []*model.ModelPrice{
		{Model: "t1", Type: model.TaskTypeText, Status: model.BillingStatusOn},
		{Model: "v1", Type: model.TaskTypeVideo, Status: model.BillingStatusOn},
		{Model: "v2", Type: model.TaskTypeVideo, Status: model.BillingStatusOn},
		{Model: "i1", Type: model.TaskTypeImage, Status: model.BillingStatusOff},
		nil,
	}

	t.Run("配好网关且有生效模型才算就绪", func(t *testing.T) {
		got := BuildSetupStatus(SetupStatusInput{
			GatewayBaseUrl: "http://localhost:3000", HasGatewayKey: true, GatewayEnabled: true,
			Prices: prices, StorageKind: StorageKindOSS, FFmpeg: true, FFprobe: true,
		})
		if !got.Ready || !got.Gateway.Configured || !got.Gateway.Enabled {
			t.Fatalf("应就绪：%+v", got)
		}
		if got.Models.PricedCount != 3 || got.Models.TotalCount != 4 {
			t.Fatalf("模型计数不对：%+v", got.Models)
		}
		want := map[string]int{"text": 1, "image": 0, "video": 2, "audio": 0}
		for k, v := range want {
			if n, ok := got.Models.ByType[k]; !ok || n != v {
				t.Fatalf("by_type[%s]=%d ok=%v，期望 %d", k, n, ok, v)
			}
		}
		if got.Storage.Kind != StorageKindOSS || !got.Storage.HasPublicBaseUrl {
			t.Fatalf("对象存储恒有公网地址：%+v", got.Storage)
		}
		if !got.Media.FFmpeg || !got.Media.FFprobe {
			t.Fatalf("媒体工具状态应透传：%+v", got.Media)
		}
	})

	t.Run("没有令牌时未配置也未启用", func(t *testing.T) {
		got := BuildSetupStatus(SetupStatusInput{GatewayBaseUrl: "http://localhost:3000", GatewayEnabled: true, Prices: prices})
		if got.Ready || got.Gateway.Configured || got.Gateway.Enabled {
			t.Fatalf("无令牌不应就绪：%+v", got)
		}
	})

	t.Run("开关关闭时不就绪", func(t *testing.T) {
		got := BuildSetupStatus(SetupStatusInput{GatewayBaseUrl: "http://localhost:3000", HasGatewayKey: true, Prices: prices})
		if got.Ready || !got.Gateway.Configured || got.Gateway.Enabled {
			t.Fatalf("开关关闭：%+v", got)
		}
	})

	t.Run("只有停用模型时不就绪", func(t *testing.T) {
		got := BuildSetupStatus(SetupStatusInput{
			GatewayBaseUrl: "http://localhost:3000", HasGatewayKey: true, GatewayEnabled: true,
			Prices: []*model.ModelPrice{{Model: "i1", Type: model.TaskTypeImage, Status: model.BillingStatusOff}},
		})
		if got.Ready || got.Models.PricedCount != 0 || got.Models.TotalCount != 1 {
			t.Fatalf("停用模型不计入：%+v", got)
		}
	})

	t.Run("本地磁盘按是否配了对外前缀区分", func(t *testing.T) {
		got := BuildSetupStatus(SetupStatusInput{StorageKind: "unknown"})
		if got.Storage.Kind != StorageKindLocal || got.Storage.HasPublicBaseUrl {
			t.Fatalf("未知类型按本地磁盘、无对外前缀：%+v", got.Storage)
		}
		got = BuildSetupStatus(SetupStatusInput{StorageKind: StorageKindLocal, UploadBaseUrl: "http://localhost:3000/upload"})
		if !got.Storage.HasPublicBaseUrl {
			t.Fatalf("配了对外前缀：%+v", got.Storage)
		}
	})
}
