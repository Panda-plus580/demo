package scheduler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

var benchSeq atomic.Int64

func benchScheduler(b *testing.B) *Scheduler {
	b.Helper()
	s := New(WithWorkers(8), WithQueueSize(64))
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Close(ctx)
	})
	return s
}

// BenchmarkParallelSubmitCancel 测「写路径」：多 goroutine 并发提交 + 取消，
// 每次操作都要过全局锁与最小堆，是竞争最激烈的场景。
func BenchmarkParallelSubmitCancel(b *testing.B) {
	s := benchScheduler(b)
	fn := func() (string, error) { return "", nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := fmt.Sprintf("bc-%d", benchSeq.Add(1))
			if err := s.AddTask(TaskSpec{ID: id, RunAt: time.Now().Add(time.Hour), Fn: fn}); err != nil {
				b.Errorf("add: %v", err)
				return
			}
			if err := s.Cancel(id); err != nil {
				b.Errorf("cancel: %v", err)
				return
			}
		}
	})
}

// BenchmarkParallelStatusQuery 测「读路径」：并发查询任务状态。
// 全局锁只用于查索引表，任务状态本身由 entry 自己的锁保护。
func BenchmarkParallelStatusQuery(b *testing.B) {
	s := benchScheduler(b)

	const n = 1000
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("q-%d", i)
		if err := s.AddTask(TaskSpec{ID: id, RunAt: time.Now().Add(time.Hour), Fn: func() (string, error) {
			return "", nil
		}}); err != nil {
			b.Fatalf("add: %v", err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			id := fmt.Sprintf("q-%d", i%n)
			i++
			if _, err := s.Status(id); err != nil {
				b.Errorf("status: %v", err)
				return
			}
		}
	})
}

// BenchmarkSubmitWaitRoundTrip 测端到端：提交任务并同步等待执行结果，
// 反映「单任务从提交到拿到结果」的延迟。
func BenchmarkSubmitWaitRoundTrip(b *testing.B) {
	s := benchScheduler(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("rt-%d", i)
		if err := s.AddTask(TaskSpec{ID: id, Fn: func() (string, error) { return "ok", nil }}); err != nil {
			b.Fatalf("add: %v", err)
		}

		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if _, err := s.Wait(waitCtx, id); err != nil {
			cancel()
			b.Fatalf("wait: %v", err)
		}
		cancel()
	}
}
