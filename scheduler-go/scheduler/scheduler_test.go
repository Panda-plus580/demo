package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor 是测试里等待异步结果的统一上限，避免偶发抖动导致误报。
const waitFor = 5 * time.Second

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func ctxWithTimeout(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), waitFor)
}

// waitStatus 等待任务进入终态并返回快照。
func waitStatus(t *testing.T, s *Scheduler, id string) TaskResult {
	t.Helper()
	ctx, cancel := ctxWithTimeout(t)
	defer cancel()

	res, err := s.Wait(ctx, id)
	if err != nil {
		t.Fatalf("wait %q: %v", id, err)
	}
	return res
}

// TestExecutesInRunAtOrder 验证「按执行时间顺序执行」这一核心需求。
func TestExecutesInRunAtOrder(t *testing.T) {
	s := New(WithWorkers(4), WithQueueSize(16))
	defer func() { _ = s.Close(context.Background()) }()

	// 故意乱序提交，验证调度顺序由 RunAt 决定，而不是提交顺序。
	delays := []struct {
		id    string
		delay time.Duration
	}{
		{"c-late", 240 * time.Millisecond},
		{"a-early", 80 * time.Millisecond},
		{"b-middle", 160 * time.Millisecond},
	}

	base := time.Now()
	var mu sync.Mutex
	var order []string

	for _, d := range delays {
		id := d.id
		err := s.AddTask(TaskSpec{
			ID:    id,
			RunAt: base.Add(d.delay),
			Fn: func() (string, error) {
				mu.Lock()
				order = append(order, id)
				mu.Unlock()
				return "ok:" + id, nil
			},
		})
		if err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	for _, d := range delays {
		res := waitStatus(t, s, d.id)
		if res.Status != StatusExecuted {
			t.Fatalf("task %s: status = %s, want 已执行", d.id, res.Status)
		}
		if want := "ok:" + d.id; res.Result != want {
			t.Fatalf("task %s: result = %q, want %q", d.id, res.Result, want)
		}
	}

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()

	want := []string{"a-early", "b-middle", "c-late"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("执行顺序 = %v, want %v", got, want)
	}
}

// TestAddTaskValidation 验证非法输入被拒绝，且不会污染调度器状态。
func TestAddTaskValidation(t *testing.T) {
	s := New()
	defer func() { _ = s.Close(context.Background()) }()

	cases := []struct {
		name string
		spec TaskSpec
	}{
		{"空 ID", TaskSpec{Fn: func() (string, error) { return "", nil }}},
		{"无任务函数", TaskSpec{ID: "x"}},
		{"负数超时", TaskSpec{ID: "y", Timeout: -time.Second, Fn: func() (string, error) { return "", nil }}},
	}
	for _, c := range cases {
		if err := s.AddTask(c.spec); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("%s: err = %v, want ErrInvalidSpec", c.name, err)
		}
	}

	if n := s.Len(); n != 0 {
		t.Fatalf("非法任务不应被登记, Len = %d", n)
	}
}

// TestDuplicateAndNotFound 验证 ID 唯一性与查询错误语义。
func TestDuplicateAndNotFound(t *testing.T) {
	s := New()
	defer func() { _ = s.Close(context.Background()) }()

	spec := TaskSpec{ID: "dup", RunAt: time.Now().Add(time.Hour), Fn: func() (string, error) { return "", nil }}
	if err := s.AddTask(spec); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := s.AddTask(spec); !errors.Is(err, ErrTaskExists) {
		t.Fatalf("second add: err = %v, want ErrTaskExists", err)
	}
	if _, err := s.Status("nope"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("status: err = %v, want ErrTaskNotFound", err)
	}
	if err := s.Cancel("nope"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cancel: err = %v, want ErrTaskNotFound", err)
	}
}

// TestCancelPendingTaskNeverRuns 验证待执行任务被取消后函数根本不会被调用。
func TestCancelPendingTaskNeverRuns(t *testing.T) {
	s := New()
	defer func() { _ = s.Close(context.Background()) }()

	var called atomic.Int32
	err := s.AddTask(TaskSpec{
		ID:    "pending",
		RunAt: time.Now().Add(time.Hour),
		Fn: func() (string, error) {
			called.Add(1)
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := s.Cancel("pending"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	// 取消是幂等的。
	if err := s.Cancel("pending"); err != nil {
		t.Fatalf("second cancel should be no-op, got %v", err)
	}

	res := waitStatus(t, s, "pending")
	if res.Status != StatusCancelled {
		t.Fatalf("status = %s, want 已取消", res.Status)
	}
	if !errors.Is(res.Err, ErrTaskCancelled) {
		t.Fatalf("err = %v, want ErrTaskCancelled", res.Err)
	}
	if n := called.Load(); n != 0 {
		t.Fatalf("被取消的任务不应执行, called = %d", n)
	}
	if st, _ := s.Status("pending"); st != StatusCancelled {
		t.Fatalf("Status() = %s, want 已取消", st)
	}
}

// TestCancelRunningTaskStopsIt 验证执行中的任务可以通过 context 被取消。
func TestCancelRunningTaskStopsIt(t *testing.T) {
	s := New(WithWorkers(2))
	defer func() { _ = s.Close(context.Background()) }()

	started := make(chan struct{})
	err := s.AddTask(TaskSpec{
		ID: "running",
		FnCtx: func(ctx context.Context) (string, error) {
			close(started)
			<-ctx.Done() // 协作式取消：等待调度器打断
			return "", ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	select {
	case <-started:
	case <-time.After(waitFor):
		t.Fatal("任务没有开始执行")
	}

	if err := s.Cancel("running"); err != nil {
		t.Fatalf("cancel running: %v", err)
	}

	res := waitStatus(t, s, "running")
	if res.Status != StatusCancelled {
		t.Fatalf("status = %s, want 已取消", res.Status)
	}
	if res.Result != "" {
		t.Fatalf("被取消的任务结果应被丢弃, result = %q", res.Result)
	}
}

// TestCancelFinishedTaskRejected 验证已执行的任务不能再被取消。
func TestCancelFinishedTaskRejected(t *testing.T) {
	s := New()
	defer func() { _ = s.Close(context.Background()) }()

	err := s.AddTask(TaskSpec{ID: "done", Fn: func() (string, error) { return "ok", nil }})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	waitStatus(t, s, "done")

	if err := s.Cancel("done"); !errors.Is(err, ErrTaskFinished) {
		t.Fatalf("cancel finished: err = %v, want ErrTaskFinished", err)
	}
}

// TestTaskErrorIsRecorded 验证任务返回的错误被原样记录，且不影响其他任务。
func TestTaskErrorIsRecorded(t *testing.T) {
	s := New(WithWorkers(2))
	defer func() { _ = s.Close(context.Background()) }()

	boom := errors.New("boom")
	if err := s.AddTask(TaskSpec{ID: "bad", Fn: func() (string, error) {
		return "", fmt.Errorf("boom: %w", boom)
	}}); err != nil {
		t.Fatalf("add bad: %v", err)
	}
	if err := s.AddTask(TaskSpec{ID: "good", Fn: func() (string, error) { return "fine", nil }}); err != nil {
		t.Fatalf("add good: %v", err)
	}

	bad := waitStatus(t, s, "bad")
	if bad.Status != StatusFailed {
		t.Fatalf("status = %s, want 错误", bad.Status)
	}
	if !errors.Is(bad.Err, boom) {
		t.Fatalf("err = %v, want 可 errors.Is(boom)", bad.Err)
	}

	good := waitStatus(t, s, "good")
	if good.Status != StatusExecuted || good.Result != "fine" {
		t.Fatalf("错误任务影响了其他任务: %+v", good)
	}
}

// TestPanicIsIsolated 验证 panic 被转成 error，且不会击穿调度器。
func TestPanicIsIsolated(t *testing.T) {
	s := New(WithWorkers(2), WithLogger(testLogger()))
	defer func() { _ = s.Close(context.Background()) }()

	if err := s.AddTask(TaskSpec{ID: "panic", Fn: func() (string, error) {
		panic("任务炸了")
	}}); err != nil {
		t.Fatalf("add panic: %v", err)
	}
	if err := s.AddTask(TaskSpec{ID: "after", RunAt: time.Now().Add(20 * time.Millisecond), Fn: func() (string, error) {
		return "仍然可以执行", nil
	}}); err != nil {
		t.Fatalf("add after: %v", err)
	}

	res := waitStatus(t, s, "panic")
	if res.Status != StatusFailed {
		t.Fatalf("status = %s, want 错误", res.Status)
	}

	var pe *PanicError
	if !errors.As(res.Err, &pe) {
		t.Fatalf("err = %v (%T), want *PanicError", res.Err, res.Err)
	}
	if len(pe.StackTrace()) == 0 {
		t.Fatal("PanicError 应该携带调用栈")
	}

	if after := waitStatus(t, s, "after"); after.Status != StatusExecuted {
		t.Fatalf("panic 影响了后续任务: %+v", after)
	}
	if got := s.Stats().Panicked; got != 1 {
		t.Fatalf("Stats().Panicked = %d, want 1", got)
	}
}

// TestTimeoutIsRecorded 验证单任务超时被识别为错误。
func TestTimeoutIsRecorded(t *testing.T) {
	s := New(WithWorkers(2))
	defer func() { _ = s.Close(context.Background()) }()

	if err := s.AddTask(TaskSpec{
		ID:      "slow",
		Timeout: 50 * time.Millisecond,
		FnCtx: func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	res := waitStatus(t, s, "slow")
	if res.Status != StatusFailed {
		t.Fatalf("status = %s, want 错误", res.Status)
	}
	if !errors.Is(res.Err, ErrTaskTimeout) {
		t.Fatalf("err = %v, want ErrTaskTimeout", res.Err)
	}
}

// TestTimeoutDoesNotBlockWorker 验证「卡死」的任务函数不会永久占住 worker。
func TestTimeoutDoesNotBlockWorker(t *testing.T) {
	s := New(WithWorkers(1)) // 只有一个 worker，更容易暴露阻塞问题
	defer func() { _ = s.Close(context.Background()) }()

	release := make(chan struct{})
	defer close(release)

	if err := s.AddTask(TaskSpec{
		ID:      "hang",
		Timeout: 30 * time.Millisecond,
		Fn: func() (string, error) { // 完全不响应 ctx 的任务
			<-release
			return "", nil
		},
	}); err != nil {
		t.Fatalf("add hang: %v", err)
	}
	if err := s.AddTask(TaskSpec{ID: "next", RunAt: time.Now().Add(60 * time.Millisecond), Fn: func() (string, error) {
		return "worker 已被释放", nil
	}}); err != nil {
		t.Fatalf("add next: %v", err)
	}

	if res := waitStatus(t, s, "hang"); !errors.Is(res.Err, ErrTaskTimeout) {
		t.Fatalf("hang err = %v, want ErrTaskTimeout", res.Err)
	}
	if res := waitStatus(t, s, "next"); res.Status != StatusExecuted {
		t.Fatalf("worker 被卡死任务占用: %+v", res)
	}
}

// TestRetryUntilSuccess 验证重试机制与尝试次数统计。
func TestRetryUntilSuccess(t *testing.T) {
	s := New(WithWorkers(1))
	defer func() { _ = s.Close(context.Background()) }()

	var calls atomic.Int32
	err := s.AddTask(TaskSpec{
		ID:           "flaky",
		MaxAttempts:  3,
		RetryBackoff: 10 * time.Millisecond,
		Fn: func() (string, error) {
			if n := calls.Add(1); n < 3 {
				return "", fmt.Errorf("第 %d 次失败", n)
			}
			return "第三次成功", nil
		},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	res := waitStatus(t, s, "flaky")
	if res.Status != StatusExecuted {
		t.Fatalf("status = %s, want 已执行", res.Status)
	}
	if res.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", res.Attempts)
	}
	if res.Result != "第三次成功" {
		t.Fatalf("result = %q", res.Result)
	}
	if got := s.Stats().Retried; got != 2 {
		t.Fatalf("Stats().Retried = %d, want 2", got)
	}
}

// TestConcurrentAddCancelAndQuery 是并发安全的主测试：
// 多个 goroutine 同时提交、取消、查询，要求 -race 下无数据竞争、状态自洽。
func TestConcurrentAddCancelAndQuery(t *testing.T) {
	const (
		writers = 16
		// perGoro 必须是偶数，这样 i%2 恰好把任务平分成「立即执行」与「远期取消」两半。
		perGoro = 24
	)
	total := writers * perGoro

	s := New(WithWorkers(8), WithQueueSize(32))
	defer func() { _ = s.Close(context.Background()) }()

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perGoro; i++ {
				id := fmt.Sprintf("t-%02d-%03d", w, i)

				spec := TaskSpec{ID: id}
				if i%2 == 0 {
					// 立即执行
					spec.Fn = func() (string, error) { return "ok", nil }
				} else {
					// 远期执行，稍后取消
					spec.RunAt = time.Now().Add(time.Hour)
					spec.Fn = func() (string, error) { return "should-not-run", nil }
				}

				if err := s.AddTask(spec); err != nil {
					t.Errorf("add %s: %v", id, err)
					return
				}

				if i%2 == 1 {
					switch err := s.Cancel(id); {
					case err == nil, errors.Is(err, ErrTaskFinished):
						// 取消成功；或任务恰好已执行完毕，都属正常竞态结果
					default:
						t.Errorf("cancel %s: %v", id, err)
					}
				}

				// 并发查询与统计，确保读路径不加错锁。
				if _, err := s.Status(id); err != nil {
					t.Errorf("status %s: %v", id, err)
				}
				if _, err := s.Result(id); err != nil {
					t.Errorf("result %s: %v", id, err)
				}
				_ = s.Stats()
			}
		}(w)
	}
	wg.Wait()

	if n := s.Len(); n != total {
		t.Fatalf("Len = %d, want %d", n, total)
	}

	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()

	snap := s.Snapshot()
	if len(snap) != total {
		t.Fatalf("Snapshot len = %d, want %d", len(snap), total)
	}

	var executed, cancelledCount int
	for _, res := range snap {
		switch res.Status {
		case StatusCancelled:
			cancelledCount++
		case StatusExecuted:
			executed++
			if res.Result != "ok" {
				t.Fatalf("task %s: 不该执行的任务被执行了, result = %q", res.ID, res.Result)
			}
		default:
			// 未终结的任务用 Wait 收敛（例如刚出堆还没写状态）。
			final, err := s.Wait(ctx, res.ID)
			if err != nil {
				t.Fatalf("wait %s: %v", res.ID, err)
			}
			if !final.Status.Terminal() {
				t.Fatalf("task %s 仍然是 %s", res.ID, final.Status)
			}
			if final.Status == StatusExecuted {
				executed++
			} else {
				cancelledCount++
			}
		}
	}

	if executed+cancelledCount != total {
		t.Fatalf("状态统计不自洽: executed=%d cancelled=%d total=%d", executed, cancelledCount, total)
	}
	if executed != total/2 {
		t.Fatalf("立即执行的任务数 = %d, want %d", executed, total/2)
	}
	if got := s.Stats().Submitted; got != uint64(total) {
		t.Fatalf("Stats().Submitted = %d, want %d", got, total)
	}
}

// TestCloseCancelsPendingAndWaits 验证优雅关闭语义。
func TestCloseCancelsPendingAndWaits(t *testing.T) {
	s := New(WithWorkers(4))

	var called atomic.Int32
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("later-%d", i)
		if err := s.AddTask(TaskSpec{
			ID:    id,
			RunAt: time.Now().Add(time.Hour),
			Fn:    func() (string, error) { called.Add(1); return "", nil },
		}); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	ctx, cancel := ctxWithTimeout(t)
	defer cancel()

	if err := s.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	if !s.Closed() {
		t.Fatal("Closed() = false, want true")
	}
	if err := s.AddTask(TaskSpec{ID: "after-close", Fn: func() (string, error) { return "", nil }}); !errors.Is(err, ErrClosed) {
		t.Fatalf("add after close: err = %v, want ErrClosed", err)
	}
	// 重复 Close 安全。
	if err := s.Close(ctx); err != nil {
		t.Fatalf("second close: %v", err)
	}

	for i := 0; i < 5; i++ {
		res, err := s.Result(fmt.Sprintf("later-%d", i))
		if err != nil {
			t.Fatalf("result: %v", err)
		}
		if res.Status != StatusCancelled {
			t.Fatalf("关闭后待执行任务应记为已取消, got %s", res.Status)
		}
		if !errors.Is(res.Err, ErrClosed) {
			t.Fatalf("err = %v, want ErrClosed", res.Err)
		}
	}
	if n := called.Load(); n != 0 {
		t.Fatalf("关闭时未到期的任务不应执行, called = %d", n)
	}

	st := s.Stats()
	if st.Pending != 0 || st.Running != 0 {
		t.Fatalf("关闭后不应有在途任务: %+v", st)
	}
}

// TestWaitHonoursContext 验证 Wait 可以被 ctx 打断。
func TestWaitHonoursContext(t *testing.T) {
	s := New()
	defer func() { _ = s.Close(context.Background()) }()

	if err := s.AddTask(TaskSpec{ID: "far", RunAt: time.Now().Add(time.Hour), Fn: func() (string, error) {
		return "", nil
	}}); err != nil {
		t.Fatalf("add: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	res, err := s.Wait(ctx, "far")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if res.Status != StatusPending {
		t.Fatalf("status = %s, want 待执行", res.Status)
	}
}

// TestSubscribeReceivesEvents 验证事件订阅（观测解耦）。
func TestSubscribeReceivesEvents(t *testing.T) {
	s := New()
	defer func() { _ = s.Close(context.Background()) }()

	events, unsubscribe := s.Subscribe(8)
	defer unsubscribe()

	if err := s.AddTask(TaskSpec{ID: "evt", Fn: func() (string, error) { return "done", nil }}); err != nil {
		t.Fatalf("add: %v", err)
	}

	select {
	case ev := <-events:
		if ev.TaskID != "evt" || ev.Status != StatusExecuted || ev.Result != "done" {
			t.Fatalf("事件内容不符: %+v", ev)
		}
	case <-time.After(waitFor):
		t.Fatal("没有收到任务事件")
	}
}

// TestStatsGauges 验证瞬时水位统计。
func TestStatsGauges(t *testing.T) {
	s := New(WithWorkers(2))
	defer func() { _ = s.Close(context.Background()) }()

	if err := s.AddTask(TaskSpec{ID: "p1", RunAt: time.Now().Add(time.Hour), Fn: func() (string, error) {
		return "", nil
	}}); err != nil {
		t.Fatalf("add: %v", err)
	}

	st := s.Stats()
	if st.Pending != 1 || st.Queued != 1 || st.Workers != 2 {
		t.Fatalf("Stats = %+v, want Pending=1 Queued=1 Workers=2", st)
	}
	if st.Submitted != 1 {
		t.Fatalf("Submitted = %d, want 1", st.Submitted)
	}
}
