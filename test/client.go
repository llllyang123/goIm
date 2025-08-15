package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

// 读取服务器发送的消息并打印
func readServerMsg(conn net.Conn) {
	reader := bufio.NewReader(conn)
	for {
		msg, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("与服务器断开连接：", err)
			os.Exit(1)
		}
		fmt.Print(msg)
	}
}

func main() {
	// 连接服务器
	conn, err := net.Dial("tcp", "localhost:8080")
	if err != nil {
		fmt.Println("连接服务器失败：", err)
		os.Exit(1)
	}
	defer conn.Close()

	// 输入用户名
	fmt.Print("请输入用户名：")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	username := scanner.Text()
	username = strings.TrimSpace(username)
	// 发送用户名给服务器
	conn.Write([]byte(username + "\n"))

	// 启动协程读取服务器消息
	go readServerMsg(conn)

	// 输入消息并发送
	fmt.Println("请输入消息（直接回车发送，退出请按 Ctrl+C）：")
	for {
		scanner.Scan()
		msg := scanner.Text()
		if len(msg) == 0 {
			continue
		}

		conn.Write([]byte(msg + "\n"))
	}
}
