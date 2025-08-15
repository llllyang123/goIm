package main

import (
	"flag"
	"fmt"
	"net"
	"sync"
	"time"
)

// 压测配置参数
type Config struct {
	ServerAddr      string
	Concurrency     int // 并发客户端数量
	MessagesPerConn int // 每个连接发送的消息数
	MessageSize     int // 每条消息的字节数
	Timeout         int // 超时时间(秒)
}

// 压测结果
type Result struct {
	TotalConnections int
	TotalMessages    int
	SuccessMessages  int
	FailedMessages   int
	StartTime        time.Time
	EndTime          time.Duration
}

func main() {
	// 解析命令行参数
	cfg := parseFlags()

	// 显示测试配置
	fmt.Printf("开始IM系统压测...\n")
	fmt.Printf("服务器地址: %s\n", cfg.ServerAddr)
	fmt.Printf("并发客户端数: %d\n", cfg.Concurrency)
	fmt.Printf("每个客户端发送消息数: %d\n", cfg.MessagesPerConn)
	fmt.Printf("每条消息大小: %d字节\n", cfg.MessageSize)
	fmt.Printf("超时时间: %d秒\n", cfg.Timeout)

	// 准备测试消息
	testMessage := generateTestMessage(cfg.MessageSize)

	// 执行压测
	result := runBenchmark(cfg, testMessage)

	// 输出测试结果
	printResult(result)
}

// 解析命令行参数
func parseFlags() Config {
	serverAddr := flag.String("addr", "localhost:8080", "服务器地址")
	concurrency := flag.Int("c", 100, "并发客户端数量")
	messagesPerConn := flag.Int("n", 100, "每个连接发送的消息数")
	messageSize := flag.Int("s", 64, "每条消息的字节数")
	timeout := flag.Int("t", 60, "超时时间(秒)")

	flag.Parse()

	return Config{
		ServerAddr:      *serverAddr,
		Concurrency:     *concurrency,
		MessagesPerConn: *messagesPerConn,
		MessageSize:     *messageSize,
		Timeout:         *timeout,
	}
}

// 生成测试消息
func generateTestMessage(size int) string {
	if size <= 0 {
		return "test"
	}

	buf := make([]byte, size)
	for i := 0; i < size; i++ {
		buf[i] = byte('a' + (i % 26))
	}
	return string(buf)
}

// 执行压测
func runBenchmark(cfg Config, message string) Result {
	var wg sync.WaitGroup
	result := Result{
		TotalConnections: cfg.Concurrency,
		TotalMessages:    cfg.Concurrency * cfg.MessagesPerConn,
		StartTime:        time.Now(),
	}

	// 限制同时启动的goroutine数量，避免瞬间资源耗尽
	sem := make(chan struct{}, cfg.Concurrency)

	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		sem <- struct{}{}

		go func(clientID int) {
			defer wg.Done()
			defer func() { <-sem }()

			// 连接服务器
			conn, err := net.Dial("tcp", cfg.ServerAddr)
			if err != nil {
				fmt.Printf("客户端%d连接失败: %v\n", clientID, err)
				return
			}
			defer conn.Close()

			// 设置超时
			conn.SetDeadline(time.Now().Add(time.Duration(cfg.Timeout) * time.Second))

			// 发送用户名
			username := fmt.Sprintf("benchmark_client_%d", clientID)
			_, err = conn.Write([]byte(username + "\n"))
			if err != nil {
				fmt.Printf("客户端%d发送用户名失败: %v\n", clientID, err)
				return
			}

			// 读取欢迎消息(防止阻塞)
			buf := make([]byte, 1024)
			conn.Read(buf)

			// 发送消息
			success := 0
			failed := 0
			for j := 0; j < cfg.MessagesPerConn; j++ {
				_, err := conn.Write([]byte(message + "\n"))
				if err != nil {
					//fmt.Printf("客户端%d发送消息失败: %v\n", clientID, err)
					failed++
					break // 如果一次失败，后续消息不再发送
				}
				success++

				// 可以添加微小延迟模拟真实场景
				// time.Sleep(time.Microsecond * 10)
			}

			// 更新结果
			result.SuccessMessages += success
			result.FailedMessages += failed

			if clientID%100 == 0 {
				fmt.Printf("客户端%d完成，成功发送%d条消息\n", clientID, success)
			}
		}(i)
	}

	wg.Wait()
	result.EndTime = time.Since(result.StartTime)

	return result
}

// 打印测试结果
func printResult(result Result) {
	fmt.Println("\n===== 压测结果 =====")
	fmt.Printf("总连接数: %d\n", result.TotalConnections)
	fmt.Printf("总消息数: %d\n", result.TotalMessages)
	fmt.Printf("成功消息数: %d (%.2f%%)\n",
		result.SuccessMessages,
		float64(result.SuccessMessages)/float64(result.TotalMessages)*100)
	fmt.Printf("失败消息数: %d (%.2f%%)\n",
		result.FailedMessages,
		float64(result.FailedMessages)/float64(result.TotalMessages)*100)
	fmt.Printf("总耗时: %v\n", result.EndTime)
	fmt.Printf("消息吞吐量: %.2f 条/秒\n",
		float64(result.SuccessMessages)/result.EndTime.Seconds())
	fmt.Println("====================")
}
