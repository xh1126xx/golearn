package main

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// WebSocket升级器，允许所有来源连接
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// 玩家结构体，包含ID、连接和出拳动作
type Player struct {
	id   string
	conn *websocket.Conn
	move string
}

// 房间结构体，包含房间名、玩家集合和互斥锁
type Room struct {
	name    string
	players map[string]*Player
	lock    sync.RWMutex // 优化为读写锁，提高并发性能
}

// 聊天服务器结构体，管理所有房间
type ChatServer struct {
	rooms map[string]*Room
	lock  sync.RWMutex // 优化为读写锁
}

// 创建新房间
func NewRoom(name string) *Room {
	return &Room{
		name:    name,
		players: make(map[string]*Player),
	}
}

// 创建新聊天服务器
func NewChatServer() *ChatServer {
	return &ChatServer{
		rooms: make(map[string]*Room),
	}
}

// 获取房间，不存在则新建
func (s *ChatServer) getRoom(name string) *Room {
	s.lock.RLock()
	room, exists := s.rooms[name]
	s.lock.RUnlock()
	if exists {
		return room
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	// 再次检查，防止并发重复创建
	room, exists = s.rooms[name]
	if !exists {
		room = NewRoom(name)
		s.rooms[name] = room
	}
	return room
}

// 判断胜负
func decide(p1, p2 *Player) string {
	if p1.move == p2.move {
		return "平局"
	}

	if (p1.move == "rock" && p2.move == "scissors") ||
		(p1.move == "scissors" && p2.move == "paper") ||
		(p1.move == "paper" && p2.move == "rock") {
		return fmt.Sprintf("玩家 %s 赢了！", p1.id)
	}
	return fmt.Sprintf("玩家 %s 赢了！", p2.id)
}

// 处理WebSocket连接
func (s *ChatServer) handleConnections(c *gin.Context) {
	roomName := c.Param("room")
	room := s.getRoom(roomName)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		fmt.Println("升级到WebSocket失败:", err)
		return
	}

	PlayerID := fmt.Sprintf("Player%d", len(room.players)+1)
	player := &Player{id: PlayerID, conn: conn}

	room.lock.Lock()
	room.players[PlayerID] = player
	room.lock.Unlock()

	room.broadcast(fmt.Sprintf("玩家%s 加入了房间%s", PlayerID, room.name))

	go func() {
		defer func() {
			room.lock.Lock()
			delete(room.players, PlayerID)
			room.lock.Unlock()
			conn.Close()
			room.broadcast(fmt.Sprintf("玩家%s 离开了房间%s", PlayerID, room.name))
		}()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				fmt.Println("读取消息失败:", err)
				break
			}
			move := string(msg)
			player.move = move
			room.broadcast(fmt.Sprintf("玩家%s 出了 %s", PlayerID, move))

			// 只在有两个玩家且都已出招时判断胜负
			room.lock.RLock()
			if len(room.players) == 2 {
				var p1, p2 *Player
				for _, p := range room.players {
					if p1 == nil {
						p1 = p
					} else {
						p2 = p
					}
				}
				if p1 != nil && p2 != nil && p1.move != "" && p2.move != "" {
					room.lock.RUnlock()
					result := decide(p1, p2)
					room.broadcast("结果：" + result)
					room.lock.Lock()
					p1.move = ""
					p2.move = ""
					room.lock.Unlock()
					continue
				}
			}
			room.lock.RUnlock()
		}
	}()
}

// 广播消息给所有玩家
func (r *Room) broadcast(message string) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	for _, p := range r.players {
		if err := p.conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			fmt.Println("发送消息失败:", err)
		}
	}
}

func main() {
	r := gin.Default()
	chatServer := NewChatServer()

	r.GET("/ws/:room", chatServer.handleConnections)

	r.Run(":8080")
}
