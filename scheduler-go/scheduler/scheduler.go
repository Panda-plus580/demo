package scheduler

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Scheduler 是一个并发安全的任务调度器：任意多个 goroutine 可以同时
// 提交、取消、查询任务，调度器保证按 RunAt 顺序在到期后执行它们。
//
// # 运行模型
//
//   - 1 个调度 goroutine（loop）：维护最小堆，把「最早到期」的任务投递给 worker；
//   - N 个 worker goroutine（worker）：真正执行任务函数，N 由 WithWorkers 决定；
//   - M 个调用方 goroutine：通过 AddTask/Cancel/Status/Result/Wait/Stats 操作调度器。
//
// # 锁模型（两层，严格「外层 -> 内层」顺序，以此避免死锁）
//
//   - s.mu：保护结构性数据（最小堆、任务索引表、closed 标志），
//     临界区只做 O(log n) 的堆操作，绝不执行用户任务函数；
//   - taskEntry.mu：保护状态型数据（status/result/err/attempts/cancel）。
//
// 于是「查询任务状态」和「写入任务结果」都不需要抢全局锁，热点被分散到每个
// 任务自身；相比「一把大锁 + 单 goroutine 全包」的实现，全局临界区显著变短。
type Scheduler struct {
	opts Options

	mu      sync.Mutex
	entries map[string]*taskEntry // 任务索引表：ID -> 任务（与堆组成双索引）
	queue   taskQueue             // 最小堆：按 RunAt 排序
	closed  bool

	wake  chan struct{}   // 容量 1：打断调度循环的休眠（只发信号，不带数据）
	ready chan *taskEntry // 到期任务队列：调度循环 -> worker，缓冲即背压水位
	done  chan struct{}   // 关闭信号：调度循环与 worker 都监听它

	baseCtx    context.Context    // 所有任务 ctx 的父 ctx
	baseCancel context.CancelFunc // Close 时调用，用于打断在途任务

	loopWG   sync.WaitGroup
	workerWG sync.WaitGroup
	finished chan struct{} // 调度循环 + 全部 worker 退出后关闭

	closeOnce sync.Once

	stats stats

	subMu  sync.Mutex // 事件订阅者。独立锁，尽量不干扰调度热路径。
	subs   map[int64]chan Event
	nextID int64
}

// New 创建调度器，并立刻启动 1 个调度 goroutine 与 N 个 worker goroutine。
func New(opts ...Option) *Scheduler {
	o := defaultOptions()
	o.apply(opts)

	ctx, cancel := context.WithCancel(context.Background())

	s := &Scheduler{
		opts:       o,
		entries:    make(map[string]*taskEntry),
		wake:       make(chan struct{}, 1),
		ready:      make(chan *taskEntry, o.QueueSize),
		done:       make(chan struct{}),
		finished:   make(chan struct{}),
		baseCtx:    ctx,
		baseCancel: cancel,
		subs:       make(map[int64]chan Event),
	}
	heap.Init(&s.queue)

	s.loopWG.Add(1)
	go s.loop()

	// worker 数固定，进程内 goroutine 总量有上界。
	s.workerWG.Add(o.Workers)
	for i := 0; i < o.Workers; i++ {
		go s.worker()
	}

	return s
}

// ---------------------------------------------------------------- 对外接口

// AddTask 提交任务。可被任意多个 goroutine 并发调用。
//
// 加锁顺序：s.mu -> entry.mu；临界区内只做 map 写 + 堆插入，O(log n)。
func (s *Scheduler) AddTask(spec TaskSpec) error {
	if err := spec.validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrClosed
	}
	if _, ok := s.entries[spec.ID]; ok {
		return fmt.Errorf("%w: %s", ErrTaskExists, spec.ID)
	}

	e := newTaskEntry(spec, s.opts.Now())
	s.entries[e.id] = e
	heap.Push(&s.queue, e) // 入堆后 e.index 有效，取消时可 O(log n) 摘除
	s.stats.submitted.Add(1)

	// 新任务可能比堆顶更早到期，必须唤醒调度循环重新计算等待时间，
	// 否则新任务要等到「原堆顶到期」才会被执行。
	s.notify()

	return nil
}

// Cancel 取消任务，支持并发调用，语义：
//
//   - 待执行：从堆中摘除，直接置为已取消，任务函数不会被调用；
//   - 执行中：取消其 context（FnCtx 任务可立即退出），状态置为已取消，结果丢弃；
//   - 已取消：幂等返回 nil；
//   - 已执行/错误：返回 ErrTaskFinished。
func (s *Scheduler) Cancel(id string) error {
	s.mu.Lock()

	e, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}

	// 先出堆：若任务还在堆里，摘除后调度循环不会再投递它。
	// 与「出堆即标记执行中」在同一把锁内完成，二者不会互相插入。
	if e.index >= 0 {
		heap.Remove(&s.queue, e.index)
		e.index = -1
	}

	switch e.statusNow() {
	case StatusCancelled:
		s.mu.Unlock()
		return nil // 幂等：重复取消不报错
	case StatusExecuted, StatusFailed:
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrTaskFinished, id)
	}

	e.cancelNow() // 执行中的任务：打断其 ctx

	if s.finishEntry(e, StatusCancelled, "", ErrTaskCancelled) {
		s.mu.Unlock()
		s.notify() // 堆顶可能变化，让调度循环重算等待时间
		return nil
	}

	// 极小概率：worker 恰好在同一瞬间写入了「已执行/错误」，终态先到先得。
	st := e.statusNow()
	s.mu.Unlock()
	s.notify()

	if st == StatusCancelled {
		return nil // 另一个取消者赢了，语义上仍算取消成功
	}
	return fmt.Errorf("%w: %s", ErrTaskFinished, id)
}

// Status 查询任务状态。只取 entry 自己的锁，不碰全局锁。
func (s *Scheduler) Status(id string) (Status, error) {
	e, err := s.lookup(id)
	if err != nil {
		return StatusPending, err
	}
	return e.statusNow(), nil
}

// Result 查询任务结果快照。
func (s *Scheduler) Result(id string) (TaskResult, error) {
	e, err := s.lookup(id)
	if err != nil {
		return TaskResult{}, err
	}
	return e.snapshot(), nil
}

// Wait 阻塞直到任务进入终态，或被 ctx 打断（返回 ctx.Err()）。
// 它是 Result 的「异步转同步」版本，方便调用方等待某个关键任务。
func (s *Scheduler) Wait(ctx context.Context, id string) (TaskResult, error) {
	e, err := s.lookup(id)
	if err != nil {
		return TaskResult{}, err
	}

	select {
	case <-e.done: // 进入终态时由 finish 关闭，只会关一次
		return e.snapshot(), nil
	case <-ctx.Done():
		return e.snapshot(), ctx.Err()
	}
}

// Snapshot 返回全部任务的结果快照，按 ID 排序，便于断言与展示。
func (s *Scheduler) Snapshot() []TaskResult {
	s.mu.Lock()
	entries := make([]*taskEntry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	s.mu.Unlock()

	out := make([]TaskResult, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Len 返回已登记任务总数（含已完成的）。
func (s *Scheduler) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Closed 报告调度器是否已进入关闭流程。
func (s *Scheduler) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Stats 返回累计计数与瞬时水位。
//
// Pending/Running 需要遍历任务表（O(n)），因此它适合做监控采样，
// 而不是放进任务执行的热路径；累计计数则全是原子读，零竞争。
func (s *Scheduler) Stats() Stats {
	out := Stats{
		Submitted: s.stats.submitted.Load(),
		Executed:  s.stats.executed.Load(),
		Failed:    s.stats.failed.Load(),
		Panicked:  s.stats.panicked.Load(),
		Cancelled: s.stats.cancelled.Load(),
		Retried:   s.stats.retried.Load(),
		Dropped:   s.stats.dropped.Load(),
		Workers:   s.opts.Workers,
	}

	s.mu.Lock()
	out.Queued = len(s.queue)
	entries := make([]*taskEntry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	s.mu.Unlock()

	for _, e := range entries {
		switch e.statusNow() {
		case StatusPending:
			out.Pending++
		case StatusRunning:
			out.Running++
		}
	}
	return out
}

// Subscribe 订阅任务生命周期事件，返回事件通道与退订函数。
//
// 事件发送是「非阻塞 + 满了就丢」：观测者永远不能拖慢调度器。
// 被丢弃的事件数记录在 Stats().Dropped 中。
func (s *Scheduler) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer < 0 {
		buffer = 0
	}
	ch := make(chan Event, buffer)

	s.subMu.Lock()
	id := s.nextID
	s.nextID++
	s.subs[id] = ch
	s.subMu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.subMu.Lock()
			delete(s.subs, id) // 与 publish 的发送互斥，保证 close 后不再有发送
			s.subMu.Unlock()
			close(ch)
		})
	}
	return ch, unsubscribe
}

// Close 优雅关闭调度器：
//
//  1. 关闭入口（后续 AddTask 返回 ErrClosed），并取消堆中所有待执行任务；
//  2. 取消所有在途任务的 context，让它们尽快收尾；
//  3. 通知调度循环与 worker 退出，等待它们全部结束；
//  4. 若 ctx 先到期则返回 ctx.Err()。
//
// 重复调用是安全的；第一次调用真正生效，后续调用只等待收尾。
func (s *Scheduler) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		for _, e := range s.entries {
			e.cancelNow()
			// 未进入终态的任务（待执行 + 执行中）一律记为已取消。
			s.finishEntry(e, StatusCancelled, "", ErrClosed)
		}
		s.queue = nil // 清空堆，避免 index 悬空引用
		s.mu.Unlock()

		s.baseCancel() // 打断在途任务的 ctx
		close(s.done)  // 调度循环与 worker 从这里退出

		go func() {
			s.loopWG.Wait()
			s.workerWG.Wait()
			close(s.finished)
		}()
	})

	select {
	case <-s.finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------------------------------------------------------------- 内部实现

// Stats 是调度器的指标快照。
type Stats struct {
	Submitted uint64 // 累计提交
	Executed  uint64 // 累计成功
	Failed    uint64 // 累计失败（含超时、panic）
	Panicked  uint64 // 累计 panic 次数（按尝试计）
	Cancelled uint64 // 累计取消
	Retried   uint64 // 累计重试次数
	Dropped   uint64 // 累计被丢弃的订阅事件
	Pending   int    // 当前待执行
	Running   int    // 当前执行中
	Queued    int    // 当前堆内任务数
	Workers   int    // worker 并发上限
}

type stats struct {
	submitted atomic.Uint64
	executed  atomic.Uint64
	failed    atomic.Uint64
	panicked  atomic.Uint64
	cancelled atomic.Uint64
	retried   atomic.Uint64
	dropped   atomic.Uint64
}

func (s *Scheduler) lookup(id string) (*taskEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return e, nil
}

// notify 非阻塞地唤醒调度循环。容量 1 的 channel 天然做了信号合并：
// 连续提交 1 万个任务也只唤醒一次，调度循环醒来后一次性看到全部新任务。
func (s *Scheduler) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// finishEntry 是唯一的终态写入入口：写状态、计数、发事件。
// 依赖 taskEntry.finish 的「先到先得」语义保证统计与事件不重复。
func (s *Scheduler) finishEntry(e *taskEntry, status Status, result string, err error) bool {
	changed, attempts := e.finish(status, result, err, s.opts.Now())
	if !changed {
		return false
	}

	switch status {
	case StatusExecuted:
		s.stats.executed.Add(1)
	case StatusFailed:
		s.stats.failed.Add(1)
	case StatusCancelled:
		s.stats.cancelled.Add(1)
	}

	s.publish(Event{
		TaskID:   e.id,
		Status:   status,
		Result:   result,
		Err:      err,
		Attempts: attempts,
		At:       s.opts.Now(),
	})
	return true
}

func (s *Scheduler) publish(ev Event) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	for _, ch := range s.subs {
		select {
		case ch <- ev:
		default:
			s.stats.dropped.Add(1)
		}
	}
}

// loop 是唯一的调度 goroutine：找出最早到期的任务，等到期后投递给 worker。
//
// 关键点：
//   - 用「可复用的 Timer」而不是轮询 ticker，任务不会早执行也不会晚太多；
//   - 等待期间随时可被 wake（新任务/取消）或 done（关闭）打断。
func (s *Scheduler) loop() {
	defer s.loopWG.Done()

	timer := time.NewTimer(time.Hour)
	stopTimer(timer)
	defer timer.Stop()

	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}

		if s.queue.Len() == 0 {
			s.mu.Unlock()
			select {
			case <-s.wake:
			case <-s.done:
				return
			}
			continue
		}

		next := s.queue[0]
		wait := next.runAt.Sub(s.opts.Now())
		if wait > 0 {
			s.mu.Unlock()

			timer.Reset(wait)
			select {
			case <-timer.C:
			case <-s.wake:
				// 有新任务/取消，立即重算等待时间，绝不盲目睡满 wait。
				stopTimer(timer)
			case <-s.done:
				stopTimer(timer)
				return
			}
			continue
		}

		// 已到期：出堆 + 标记执行中，两步在同一临界区内原子完成。
		heap.Pop(&s.queue)
		if !next.markRunning(s.opts.Now()) {
			// 出堆与被取消竞争时，取消方已经赢得终态，直接跳过。
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()

		// 投递给 worker。队列满时这里阻塞，形成背压：
		// 堆里的任务不会被丢弃，只是推迟到有空闲 worker 再投递。
		select {
		case s.ready <- next:
		case <-s.done:
			s.finishEntry(next, StatusCancelled, "", ErrClosed)
			return
		}
	}
}

// worker 是任务执行者：从 ready 队列领取任务并执行。
// worker 数量就是并发上限，任务再多也不会无限开 goroutine。
func (s *Scheduler) worker() {
	defer s.workerWG.Done()

	for {
		select {
		case <-s.done:
			return
		case e := <-s.ready:
			s.runEntry(e)
		}
	}
}

// runEntry 执行一个任务，包含「单次尝试 + 失败重试」的完整闭环。
func (s *Scheduler) runEntry(e *taskEntry) {
	for {
		attempt := e.nextAttempt()
		if attempt == 0 {
			return // 投递后、执行前被取消
		}

		ctx, cancel := s.newTaskContext(e)
		e.setCancel(cancel)

		result, err := s.invoke(ctx, e)

		cancel()
		e.clearCancel()

		if err == nil {
			s.finishEntry(e, StatusExecuted, result, nil)
			return
		}

		err = normalizeTaskError(err)

		var pe *PanicError
		if errors.As(err, &pe) {
			s.stats.panicked.Add(1)
		}
		s.logTaskError(e, attempt, err)

		if errors.Is(err, ErrTaskCancelled) {
			// 用户取消 / 调度器关闭：状态多半已是终态，这里只做兜底。
			s.finishEntry(e, StatusCancelled, "", ErrTaskCancelled)
			return
		}

		if attempt < e.attemptsLimit {
			s.stats.retried.Add(1)
			if !s.sleep(e.backoff) {
				return // 调度器已关闭
			}
			continue
		}

		s.finishEntry(e, StatusFailed, "", err)
		return
	}
}

// newTaskContext 为一次尝试创建 ctx：以调度器 baseCtx 为父，
// 因此 Close 时所有在途任务都会被一起取消。
func (s *Scheduler) newTaskContext(e *taskEntry) (context.Context, context.CancelFunc) {
	if e.timeout > 0 {
		return context.WithTimeout(s.baseCtx, e.timeout)
	}
	return context.WithCancel(s.baseCtx)
}

// invoke 在独立 goroutine 中执行任务函数，并与 ctx 结束做竞速。
//
// 为什么要再开一个 goroutine：任务函数签名固定为 func() (string, error)，
// 无法强制中断。放进独立 goroutine 后，即使函数不响应 ctx，worker 也能在
// 超时/取消时立刻返回，不会被一个卡死的任务永久占住。
// 代价是函数本身仍在后台运行，直到它自己返回（见文档「已知限制」）。
func (s *Scheduler) invoke(ctx context.Context, e *taskEntry) (string, error) {
	type outcome struct {
		result string
		err    error
	}

	ch := make(chan outcome, 1) // 缓冲 1：发送端永不阻塞，不会泄漏 goroutine

	go func() {
		defer func() {
			// panic 隔离：任务栈的崩溃在这里被拦住，转成普通 error。
			if r := recover(); r != nil {
				ch <- outcome{err: newPanicError(r)}
			}
		}()

		if e.fnCtx != nil {
			out, err := e.fnCtx(ctx)
			ch <- outcome{result: out, err: err}
			return
		}
		out, err := e.fn()
		ch <- outcome{result: out, err: err}
	}()

	select {
	case o := <-ch:
		return o.result, o.err
	case <-ctx.Done():
		return "", contextError(ctx)
	}
}

func (s *Scheduler) sleep(d time.Duration) bool {
	if d <= 0 {
		return s.baseCtx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
		return true
	case <-s.baseCtx.Done():
		return false
	}
}

func (s *Scheduler) logTaskError(e *taskEntry, attempt int, err error) {
	var pe *PanicError
	if errors.As(err, &pe) {
		s.opts.Logger.Error("task panicked",
			"task_id", e.id,
			"attempt", attempt,
			"error", err,
			"stack", string(pe.Stack),
		)
		return
	}
	s.opts.Logger.Warn("task failed",
		"task_id", e.id,
		"attempt", attempt,
		"error", err,
	)
}

// stopTimer 停止定时器并排空已触发的信号，保证后续 Reset 得到干净的定时器。
func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}
