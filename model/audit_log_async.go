package model

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"easytv-api/common"
)

// 审计日志异步写入：有界缓冲队列 + 单个后台写协程。
// 业务请求只做入队（非阻塞），写协程每次最多攒 auditWriteBatchSize 条批量 INSERT；
// 队列满时丢弃新事件并计数（首条及每 100 条记一次 SysError），宁可丢审计也不拖慢主链路。
// 进程被强杀时队列中尚未落库的事件会丢失。

const (
	auditQueueSize      = 1000
	auditWriteBatchSize = 100
)

// auditJob 队列元素：entry 为待写日志；flush 非 nil 表示刷新请求，写完它之前的事件后关闭。
type auditJob struct {
	entry *AuditLog
	flush chan struct{}
}

type auditBuffer struct {
	queue     chan auditJob
	write     func([]*AuditLog) error
	dropped   atomic.Int64
	startOnce sync.Once
}

func newAuditBuffer(size int, write func([]*AuditLog) error) *auditBuffer {
	return &auditBuffer{queue: make(chan auditJob, size), write: write}
}

// start 启动写协程（幂等）。
func (b *auditBuffer) start() {
	b.startOnce.Do(func() { go b.run() })
}

// enqueue 非阻塞入队；队列满返回 false 并计入丢弃数。
func (b *auditBuffer) enqueue(entry *AuditLog) bool {
	select {
	case b.queue <- auditJob{entry: entry}:
		return true
	default:
		n := b.dropped.Add(1)
		if n == 1 || n%100 == 0 {
			common.SysError(fmt.Sprintf("audit log queue full, dropped %d events (action=%s, request_id=%s)", n, entry.Action, entry.RequestId))
		}
		return false
	}
}

func (b *auditBuffer) run() {
	for job := range b.queue {
		batch := make([]*AuditLog, 0, auditWriteBatchSize)
		var flushes []chan struct{}
		collect := func(j auditJob) {
			if j.entry != nil {
				batch = append(batch, j.entry)
			}
			if j.flush != nil {
				flushes = append(flushes, j.flush)
			}
		}
		collect(job)
	drain:
		for len(batch) < auditWriteBatchSize {
			select {
			case j := <-b.queue:
				collect(j)
			default:
				break drain
			}
		}
		b.writeBatch(batch)
		for _, f := range flushes {
			close(f)
		}
	}
}

// writeBatch 批量写入；整批失败时逐条重试，避免一条坏数据拖累整批。写入 panic 不能带走写协程。
func (b *auditBuffer) writeBatch(batch []*AuditLog) {
	if len(batch) == 0 || b.write == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("audit log writer panic: %v", r))
		}
	}()
	if err := b.write(batch); err == nil || len(batch) == 1 {
		if err != nil {
			common.SysError(fmt.Sprintf("audit log write failed (request_id=%s): %v", batch[0].RequestId, err))
		}
		return
	}
	for _, entry := range batch {
		if err := b.write([]*AuditLog{entry}); err != nil {
			common.SysError(fmt.Sprintf("audit log write failed (request_id=%s): %v", entry.RequestId, err))
		}
	}
}

// flush 等待刷新请求之前入队的事件全部写完；超时（或写协程未启动）返回 false。
func (b *auditBuffer) flush(timeout time.Duration) bool {
	done := make(chan struct{})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case b.queue <- auditJob{flush: done}:
	case <-timer.C:
		return false
	}
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// writeAuditLogs 默认写入：批量 INSERT。DB 未初始化（单测）时直接跳过。
func writeAuditLogs(entries []*AuditLog) error {
	if DB == nil {
		return nil
	}
	return DB.Create(&entries).Error
}

// auditLogBuffer 全局缓冲；用 atomic.Pointer 以便测试替换写入函数。
var auditLogBuffer atomic.Pointer[auditBuffer]

func init() {
	auditLogBuffer.Store(newAuditBuffer(auditQueueSize, writeAuditLogs))
}

// enqueueAuditLog 首次记录时懒启动写协程。
func enqueueAuditLog(entry *AuditLog) {
	b := auditLogBuffer.Load()
	b.start()
	b.enqueue(entry)
}

// FlushAuditLogs 同步等待已入队的审计日志落库（测试、进程退出前调用）。
func FlushAuditLogs(timeout time.Duration) bool {
	b := auditLogBuffer.Load()
	b.start()
	return b.flush(timeout)
}

// AuditLogDroppedCount 进程启动以来因队列满丢弃的审计事件数。
func AuditLogDroppedCount() int64 {
	return auditLogBuffer.Load().dropped.Load()
}

// ReplaceAuditLogWriter 换一个新缓冲并指定写入函数，返回还原函数。仅供测试捕获审计写入，业务代码不要调用。
func ReplaceAuditLogWriter(write func([]*AuditLog) error) (restore func()) {
	old := auditLogBuffer.Swap(newAuditBuffer(auditQueueSize, write))
	return func() {
		FlushAuditLogs(time.Second)
		auditLogBuffer.Store(old)
	}
}
