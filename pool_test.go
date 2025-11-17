package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNew 测试创建协程池
func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		size     int
		expected int
	}{
		{"正常大小", 10, 10},
		{"零值", 0, 1},
		{"负值", -1, 1},
		{"单worker", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(tt.size, 100)
			if p == nil {
				t.Fatal("New() returned nil")
			}
			if p.workerCount != tt.expected {
				t.Errorf("workerCount = %d, want %d", p.workerCount, tt.expected)
			}
			if p.closed.Load() {
				t.Error("pool should not be closed")
			}
			p.Close()
		})
	}
}

// TestSubmit 测试提交任务
func TestSubmit(t *testing.T) {
	p := New(2, 100)
	defer p.Close()

	// 测试正常提交
	err := p.Submit(context.Background(), "test-task-1", func(ctx context.Context) (any, error) {
		return "success", nil
	})
	if err != nil {
		t.Errorf("Submit() error = %v, want nil", err)
	}

	// 测试关闭后提交
	p.Close()
	err = p.Submit(context.Background(), "test-task-2", func(ctx context.Context) (any, error) {
		return nil, nil
	})
	if err == nil {
		t.Error("Submit() should return error when pool is closed")
	}
}

// TestSubscribe 测试结果订阅
func TestSubscribe(t *testing.T) {
	p := New(2, 100)
	defer p.Close()

	var results []TaskResult
	var mu sync.Mutex

	p.Subscribe(func(result TaskResult) {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, result)
	})

	// 提交成功任务
	p.Submit(context.Background(), "test-task-1", func(ctx context.Context) (any, error) {
		return "test", nil
	})

	// 提交失败任务
	p.Submit(context.Background(), "test-task-2", func(ctx context.Context) (any, error) {
		return nil, errors.New("test error")
	})

	// 等待任务执行完成
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	if results[0].Data != "test" || results[0].Err != nil {
		t.Errorf("first result incorrect: %+v", results[0])
	}
	if results[1].Err == nil {
		t.Error("second result should have error")
	}
	mu.Unlock()
}

// TestResize 测试动态调整大小
func TestResize(t *testing.T) {
	p := New(2, 100)
	defer p.Close()

	if p.workerCount != 2 {
		t.Errorf("initial workerCount = %d, want 2", p.workerCount)
	}

	// 增加worker
	p.Resize(5)
	time.Sleep(100 * time.Millisecond)
	if p.workerCount != 5 {
		t.Errorf("after resize to 5, workerCount = %d, want 5", p.workerCount)
	}

	// 减少worker
	p.Resize(3)
	time.Sleep(100 * time.Millisecond)
	if p.workerCount != 3 {
		t.Errorf("after resize to 3, workerCount = %d, want 3", p.workerCount)
	}

	// 测试零值和负值
	p.Resize(0)
	time.Sleep(100 * time.Millisecond)
	if p.workerCount != 1 {
		t.Errorf("after resize to 0, workerCount = %d, want 1", p.workerCount)
	}

	// 测试关闭后调整
	p.Close()
	p.Resize(10)
	// 关闭后应该不生效
}

// TestClose 测试关闭池
func TestClose(t *testing.T) {
	p := New(3, 100)

	// 提交一些任务
	for i := 0; i < 10; i++ {
		p.Submit(context.Background(), fmt.Sprintf("task-%d", i), func(ctx context.Context) (any, error) {
			time.Sleep(10 * time.Millisecond)
			return i, nil
		})
	}

	// 关闭池
	done := make(chan struct{})
	go func() {
		p.Close()
		close(done)
	}()

	// 等待关闭完成
	select {
	case <-done:
		// 正常关闭
	case <-time.After(2 * time.Second):
		t.Fatal("Close() timeout")
	}

	// 验证关闭状态
	if !p.closed.Load() {
		t.Error("pool should be closed")
	}
}

// TestConcurrentSubmit 测试并发提交
func TestConcurrentSubmit(t *testing.T) {
	p := New(5, 100)
	defer p.Close()

	var successCount atomic.Int64
	var errorCount atomic.Int64

	var wg sync.WaitGroup
	numTasks := 100

	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := p.Submit(context.Background(), fmt.Sprintf("task-%d", id), func(ctx context.Context) (any, error) {
				return id, nil
			})
			if err != nil {
				errorCount.Add(1)
			} else {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	if successCount.Load() == 0 {
		t.Error("no tasks submitted successfully")
	}
	t.Logf("Success: %d, Errors: %d", successCount.Load(), errorCount.Load())
}

// TestTaskPanic 测试任务panic处理
func TestTaskPanic(t *testing.T) {
	p := New(2, 100)
	defer p.Close()

	var results []TaskResult
	var mu sync.Mutex

	p.Subscribe(func(result TaskResult) {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, result)
	})

	// 提交会panic的任务
	p.Submit(context.Background(), "panic-task", func(ctx context.Context) (any, error) {
		panic("test panic")
	})

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Error("expected error for panic")
	}
	mu.Unlock()
}

// TestContextCancellation 测试上下文取消
func TestContextCancellation(t *testing.T) {
	p := New(2, 100)
	defer p.Close()

	// 提交长时间运行的任务
	p.Submit(context.Background(), "long-task", func(ctx context.Context) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
			return "done", nil
		}
	})

	// 立即调整大小，触发worker取消
	p.Resize(1)
	time.Sleep(100 * time.Millisecond)

	// 关闭池
	p.Close()
	time.Sleep(100 * time.Millisecond)
}

// TestFlushTasksOnClose 测试关闭时清空任务
func TestFlushTasksOnClose(t *testing.T) {
	p := New(2, 100)

	var executedCount atomic.Int64

	// 提交大量任务
	for i := 0; i < 50; i++ {
		p.Submit(context.Background(), fmt.Sprintf("task-%d", i), func(ctx context.Context) (any, error) {
			executedCount.Add(1)
			time.Sleep(10 * time.Millisecond)
			return nil, nil
		})
	}

	// 立即关闭
	p.Close()

	// 等待一段时间，检查是否有任务执行
	time.Sleep(500 * time.Millisecond)
	executed := executedCount.Load()
	t.Logf("Executed %d tasks before close", executed)
}

// BenchmarkSubmit 性能测试：提交任务
func BenchmarkSubmit(b *testing.B) {
	p := New(10, 100)
	defer p.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Submit(context.Background(), fmt.Sprintf("task-%d", i), func(ctx context.Context) (any, error) {
			return i, nil
		})
	}
}

// BenchmarkSubmitConcurrent 性能测试：并发提交
func BenchmarkSubmitConcurrent(b *testing.B) {
	p := New(10, 100)
	defer p.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		counter := int64(0)
		for pb.Next() {
			id := atomic.AddInt64(&counter, 1)
			p.Submit(context.Background(), fmt.Sprintf("task-%d", id), func(ctx context.Context) (any, error) {
				return nil, nil
			})
		}
	})
}

// BenchmarkTaskExecution 性能测试：任务执行
func BenchmarkTaskExecution(b *testing.B) {
	p := New(10, 100)
	defer p.Close()

	done := make(chan struct{})
	var count atomic.Int64

	p.Subscribe(func(result TaskResult) {
		if count.Add(1) == int64(b.N) {
			close(done)
		}
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Submit(context.Background(), fmt.Sprintf("task-%d", i), func(ctx context.Context) (any, error) {
			return i, nil
		})
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		b.Fatal("timeout waiting for tasks")
	}
}

// BenchmarkResize 性能测试：动态调整大小
func BenchmarkResize(b *testing.B) {
	p := New(5, 100)
	defer p.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Resize(5 + (i % 10))
	}
}

// BenchmarkPoolWithWorkload 性能测试：带工作负载
func BenchmarkPoolWithWorkload(b *testing.B) {
	p := New(20, 100)
	defer p.Close()

	var wg sync.WaitGroup
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.Submit(context.Background(), fmt.Sprintf("task-%d", id), func(ctx context.Context) (any, error) {
				// 模拟一些工作
				time.Sleep(1 * time.Millisecond)
				return nil, nil
			})
		}(i)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)
}
