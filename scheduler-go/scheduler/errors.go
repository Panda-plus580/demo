package scheduler

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
)

// 哨兵错误：调用方用 errors.Is 判断，而不是比较字符串。
var (
	// ErrClosed 表示调度器已关闭，不再接受新任务。
	ErrClosed = errors.New("scheduler: closed")
	// ErrInvalidSpec 表示任务定义非法（ID 为空、没有任务函数、超时/退避为负）。
	ErrInvalidSpec = errors.New("scheduler: invalid task spec")
	// ErrTaskExists 表示任务 ID 已经被使用。
	ErrTaskExists = errors.New("scheduler: task already exists")
	// ErrTaskNotFound 表示任务 ID 不存在。
	ErrTaskNotFound = errors.New("scheduler: task not found")
	// ErrTaskFinished 表示任务已进入终态（已执行/已失败），无法再取消。
	ErrTaskFinished = errors.New("scheduler: task already finished")
	// ErrTaskCancelled 表示任务被取消（用户主动取消，或执行中被取消）。
	ErrTaskCancelled = errors.New("scheduler: task cancelled")
	// ErrTaskTimeout 表示任务单次执行超时。
	ErrTaskTimeout = errors.New("scheduler: task timeout")
)

// PanicError 把任务函数中的 panic 转成普通 error。
//
// 这是「错误隔离」的关键：任务栈在 goroutine 边界被 recover 住，
// panic 不会向上扩散击穿进程，同时保留了原始值与调用栈供排查。
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("scheduler: task panicked: %v", e.Value)
}

// StackTrace 返回 panic 发生时的调用栈（可能为空）。
func (e *PanicError) StackTrace() []byte { return e.Stack }

// newPanicError 在 recover 现场构造 PanicError（Stack 必须在现场取）。
func newPanicError(v any) *PanicError {
	return &PanicError{Value: v, Stack: debug.Stack()}
}

// contextError 把 ctx 的结束原因归一成调度器自己的错误，
// 便于调用方不用关心底层是 timeout 还是 cancel。
func contextError(ctx context.Context) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return ErrTaskTimeout
	case errors.Is(ctx.Err(), context.Canceled):
		return ErrTaskCancelled
	default:
		return ctx.Err()
	}
}

// normalizeTaskError 把任务自己返回的裸 ctx 错误也归一成调度器错误。
//
// 意义：超时可能由两条路径产生 —— worker 的 select 命中了 ctx.Done()，
// 或者任务自己检查 ctx 后返回了 ctx.Err()。两条路径对外的错误必须是同一个，
// 否则调用方拿到的错误会随机摇摆。
func normalizeTaskError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTaskTimeout
	case errors.Is(err, context.Canceled):
		return ErrTaskCancelled
	default:
		return err
	}
}
