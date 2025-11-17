package pool

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// TaskFunc 定义带返回值的任务函数
type TaskFunc func(ctx context.Context) (any, error)
type ResultSubscriber func(result TaskResult)

type Entry struct {
	Ctx    context.Context
	TaskID string
	Task   TaskFunc
}

type TaskResult struct {
	TaskID   string
	Data     any
	Err      error
	Duration time.Duration
}

type Pool struct {
	tasks       chan Entry
	resizeReq   chan int
	closeReq    chan struct{}
	closed      atomic.Bool
	wg          sync.WaitGroup
	cancelFuncs []context.CancelFunc // 仅由 controlLoop goroutine 访问
	workerCount int                  // 仅由 controlLoop goroutine 访问

	// subscriber 使用 atomic.Value 以避免 data race
	subscriber atomic.Value // stores ResultSubscriber
}

// New 创建协程池。
// workerCnt: 初始 worker 数量（>=1）
// taskBuf: task channel 缓冲大小（>=1）
func New(workerCnt, taskBuf int) *Pool {
	if workerCnt <= 0 {
		workerCnt = 1
	}
	if taskBuf <= 0 {
		taskBuf = 100
	}
	p := &Pool{
		tasks:       make(chan Entry, taskBuf),
		resizeReq:   make(chan int, 1),
		closeReq:    make(chan struct{}),
		workerCount: workerCnt,
	}
	p.cancelFuncs = make([]context.CancelFunc, 0, workerCnt)
	go p.controlLoop()
	return p
}

// Submit 提交任务：会阻塞直到任务被放入队列、ctx 取消或 pool 关闭
func (p *Pool) Submit(ctx context.Context, taskID string, task TaskFunc) error {
	if p.closed.Load() {
		return errors.New("pool is closed")
	}
	entry := Entry{
		Ctx:    ctx,
		TaskID: taskID,
		Task:   task,
	}
	select {
	case <-p.closeReq:
		return errors.New("pool is closed")
	case <-ctx.Done():
		return ctx.Err()
	case p.tasks <- entry:
		return nil
	}
}

// Subscribe 注册结果订阅者（并发安全）
func (p *Pool) Subscribe(sub ResultSubscriber) {
	if sub == nil {
		p.subscriber.Store(nil)
		return
	}
	p.subscriber.Store(sub)
}

// Resize 请求修改 worker 数量（只保留最近一次请求）
func (p *Pool) Resize(n int) {
	if n <= 0 {
		n = 1
	}
	if p.closed.Load() {
		return
	}
	// 丢弃旧请求，只保留最新
	select {
	case <-p.resizeReq:
	default:
	}
	select {
	case p.resizeReq <- n:
	default:
	}
}

// Close 安全关闭：标记关闭 -> 通知 controlLoop -> 等待所有 worker 退出。
// 语义：Close 返回前保证所有正在执行的任务已结束（pool 会尝试让 worker flush），但是 Submit 会立即失败。
func (p *Pool) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	// wake up controlLoop
	close(p.closeReq)
	// wait all workers done
	p.wg.Wait()
}

// ------------------ internal ------------------

func (p *Pool) controlLoop() {
	// 启动初始 workers
	p.startWorkers(p.workerCount)
	for {
		select {
		case n := <-p.resizeReq:
			p.doResize(n)
		case <-p.closeReq:
			// cancel all workers; since closed == true (Close already set),
			// worker will enter flush mode and try to drain remaining tasks.
			for _, cancel := range p.cancelFuncs {
				cancel()
			}
			// drop references
			p.cancelFuncs = nil
			return
		}
	}
}

func (p *Pool) doResize(target int) {
	diff := target - p.workerCount
	p.workerCount = target
	if diff > 0 {
		p.startWorkers(diff)
	} else if diff < 0 {
		p.stopWorkers(-diff)
	}
}

func (p *Pool) startWorkers(n int) {
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		p.cancelFuncs = append(p.cancelFuncs, cancel)
		p.wg.Add(1)
		go p.worker(ctx)
	}
}

func (p *Pool) stopWorkers(n int) {
	if n > len(p.cancelFuncs) {
		n = len(p.cancelFuncs)
	}
	toCancel := p.cancelFuncs[:n]
	p.cancelFuncs = p.cancelFuncs[n:]
	for _, cancel := range toCancel {
		cancel()
	}
}

func (p *Pool) worker(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			// 闭包判断 closed：如果是 pool Close（closed==true），则尝试 flush 剩余队列（逐条处理）；
			// 否则（例如 Resize stop worker），直接退出，不处理队列。
			if p.closed.Load() {
				goto flushTask
			}
			return
		case task, ok := <-p.tasks:
			if !ok {
				return
			}
			p.invoke(task)
		}
	}

flushTask:
	for {
		select {
		case task, ok := <-p.tasks:
			if !ok {
				return
			}
			p.invoke(task)
		default:
			return
		}
	}
}

func (p *Pool) invoke(task Entry) {
	start := time.Now()
	var data any
	var err error

	defer func() {
		if r := recover(); r != nil {
			// 捕获 panic 并把 stack 放到 Err（便于排查）
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
		// 取 subscriber 并回调（并发安全）
		if v := p.subscriber.Load(); v != nil {
			if sub, ok := v.(ResultSubscriber); ok && sub != nil {
				sub(TaskResult{
					TaskID:   task.TaskID,
					Data:     data,
					Err:      err,
					Duration: time.Since(start),
				})
			}
		}
	}()

	data, err = task.Task(task.Ctx)
}
