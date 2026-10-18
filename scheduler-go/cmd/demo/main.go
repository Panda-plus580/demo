// 命令 demo 演示 scheduler 包的核心能力：
//
//	go run ./cmd/demo
//
// 覆盖：按执行时间顺序调度、并发提交/取消、panic 隔离、失败重试、超时、
// 状态查询与指标、事件订阅、优雅关闭。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scheduler-go/scheduler"
)

const demoTimeout = 5 * time.Second

func main() {
	orderDemo()
	concurrencyDemo()
	errorDemo()
	queryDemo()
	shutdownDemo()

	fmt.Println("全部演示结束。")
}

// orderDemo 演示「按任务的执行时间顺序自动执行」，且顺序与提交顺序无关。
func orderDemo() {
	fmt.Println("=== 1. 按执行时间顺序调度（故意乱序提交）===")

	s := scheduler.New(scheduler.WithWorkers(4))
	defer closeScheduler(s)

	items := []struct {
		id    string
		after time.Duration
	}{
		{"任务C", 300 * time.Millisecond},
		{"任务A", 100 * time.Millisecond},
		{"任务B", 200 * time.Millisecond},
	}

	base := time.Now()
	var mu sync.Mutex
	var order []string

	for _, it := range items {
		it := it
		must(s.AddTask(scheduler.TaskSpec{
			ID:    it.id,
			RunAt: base.Add(it.after),
			Fn: func() (string, error) {
				mu.Lock()
				order = append(order, it.id)
				mu.Unlock()
				return "执行于 " + time.Now().Format("15:04:05.000"), nil
			},
		}))
		fmt.Printf("  提交 %s：期望 %dms 后执行\n", it.id, it.after.Milliseconds())
	}

	for _, it := range items {
		res := mustWait(s, it.id)
		fmt.Printf("  %s 状态=%s 结果=%q\n", it.id, res.Status, res.Result)
	}

	mu.Lock()
	defer mu.Unlock()
	fmt.Printf("  实际执行顺序 = %v（与提交顺序相反，符合 RunAt 排序）\n\n", order)
}

// concurrencyDemo 演示多 goroutine 并发添加 / 取消 / 查询。
func concurrencyDemo() {
	fmt.Println("=== 2. 并发添加与取消（-race 可验证）===")

	s := scheduler.New(scheduler.WithWorkers(8), scheduler.WithQueueSize(16))
	defer closeScheduler(s)

	const (
		goroutines = 20
		perGoro    = 10
	)

	var added, cancelled atomic.Int64

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoro; i++ {
				id := fmt.Sprintf("job-%02d-%02d", g, i)

				spec := scheduler.TaskSpec{
					ID: id,
					Fn: func() (string, error) { return "ok", nil },
				}
				if i%2 == 1 {
					// 一半任务排在 1 小时后，随后立刻被取消。
					spec.RunAt = time.Now().Add(time.Hour)
					spec.Fn = func() (string, error) { return "不该执行", nil }
				}

				if err := s.AddTask(spec); err != nil {
					fmt.Printf("  ! 添加 %s 失败: %v\n", id, err)
					return
				}
				added.Add(1)

				if i%2 == 1 {
					switch err := s.Cancel(id); {
					case err == nil, errors.Is(err, scheduler.ErrTaskFinished):
						cancelled.Add(1)
					default:
						fmt.Printf("  ! 取消 %s 失败: %v\n", id, err)
					}
				}

				// 并发查询：验证读路径也是并发安全的。
				_, _ = s.Status(id)
				_, _ = s.Result(id)
				_ = s.Stats()
			}
		}(g)
	}
	wg.Wait()

	waitDrained(s)

	st := s.Stats()
	fmt.Printf("  并发提交 %d 个任务，并发取消 %d 个\n", added.Load(), cancelled.Load())
	fmt.Printf("  指标：提交=%d 已执行=%d 已取消=%d 错误=%d | 剩余 待执行=%d 执行中=%d 堆内=%d worker=%d\n\n",
		st.Submitted, st.Executed, st.Cancelled, st.Failed, st.Pending, st.Running, st.Queued, st.Workers)
}

// errorDemo 演示错误隔离：panic 转 error、失败重试、超时，且互不影响。
func errorDemo() {
	fmt.Println("=== 3. 错误处理：panic 隔离 / 重试 / 超时 ===")

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s := scheduler.New(scheduler.WithWorkers(4), scheduler.WithLogger(logger))
	defer closeScheduler(s)

	// 3.1 panic：任务崩溃被隔离成普通 error，不会击穿进程。
	must(s.AddTask(scheduler.TaskSpec{
		ID: "panic-task",
		Fn: func() (string, error) { panic("模拟业务 panic") },
	}))

	// 3.2 重试：前两次失败，第三次成功。
	var attempts atomic.Int32
	must(s.AddTask(scheduler.TaskSpec{
		ID:           "flaky-task",
		MaxAttempts:  3,
		RetryBackoff: 50 * time.Millisecond,
		Fn: func() (string, error) {
			if n := attempts.Add(1); n < 3 {
				return "", fmt.Errorf("第 %d 次尝试失败", n)
			}
			return "第 3 次尝试成功", nil
		},
	}))

	// 3.3 超时：任务卡住 80ms 后由调度器判定超时。
	must(s.AddTask(scheduler.TaskSpec{
		ID:      "timeout-task",
		Timeout: 80 * time.Millisecond,
		FnCtx: func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}))

	// 3.4 健康任务：前面三个任务的失败不能影响它。
	must(s.AddTask(scheduler.TaskSpec{
		ID:    "healthy-task",
		RunAt: time.Now().Add(200 * time.Millisecond),
		Fn:    func() (string, error) { return "一切正常", nil },
	}))

	fmt.Println()
	for _, id := range []string{"panic-task", "flaky-task", "timeout-task", "healthy-task"} {
		res := mustWait(s, id)
		fmt.Printf("  %-13s 状态=%-6s 尝试=%d 结果=%-14q 错误=%v\n",
			id, res.Status, res.Attempts, res.Result, res.Err)
	}

	pres := mustWait(s, "panic-task")
	var pe *scheduler.PanicError
	if errors.As(pres.Err, &pe) {
		stack := strings.Split(string(pe.StackTrace()), "\n")
		fmt.Printf("  panic 原始值=%v，调用栈首帧=%s\n", pe.Value, strings.TrimSpace(stack[0]))
	}
	fmt.Println()
}

// queryDemo 演示状态查询接口、全量快照与事件订阅。
func queryDemo() {
	fmt.Println("=== 4. 状态查询 / 快照 / 事件订阅 ===")

	s := scheduler.New(scheduler.WithWorkers(2))
	defer closeScheduler(s)

	events, unsubscribe := s.Subscribe(32)
	defer unsubscribe()

	must(s.AddTask(scheduler.TaskSpec{ID: "ok-task", Fn: func() (string, error) { return "成功", nil }}))
	must(s.AddTask(scheduler.TaskSpec{ID: "fail-task", RunAt: time.Now().Add(30 * time.Millisecond), Fn: func() (string, error) {
		return "", errors.New("业务校验失败")
	}}))
	must(s.AddTask(scheduler.TaskSpec{ID: "cancel-task", RunAt: time.Now().Add(time.Hour), Fn: func() (string, error) {
		return "不该执行", nil
	}}))
	must(s.AddTask(scheduler.TaskSpec{ID: "wait-task", RunAt: time.Now().Add(60 * time.Millisecond), Fn: func() (string, error) {
		return "稍后完成", nil
	}}))

	// 查询单个任务：待执行 -> 已取消
	if st, err := s.Status("cancel-task"); err == nil {
		fmt.Printf("  cancel-task 当前状态=%s\n", st)
	}
	must(s.Cancel("cancel-task"))
	if st, err := s.Status("cancel-task"); err == nil {
		fmt.Printf("  cancel-task 取消后状态=%s\n", st)
	}

	for _, id := range []string{"ok-task", "fail-task", "wait-task"} {
		mustWait(s, id)
	}

	fmt.Println("  全量快照（Snapshot）：")
	fmt.Printf("    %-13s %-18s %-8s %-6s %s\n", "ID", "状态", "尝试", "耗时", "结果/错误")
	for _, res := range s.Snapshot() {
		detail := res.Result
		if res.Err != nil {
			detail = "error: " + res.Err.Error()
		}
		cost := "-"
		if !res.StartedAt.IsZero() && !res.FinishedAt.IsZero() {
			cost = res.FinishedAt.Sub(res.StartedAt).Round(time.Millisecond).String()
		}
		fmt.Printf("    %-13s %-18s %-8d %-6s %s\n", res.ID, res.Status, res.Attempts, cost, detail)
	}

	// 事件订阅：观测者异步消费任务生命周期事件。
	var received int
	deadline := time.After(300 * time.Millisecond)
loop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break loop
			}
			received++
			fmt.Printf("    [事件] %s -> %s\n", ev.TaskID, ev.Status)
		case <-deadline:
			break loop
		}
	}
	fmt.Printf("  共订阅到 %d 个事件\n\n", received)
}

// shutdownDemo 演示优雅关闭：待执行任务被取消，在途任务被打断并等待收尾。
func shutdownDemo() {
	fmt.Println("=== 5. 优雅关闭 ===")

	s := scheduler.New(scheduler.WithWorkers(4))

	must(s.AddTask(scheduler.TaskSpec{
		ID: "in-flight",
		FnCtx: func(ctx context.Context) (string, error) {
			<-ctx.Done() // 在途任务：被 Close 打断后立即收尾
			return "", ctx.Err()
		},
	}))

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("pending-%d", i)
		must(s.AddTask(scheduler.TaskSpec{
			ID:    id,
			RunAt: time.Now().Add(time.Hour),
			Fn:    func() (string, error) { return "不该执行", nil },
		}))
	}

	// 等到在途任务真的开始执行，观察效果更直观。
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), demoTimeout)
	defer cancel()

	start := time.Now()
	if err := s.Close(ctx); err != nil {
		fmt.Printf("  ! 关闭失败: %v\n", err)
		return
	}
	fmt.Printf("  Close 返回，耗时 %v\n", time.Since(start).Round(time.Millisecond))

	if err := s.AddTask(scheduler.TaskSpec{ID: "after-close", Fn: func() (string, error) { return "", nil }}); err != nil {
		fmt.Printf("  关闭后提交任务被拒绝: %v\n", err)
	}

	for _, id := range []string{"in-flight", "pending-0", "pending-1", "pending-2"} {
		res, err := s.Result(id)
		if err != nil {
			fmt.Printf("  ! 查询 %s 失败: %v\n", id, err)
			continue
		}
		fmt.Printf("  %-10s 最终状态=%s 错误=%v\n", id, res.Status, res.Err)
	}

	st := s.Stats()
	fmt.Printf("  关闭后无在途任务：待执行=%d 执行中=%d\n\n", st.Pending, st.Running)
}

// ---------------------------------------------------------------- 辅助函数

func must(err error) {
	if err != nil {
		fmt.Printf("  ! 操作失败: %v\n", err)
	}
}

func mustWait(s *scheduler.Scheduler, id string) scheduler.TaskResult {
	ctx, cancel := context.WithTimeout(context.Background(), demoTimeout)
	defer cancel()

	res, err := s.Wait(ctx, id)
	if err != nil {
		fmt.Printf("  ! 等待 %s 超时: %v\n", id, err)
	}
	return res
}

func closeScheduler(s *scheduler.Scheduler) {
	ctx, cancel := context.WithTimeout(context.Background(), demoTimeout)
	defer cancel()
	_ = s.Close(ctx)
}

// waitDrained 轮询等待调度器把堆内任务处理干净（演示用，最多等 2 秒）。
func waitDrained(s *scheduler.Scheduler) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st := s.Stats()
		if st.Pending == 0 && st.Running == 0 && st.Queued == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
