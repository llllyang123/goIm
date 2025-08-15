package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 存储在线用户：用户名 -> 连接
var (
	users     sync.Map
	userCount int64 // 原子计数器，跟踪在线用户数量
)

// 检查消息是否有效
// 如果消息仅包含空格且长度小于2，则返回false
func isValidMessage(msg string) bool {
	// 去除所有空格后检查
	trimmed := strings.TrimSpace(msg)
	if len(trimmed) == 0 && len(msg) < 2 {
		return false
	}
	return true
}

// 处理单个客户端连接
func handleClient(conn net.Conn) {
	defer conn.Close()

	// 对TCP连接设置选项
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
		tcpConn.SetNoDelay(true)
	}

	// 读取客户端发送的用户名
	reader := bufio.NewReaderSize(conn, 4096) // 增大缓冲区到4KB
	username, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("读取用户名失败：", err)
		return
	}
	username = strings.TrimSpace(username)

	// 注册用户，使用原子操作更新计数器
	users.Store(username, conn)
	atomic.AddInt64(&userCount, 1)
	currentCount := atomic.LoadInt64(&userCount)
	fmt.Printf("用户 %s 上线，当前在线：%d 人\n", username, currentCount)
	conn.Write([]byte("欢迎加入聊天室！\n"))

	// 确保用户下线时清理资源
	defer func() {
		users.Delete(username)
		atomic.AddInt64(&userCount, -1)
		fmt.Printf("用户 %s 下线，当前在线：%d 人\n", username, atomic.LoadInt64(&userCount))
	}()

	// 循环读取客户端客户端消息并转发
	for {
		msg, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("读取用户 %s 消息失败：%v\n", username, err)
			return
		}
		msg = strings.TrimSpace(msg)

		// 验证消息是否有效
		if !isValidMessage(msg) {
			conn.Write([]byte("消息无效：不能发送仅包含空格或长度小于2的消息\n"))
			continue
		}

		fmt.Printf("[%s] 说：%s\n", username, msg)

		// 转发消息给所有在线用户
		// 使用Range遍历sync.Map，无需加锁
		users.Range(func(key, value interface{}) bool {
			name := key.(string)
			c := value.(net.Conn)

			if name != username { // 不发给自己
				// 使用非阻塞写入，避免单个连接阻塞整体转发
				go func(conn net.Conn, data string) {
					_, err := conn.Write([]byte(data))
					if err != nil {
						fmt.Printf("向用户 %s 发送消息失败：%v\n", name, err)
					}
				}(c, fmt.Sprintf("[%s]：%s\n", username, msg))
			}
			return true // 继续遍历所有用户
		})
	}
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU()) // 利用全部CPU核心

	// 监听端口
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		fmt.Println("监听失败：", err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Println("服务器启动，监听端口 8080...")

	// 限制最大并发连接数，防止资源耗尽
	maxConnections := 10000
	connSem := make(chan struct{}, maxConnections)

	// 循环接受客户端连接
	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("接受连接失败：", err)
			continue
		}

		connSem <- struct{}{} // 获取连接信号量
		go func() {
			defer func() { <-connSem }() // 释放信号量
			handleClient(conn)
		}()
	}
}
