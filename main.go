package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
)

// 存储在线用户：用户名 -> 连接
var (
	users = make(map[string]net.Conn)
	mu    sync.Mutex // 保护 users 并发安全
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

	// 读取客户端发送的用户名
	reader := bufio.NewReader(conn)
	username, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("读取用户名失败：", err)
		return
	}
	username = strings.TrimSpace(username)

	// 注册用户
	mu.Lock()
	users[username] = conn
	mu.Unlock()
	fmt.Printf("用户 %s 上线，当前在线：%d 人\n", username, len(users))
	conn.Write([]byte("欢迎加入聊天室！\n"))

	// 循环读取客户端消息并转发
	for {
		msg, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("用户 %s 下线：%v\n", username, err)
			// 移除离线用户
			mu.Lock()
			delete(users, username)
			mu.Unlock()
			return
		}
		msg = strings.TrimSpace(msg)

		// 验证消息是否有效
		if !isValidMessage(msg) {
			conn.Write([]byte("消息无效：不能发送仅或长度小于2的空白消息\n"))
			continue
		}

		fmt.Printf("[%s] 说：%s\n", username, msg)

		// 转发消息给所有在线用户
		mu.Lock()
		for name, c := range users {
			if name != username { // 不发给自己
				c.Write([]byte(fmt.Sprintf("[%s]：%s\n", username, msg)))
			}
		}
		mu.Unlock()
	}
}

func main() {
	// 监听端口
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		fmt.Println("监听失败：", err)
		os.Exit(1)
	}
	defer listener.Close()
	fmt.Println("服务器启动，监听端口 8080...")

	// 循环接受客户端连接
	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("接受连接失败：", err)
			continue
		}
		// 启动协程处理客户端
		go handleClient(conn)
	}
}
