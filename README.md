# task-pool

一个高性能、功能丰富的 Go 语言协程池（Goroutine Pool）库，支持动态调整大小、结果订阅、优雅关闭等特性。

## ✨ 特性

- 🚀 **高性能**: 基于 channel 实现，支持高并发任务提交
- 🔄 **动态调整**: 运行时动态增加或减少 worker 数量
- 📡 **结果订阅**: 通过订阅机制异步接收任务执行结果
- 🛡️ **安全可靠**: 自动捕获 panic，提供完整的错误信息（包含堆栈）
- 🎯 **优雅关闭**: 关闭时会尝试处理队列中的剩余任务
- ⏱️ **执行统计**: 自动记录任务执行时长
- 🔌 **Context 支持**: 支持 context 取消和超时控制
- 🔒 **并发安全**: 所有操作都是并发安全的

## 📦 安装

```bash
go get github.com/itart-top/task-pool
```

## 🚀 快速开始

### 基本使用

```go
package main

import (
    "context"
    "fmt"
    "time"
    
    "github.com/itart-top/task-pool"
)

func main() {
    // 创建协程池：10 个 worker，任务队列缓冲 100
    p := pool.New(10, 100)
    defer p.Close()
    
    // 提交任务
    err := p.Submit(context.Background(), "task-1", func(ctx context.Context) (any, error) {
        // 执行你的任务逻辑
        time.Sleep(100 * time.Millisecond)
        return "result", nil
    })
    
    if err != nil {
        fmt.Printf("提交任务失败: %v\n", err)
    }
}
```

### 结果订阅

```go
p := pool.New(10, 100)
defer p.Close()

// 订阅任务执行结果
p.Subscribe(func(ctx context.Context, result pool.TaskResult) {
    if result.Err != nil {
        fmt.Printf("任务 %s 执行失败: %v\n", result.TaskID, result.Err)
    } else {
        fmt.Printf("任务 %s 执行成功: %v, 耗时: %v\n", 
            result.TaskID, result.Data, result.Duration)
    }
})

// 提交任务
p.Submit(context.Background(), "task-1", func(ctx context.Context) (any, error) {
    return "hello", nil
})
```

### 动态调整 Worker 数量

```go
p := pool.New(5, 100)
defer p.Close()

// 增加到 20 个 worker
p.Resize(20)

// 减少到 10 个 worker
p.Resize(10)
```

### 完整示例

```go
package main

import (
    "context"
    "fmt"
    "sync"
    "time"
    
    "github.com/itart-top/task-pool"
)

func main() {
    // 创建协程池
    p := pool.New(5, 100)
    defer p.Close()
    
    var wg sync.WaitGroup
    var mu sync.Mutex
    results := make(map[string]pool.TaskResult)
    
    // 订阅结果
    p.Subscribe(func(ctx context.Context, result pool.TaskResult) {
        mu.Lock()
        defer mu.Unlock()
        results[result.TaskID] = result
        wg.Done()
    })
    
    // 提交多个任务
    for i := 0; i < 10; i++ {
        taskID := fmt.Sprintf("task-%d", i)
        wg.Add(1)
        
        p.Submit(context.Background(), taskID, func(ctx context.Context) (any, error) {
            // 模拟任务执行
            time.Sleep(50 * time.Millisecond)
            return fmt.Sprintf("result-%d", i), nil
        })
    }
    
    // 等待所有任务完成
    wg.Wait()
    
    // 打印结果
    for taskID, result := range results {
        fmt.Printf("%s: %v (耗时: %v)\n", taskID, result.Data, result.Duration)
    }
}
```

## 📚 API 文档

### New(workerCnt, taskBuf int) *Pool

创建新的协程池。

- `workerCnt`: 初始 worker 数量（>=1，如果 <=0 则默认为 1）
- `taskBuf`: 任务 channel 缓冲大小（>=1，如果 <=0 则默认为 100）

返回: `*Pool` 实例

### Submit(ctx context.Context, taskID string, task TaskFunc) error

提交任务到池中。

- `ctx`: 上下文，用于取消控制
- `taskID`: 任务唯一标识
- `task`: 任务函数，类型为 `func(ctx context.Context) (any, error)`

返回: 错误信息（如果池已关闭或上下文被取消）

**注意**: 此方法会阻塞直到任务被放入队列、ctx 取消或 pool 关闭。

### Subscribe(sub ResultSubscriber)

注册结果订阅者。当任务执行完成时，会调用订阅者函数。

- `sub`: 订阅者函数，类型为 `func(result TaskResult)`
- 如果传入 `nil`，则取消订阅

### Resize(n int)

动态调整 worker 数量。

- `n`: 目标 worker 数量（>=1，如果 <=0 则默认为 1）
- 如果池已关闭，此操作不会生效
- 只保留最近一次调整请求（会丢弃旧的未处理请求）

### Close()

安全关闭协程池。

- 标记池为关闭状态
- 取消所有 worker 的 context
- 等待所有正在执行的任务完成（会尝试处理队列中的剩余任务）
- 关闭后，`Submit` 会立即返回错误

## 🎯 使用场景

- **批量任务处理**: 需要并发执行大量独立任务
- **资源控制**: 限制并发数量，避免资源耗尽
- **异步任务队列**: 解耦任务提交和执行
- **结果收集**: 通过订阅机制统一处理任务结果
- **动态负载**: 根据负载情况动态调整 worker 数量

## ⚠️ 注意事项

1. **任务函数**: 任务函数应该检查 `ctx.Done()` 以支持取消操作
2. **Panic 处理**: 任务中的 panic 会被自动捕获，并作为错误返回
3. **关闭顺序**: 建议使用 `defer p.Close()` 确保资源正确释放
4. **结果订阅**: 订阅者函数应该快速返回，避免阻塞 worker
5. **并发安全**: 所有方法都是并发安全的，可以在多个 goroutine 中调用

## 🧪 测试

运行测试：

```bash
go test -v
```

运行性能测试：

```bash
go test -bench=. -benchmem
```

## 📊 性能

项目包含完整的性能测试，包括：
- 任务提交性能
- 并发提交性能
- 任务执行性能
- 动态调整性能
- 带工作负载的性能测试

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📄 License

本项目采用 MIT License。

