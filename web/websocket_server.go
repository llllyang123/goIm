package main

import (
	"bytes"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"
)

// 配置参数（修正变量名大小写）
const (
	shardCount      = 32              // 用户连接分片数量
	workerPoolSize  = 1024            // 消息发送工作池大小
	maxMessageSize  = 1024 * 1024     // 最大消息大小（1MB）
	cleanupInterval = 5 * time.Minute // 无效连接清理间隔
	maxConnections  = 100000          // 最大并发连接数（修正为大写开头）
)

// 用户连接分片存储
type connShard struct {
	sync.RWMutex
	conns map[string]*websocket.Conn // 用户名 -> 连接
}

// 全局连接管理
var (
	shards     [shardCount]*connShard
	userCount  int64         // 原子计数器：总在线用户
	workerPool chan func()   // 消息发送工作池
	connSem    chan struct{} // 连接信号量
)

// 初始化资源
func init() {
	// 初始化分片
	for i := 0; i < shardCount; i++ {
		shards[i] = &connShard{
			conns: make(map[string]*websocket.Conn),
		}
	}
	// 初始化工作池和信号量（使用正确的变量名）
	workerPool = make(chan func(), workerPoolSize)
	connSem = make(chan struct{}, maxConnections)

	// 启动工作池消费者
	for i := 0; i < workerPoolSize; i++ {
		go func() {
			for task := range workerPool {
				task() // 执行任务
			}
		}()
	}

	// 启动定期清理无效连接的协程
	go cleanupInvalidConns()
}

// 根据用户名哈希获取分片
func getShard(username string) *connShard {
	hash := 0
	for _, c := range username {
		hash = (hash << 5) - hash + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	return shards[hash%shardCount]
}

// 清理无效连接
func cleanupInvalidConns() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		for _, shard := range shards {
			shard.Lock()
			for username, conn := range shard.conns {
				// 使用写入空数据的方式检测连接是否有效
				err := websocket.Message.Send(conn, "")
				if err != nil {
					delete(shard.conns, username)
					atomic.AddInt64(&userCount, -1)
					fmt.Printf("清理理无效效连接：%s（原因：%v）\n", username, err)
				}
			}
			shard.Unlock()
		}
	}
}

// 消息缓冲区池
var bufferPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

// 检查消息有效性
func isValidMessage(msg string) bool {
	trimmed := strings.TrimSpace(msg)
	return !(len(trimmed) == 0 && len(msg) < 2)
}

// WebSocket处理器
func websocketHandler(ws *websocket.Conn) {
	defer ws.Close()

	// 获取连接信号量
	connSem <- struct{}{}
	defer func() { <-connSem }()

	// 读取用户名（第一帧必须是用户名）
	var username string
	if err := websocket.Message.Receive(ws, &username); err != nil { // 修正方法调用
		fmt.Println("读取用户名失败：", err)
		return
	}
	username = strings.TrimSpace(username)
	if username == "" {
		websocket.Message.Send(ws, "错误：用户名不能为空")
		return
	}

	// 获取用户分片并添加连接
	shard := getShard(username)
	shard.Lock()
	// 检查用户名是否已存在
	if _, exists := shard.conns[username]; exists {
		shard.Unlock()
		websocket.Message.Send(ws, "错误：用户名已被占用")
		return
	}
	shard.conns[username] = ws
	shard.Unlock()

	// 更新在线人数
	atomic.AddInt64(&userCount, 1)
	currentCount := atomic.LoadInt64(&userCount)
	fmt.Printf("用户 %s 上线，当前在线：%d 人\n", username, currentCount)

	// 发送欢迎消息
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	buf.WriteString("欢迎加入聊天室！当前在线人数：")
	buf.WriteString(fmt.Sprintf("%d", currentCount))
	websocket.Message.Send(ws, buf.String())
	bufferPool.Put(buf)

	// 退出时清理资源
	defer func() {
		shard.Lock()
		delete(shard.conns, username)
		shard.Unlock()
		atomic.AddInt64(&userCount, -1)
		fmt.Printf("用户 %s 下线，当前在线：%d 人\n", username, atomic.LoadInt64(&userCount))
	}()

	// 消息读取循环
	for {
		var msg string
		// 修正方法调用错误（移除多余的Message）
		if err := websocket.Message.Receive(ws, &msg); err != nil {
			// 正常关闭不报错
			if !strings.Contains(err.Error(), "closed") {
				fmt.Printf("用户 %s 消息读取失败：%v\n", username, err)
			}
			return
		}

		// 验证消息
		if !isValidMessage(msg) {
			websocket.Message.Send(ws, "错误：不能发送仅含空格或长度小于2的消息")
			continue
		}

		// 构建广播消息
		buf := bufferPool.Get().(*bytes.Buffer)
		buf.Reset()
		buf.WriteString("[")
		buf.WriteString(username)
		buf.WriteString("]：")
		buf.WriteString(msg)
		broadcastMsg := buf.String()
		bufferPool.Put(buf)

		// 广播消息到所有在线用户
		for _, shard := range shards {
			shard.RLock()
			for name, conn := range shard.conns {
				if name != username {
					// 通过工作池异步发送
					connCopy := conn
					msgCopy := broadcastMsg
					select {
					case workerPool <- func() {
						if err := websocket.Message.Send(connCopy, msgCopy); err != nil {
							fmt.Printf("向 %s 发送消息失败：%v\n", name, err)
						}
					}:
					default:
						// 工作池满时降级处理
						go func(c *websocket.Conn, m string) {
							websocket.Message.Send(c, m)
						}(connCopy, msgCopy)
					}
				}
			}
			shard.RUnlock()
		}
	}
}

// 提供Web客户端页面
func serveClient(w http.ResponseWriter, r *http.Request) {
	html := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>WebSocket聊天室</title>
		<style>
			#chat { height: 500px; overflow-y: auto; border: 1px solid #eee; padding: 10px; margin: 10px 0; }
			#message { width: 80%; padding: 8px; margin: 0 1%; }
			#send { width: 17%; padding: 8px; }
			.system { color: #666; font-style: italic; }
			.error { color: #f00; }
		</style>
	</head>
	<body>
		<div id="chat"></div>
		<input type="text" id="message" placeholder="输入消息...">
		<button id="send">发送</button>

		<script>
			const chatDiv = document.getElementById('chat');
			const msgInput = document.getElementById('message');
			const sendBtn = document.getElementById('send');
			
			// 连接WebSocket
			const ws = new WebSocket('ws://' + window.location.host + '/ws');
			ws.binaryType = 'text';

			// 输入用户名
			const username = prompt('请输入用户名：');
			if (!username) {
				alert('用户名不能为空');
				window.location.reload();
			}

			// 连接成功后发送用户名
			ws.onopen = () => {
				ws.send(username);
				addMessage('系统', '连接成功，可开始聊天', 'system');
			};

			// 接收消息
			ws.onmessage = (e) => {
				const isError = e.data.startsWith('错误：');
				addMessage('', e.data, isError ? 'error' : '');
			};

			// 连接关闭
			ws.onclose = () => addMessage('系统', '连接已断开', 'system');
			ws.onerror = (e) => addMessage('系统', '连接错误: ' + e.message, 'error');

			// 添加消息到界面
			function addMessage(sender, content, className) {
				const div = document.createElement('div');
				if (className) div.className = className;
				div.textContent = content;
				chatDiv.appendChild(div);
				chatDiv.scrollTop = chatDiv.scrollHeight;
			}

			// 发送消息
			function sendMessage() {
				const msg = msgInput.value.trim();
				if (msg) {
					ws.send(msg);
					msgInput.value = '';
				}
			}

			// 绑定事件
			sendBtn.addEventListener('click', sendMessage);
			msgInput.addEventListener('keydown', (e) => {
				if (e.key === 'Enter') sendMessage();
			});
		</script>
	</body>
	</html>
	`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func main() {
	// 最大化利用CPU核心
	runtime.GOMAXPROCS(runtime.NumCPU())

	// 配置HTTP服务器
	mux := http.NewServeMux()
	mux.Handle("/ws", websocket.Handler(websocketHandler))
	mux.HandleFunc("/", serveClient)

	server := &http.Server{
		Addr:           ":8080",
		Handler:        mux,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1024 * 10,
	}

	// 启动服务器（使用正确的变量名）
	fmt.Printf("IM服务器启动，监听: http://localhost:8080\n")
	fmt.Printf("配置：最大连接数=%d，工作池大小=%d，分片数=%d\n",
		maxConnections, workerPoolSize, shardCount)

	if err := server.ListenAndServe(); err != nil {
		fmt.Printf("服务器退出：%v\n", err)
	}
}
