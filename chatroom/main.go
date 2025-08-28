package main

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// websocket.Upgrader 用于将 HTTP 连接升级为 WebSocket 连接
var upgrader = websocket.Upgrader{
	// 允许所有来源连接
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ChatRoom 结构体，管理所有客户端连接和消息广播
type ChatRoom struct {
	clients   map[*websocket.Conn]bool // 存储所有连接的客户端
	lock      sync.Mutex               // 保护 clients 并发安全
	broadcast chan string              // 广播消息的 channel
}

// NewChatRoom 创建并初始化一个新的聊天室实例
func NewChatRoom() *ChatRoom {
	return &ChatRoom{
		clients:   make(map[*websocket.Conn]bool),
		broadcast: make(chan string),
	}
}

// handleConnections 处理 WebSocket 客户端连接
func (room *ChatRoom) handleConnections(c *gin.Context) {
	// 升级 HTTP 连接为 WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		fmt.Println("Upgrade error:", err)
		return
	}

	// 将新连接加入聊天室
	room.lock.Lock()
	room.clients[conn] = true
	room.lock.Unlock()

	// 启动 goroutine 监听客户端消息
	go func() {
		defer func() {
			// 客户端断开时移除连接并关闭
			room.lock.Lock()
			delete(room.clients, conn)
			room.lock.Unlock()
			conn.Close()
		}()

		for {
			// 读取客户端消息
			_, msg, err := conn.ReadMessage()
			if err != nil {
				fmt.Println("Read error:", err)
				break
			}
			// 将消息发送到广播 channel
			room.broadcast <- string(msg)
		}
	}()
}

// start 启动聊天室消息广播循环
func (room *ChatRoom) start() {
	for {
		// 从广播 channel 读取消息
		msg := <-room.broadcast
		room.lock.Lock()
		// 向所有客户端发送消息
		for conn := range room.clients {
			err := conn.WriteMessage(websocket.TextMessage, []byte(msg))
			if err != nil {
				fmt.Println("Write error:", err)
				conn.Close()
				delete(room.clients, conn)
			}
		}
		room.lock.Unlock()
	}
}

func main() {
	r := gin.Default()    // 创建 gin 路由
	room := NewChatRoom() // 初始化聊天室

	// 注册 WebSocket 路由
	r.GET("/ws", room.handleConnections)

	// 启动广播 goroutine
	go room.start()

	fmt.Println("Server started at :8080")
	r.Run(":8080") // 启动 HTTP 服务
}
