package main

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// upgrader 用于将 HTTP 连接升级为 WebSocket 连接
// CheckOrigin 允许所有来源连接，实际生产环境建议做安全校验
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Room 表示一个聊天室
type Room struct {
	name      string                   // 聊天室名称
	clients   map[*websocket.Conn]bool // 当前连接的客户端集合
	lock      sync.Mutex               // 保护 clients 并发安全
	broadcast chan string              // 广播消息的 channel
}

// ChatServer 管理多个聊天室
type ChatServer struct {
	rooms map[string]*Room // 所有聊天室的映射
	lock  sync.Mutex       // 保护 rooms 并发安全
}

// NewRoom 创建一个新的聊天室实例
func NewRoom(name string) *Room {
	return &Room{
		name:      name,
		clients:   make(map[*websocket.Conn]bool),
		broadcast: make(chan string),
	}
}

// start 启动聊天室的消息广播循环
// 不断监听 broadcast channel，将消息发送给所有连接的客户端
func (r *Room) start() {
	for {
		msg := <-r.broadcast // 从广播 channel 读取消息
		r.lock.Lock()
		for conn := range r.clients {
			// 向每个客户端发送消息
			err := conn.WriteMessage(websocket.TextMessage, []byte(msg))
			if err != nil {
				fmt.Println("WriteMessage error:", err)
				conn.Close()
				delete(r.clients, conn) // 发送失败则移除客户端
			}
		}
		r.lock.Unlock()
	}
}

// NewChatServer 创建一个新的聊天服务器实例
func NewChatServer() *ChatServer {
	return &ChatServer{
		rooms: make(map[string]*Room),
	}
}

// getRoom 获取指定名称的聊天室，不存在则创建
func (s *ChatServer) getRoom(name string) *Room {
	s.lock.Lock()
	defer s.lock.Unlock()

	room, exists := s.rooms[name]
	if !exists {
		room = NewRoom(name) // 创建新聊天室
		s.rooms[name] = room // 加入 rooms 映射
		go room.start()      // 启动该聊天室的广播 goroutine
	}
	return room
}

// handleConnections 处理 WebSocket 客户端连接
// 路由格式: /ws/:room
func (s *ChatServer) handleConnections(c *gin.Context) {
	roomName := c.Param("room") // 获取聊天室名称
	room := s.getRoom(roomName) // 获取或创建聊天室

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
				fmt.Println("ReadMessage error:", err)
				break
			}
			// 将消息发送到聊天室广播 channel，带上房间名
			room.broadcast <- fmt.Sprintf("[%s] %s", room.name, msg)
		}
	}()
}

// main 程序入口，启动 Gin Web 服务并注册 WebSocket 路由
func main() {
	r := gin.Default()                           // 创建 Gin 路由引擎
	server := NewChatServer()                    // 创建聊天服务器
	r.GET("/ws/:room", server.handleConnections) // 注册 WebSocket 路由
	r.Run(":8080")                               // 启动 HTTP 服务，监听 8080 端口
}
