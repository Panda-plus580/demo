package scheduler

import (
	"log/slog"
	"time"
)

// Options 是调度器的可调参数，通过 Option 函数式选项设置。
type Options struct {
	// Workers 是同时执行任务的 worker 数量（并发上限），默认 8。
	// 它给「任务执行」加了一道闸门，避免海量任务同时到期时 goroutine 爆炸。
	Workers int

	// QueueSize 是「已到期但还没被 worker 领走」的任务缓冲长度，默认 2*Workers。
	// 缓冲满时调度循环会阻塞在投递上，形成背压（backpressure）。
	QueueSize int

	// Now 是可注入的时钟，便于测试。
	Now func() time.Time

	// Logger 用于记录任务失败与 panic，默认丢弃（slog.DiscardHandler）。
	Logger *slog.Logger
}

// Option 是 Options 的函数式选项。
type Option func(*Options)

// WithWorkers 设置 worker 并发数。
func WithWorkers(n int) Option {
	return func(o *Options) { o.Workers = n }
}

// WithQueueSize 设置到期任务缓冲长度。
func WithQueueSize(n int) Option {
	return func(o *Options) { o.QueueSize = n }
}

// WithClock 注入时钟（测试用）。
func WithClock(now func() time.Time) Option {
	return func(o *Options) { o.Now = now }
}

// WithLogger 注入日志器，用于记录任务失败与 panic（含堆栈）。
func WithLogger(l *slog.Logger) Option {
	return func(o *Options) { o.Logger = l }
}

func defaultOptions() Options {
	return Options{
		Workers:   8,
		Logger:    slog.New(slog.DiscardHandler),
		Now:       time.Now,
		QueueSize: 0,
	}
}

// apply 依次应用选项并做兜底校验，保证内部读到的一定是合法值。
func (o *Options) apply(opts []Option) {
	for _, fn := range opts {
		if fn != nil {
			fn(o)
		}
	}
	if o.Workers < 1 {
		o.Workers = 1
	}
	if o.QueueSize <= 0 {
		o.QueueSize = o.Workers * 2
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
}
