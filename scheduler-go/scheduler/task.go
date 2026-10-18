package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// TaskFunc 是任务函数：无参数，返回结果字符串与错误。
type TaskFunc func() (string, error)

// TaskFuncCtx 是带 context 的任务函数。
//
// 相比 TaskFunc，它多一个能力：任务可以感知取消与超时并主动提前退出，
// 从而避免「调度器已经放弃结果、函数却还在后台跑」的资源浪费。
// 代价是任务实现要自己检查 ctx；对纯 CPU 计算类任务收益不大。
type TaskFuncCtx func(ctx context.Context) (string, error)

// Status 是任务的对外状态。
type Status uint8

const (
	StatusPending   Status = iota // 待执行
	StatusRunning                 // 执行中
	StatusExecuted                // 已执行（成功返回）
	StatusCancelled               // 已取消（执行前被取消，或执行中被取消）
	StatusFailed                  // 错误（返回 error / panic / 超时）
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "待执行"
	case StatusRunning:
		return "执行中"
	case StatusExecuted:
		return "已执行"
	case StatusCancelled:
		return "已取消"
	case StatusFailed:
		return "错误"
	default:
		return "未知"
	}
}

// Terminal 报告任务是否已经进入终态。终态是单向的，不会再变化。
func (s Status) Terminal() bool {
	return s == StatusExecuted || s == StatusCancelled || s == StatusFailed
}

// TaskSpec 描述一个待调度任务。字段全部为值语义，提交后调度器不再读原变量。
type TaskSpec struct {
	// ID 是任务的唯一标识，必填。重复提交同一 ID 会返回 ErrTaskExists。
	ID string

	// RunAt 是期望执行时间。零值或过去时间表示「尽快执行」。
	RunAt time.Time

	// Fn 是任务函数，与 FnCtx 二选一（必填其一）。
	Fn TaskFunc

	// FnCtx 是可选的 context 版本任务函数；两者都提供时优先使用 FnCtx。
	FnCtx TaskFuncCtx

	// Timeout 是单次执行超时，0 表示不限制。
	Timeout time.Duration

	// MaxAttempts 是最大尝试次数（含首次），<=0 时按 1 处理。
	MaxAttempts int

	// RetryBackoff 是失败后的重试间隔，0 表示立即重试。
	RetryBackoff time.Duration

	// Tags 只做透传，供业务侧筛选/埋点使用。
	Tags map[string]string
}

func (spec TaskSpec) validate() error {
	if strings.TrimSpace(spec.ID) == "" {
		return fmt.Errorf("%w: task id must not be empty", ErrInvalidSpec)
	}
	if spec.Fn == nil && spec.FnCtx == nil {
		return fmt.Errorf("%w: task %q has no function", ErrInvalidSpec, spec.ID)
	}
	if spec.Timeout < 0 {
		return fmt.Errorf("%w: task %q has negative timeout", ErrInvalidSpec, spec.ID)
	}
	if spec.RetryBackoff < 0 {
		return fmt.Errorf("%w: task %q has negative retry backoff", ErrInvalidSpec, spec.ID)
	}
	return nil
}

// TaskResult 是任务在某一时刻的完整快照，用于状态查询。
type TaskResult struct {
	ID         string
	Status     Status
	Result     string    // 成功时任务函数返回的字符串
	Err        error     // 取消/失败原因（可 errors.Is / errors.As）
	Attempts   int       // 已尝试次数
	RunAt      time.Time // 归一化后的执行时间
	StartedAt  time.Time // 首次开始执行时间
	FinishedAt time.Time // 进入终态时间
	Tags       map[string]string
}

// Event 是任务生命周期事件，用于解耦观测（日志、埋点、UI 推送）。
type Event struct {
	TaskID   string
	Status   Status
	Result   string
	Err      error
	Attempts int
	At       time.Time
}

// taskEntry 是调度器内部的任务实体。
//
// 它同时承载两类数据，分别由不同的锁保护：
//   - index：堆内下标，结构性数据，由 Scheduler.mu 保护；
//   - status/result/err/attempts/cancel：状态型数据，由自己的 mu 保护。
//
// 这样「任务结果写入」和「任务状态查询」都不会去抢全局锁。
type taskEntry struct {
	id            string
	runAt         time.Time
	fn            TaskFunc    // 只读
	fnCtx         TaskFuncCtx // 只读
	timeout       time.Duration
	attemptsLimit int
	backoff       time.Duration
	tags          map[string]string // 只读

	// index 是任务在最小堆中的下标，-1 表示不在堆中（已出堆或从未入堆）。
	// 只由 Scheduler.mu 保护。
	index int

	// done 在任务进入终态时被关闭一次，用于 Wait 阻塞等待。
	done     chan struct{}
	doneOnce sync.Once

	// mu 保护以下字段。加锁顺序固定为 Scheduler.mu -> taskEntry.mu（不可反向）。
	mu         sync.Mutex
	status     Status
	result     string
	err        error
	attempts   int
	startedAt  time.Time
	finishedAt time.Time
	cancel     context.CancelFunc
}

func newTaskEntry(spec TaskSpec, now time.Time) *taskEntry {
	runAt := spec.RunAt
	if runAt.IsZero() || runAt.Before(now) {
		// 过去时间归一化为「立刻到期」，保证堆顶判定的单调性。
		runAt = now
	}

	limit := spec.MaxAttempts
	if limit <= 0 {
		limit = 1
	}

	return &taskEntry{
		id:            spec.ID,
		runAt:         runAt,
		fn:            spec.Fn,
		fnCtx:         spec.FnCtx,
		timeout:       spec.Timeout,
		attemptsLimit: limit,
		backoff:       spec.RetryBackoff,
		tags:          spec.Tags,
		index:         -1,
		status:        StatusPending,
		done:          make(chan struct{}),
	}
}

// finish 尝试把任务置为终态，返回是否真正发生了状态迁移，以及最终尝试次数。
//
// 这是「终态幂等」的实现：Worker 执行完成、用户取消、调度器关闭可能同时发生，
// 但只有第一个成功把状态写成终态的人生效，后来的调用都是空操作，
// 因此不会出现「已取消的任务又被写成已执行」这类脏写。
// 返回 changed=false 时，调用方不要再计入统计或发事件。
func (e *taskEntry) finish(status Status, result string, err error, at time.Time) (changed bool, attempts int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status.Terminal() {
		return false, e.attempts
	}
	e.status = status
	e.result = result
	e.err = err
	e.finishedAt = at
	e.cancel = nil
	e.doneOnce.Do(func() { close(e.done) })
	return true, e.attempts
}

// markRunning 把任务从待执行切到执行中；若已被取消则返回 false。
func (e *taskEntry) markRunning(at time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status.Terminal() {
		return false
	}
	e.status = StatusRunning
	if e.startedAt.IsZero() {
		e.startedAt = at
	}
	return true
}

// nextAttempt 递增尝试次数并返回它；任务已终态时返回 0。
func (e *taskEntry) nextAttempt() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status.Terminal() {
		return 0
	}
	e.attempts++
	return e.attempts
}

// setCancel 登记本次尝试的 cancel 函数；若任务已终态则立刻取消，避免 ctx 泄漏。
func (e *taskEntry) setCancel(cancel context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status.Terminal() {
		cancel()
		return
	}
	e.cancel = cancel
}

func (e *taskEntry) clearCancel() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancel = nil
}

// cancelNow 取消正在执行的任务（若存在）。待执行任务没有 cancel，安全返回。
func (e *taskEntry) cancelNow() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
}

func (e *taskEntry) statusNow() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

// snapshot 返回一致性快照（status/result/err 在同一临界区内读取，不会撕裂）。
func (e *taskEntry) snapshot() TaskResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	return TaskResult{
		ID:         e.id,
		Status:     e.status,
		Result:     e.result,
		Err:        e.err,
		Attempts:   e.attempts,
		RunAt:      e.runAt,
		StartedAt:  e.startedAt,
		FinishedAt: e.finishedAt,
		Tags:       e.tags,
	}
}
