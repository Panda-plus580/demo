package scheduler

// taskQueue 是按 RunAt 升序的最小堆（container/heap 接口实现）。
//
// 为什么用最小堆而不是遍历切片找最早任务：
// 堆顶永远是「最早到期」的任务，入堆/出堆都是 O(log n)，
// 适合几万甚至几十万任务同时挂起的场景。
//
// 每个元素自带 index 字段，与 heap.Remove 配合即可做到「按 ID 取消任意任务」，
// 复杂度同样是 O(log n) —— 这就是下面提到的 Map + Heap 双索引方案。
type taskQueue []*taskEntry

func (q taskQueue) Len() int { return len(q) }

func (q taskQueue) Less(i, j int) bool {
	if !q[i].runAt.Equal(q[j].runAt) {
		return q[i].runAt.Before(q[j].runAt)
	}
	// 同一时刻按 ID 兜底排序，让调度顺序确定、可测试、可复现。
	return q[i].id < q[j].id
}

func (q taskQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}

func (q *taskQueue) Push(x any) {
	e := x.(*taskEntry)
	e.index = len(*q)
	*q = append(*q, e)
}

func (q *taskQueue) Pop() any {
	old := *q
	n := len(old)
	e := old[n-1]
	old[n-1] = nil // 避免残留引用，帮助 GC
	e.index = -1
	*q = old[:n-1]
	return e
}
