package main

import (
	"database/sql"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Player struct {
	id   string
	conn *websocket.Conn
}

type Room struct {
	name    string
	players map[string]*Player
	lock    sync.RWMutex
	secret  int
	db      *sql.DB
}

type GameServer struct {
	rooms map[string]*Room
	lock  sync.RWMutex
	db    *sql.DB
}

func NewGameServer(db *sql.DB) *GameServer {
	return &GameServer{
		rooms: make(map[string]*Room),
		db:    db,
	}
}

// 修复：getRoom 需要写锁创建房间，读锁只用于查找
func (s *GameServer) getRoom(name string) *Room {
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
		room = &Room{
			name:    name,
			players: make(map[string]*Player),
			secret:  rand.Intn(100) + 1,
			db:      s.db,
		}
		s.rooms[name] = room
	}
	return room
}

func (s *GameServer) handleConnections(c *gin.Context) {
	roomName := c.Param("room")
	room := s.getRoom(roomName)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		fmt.Println("Upgrade error:", err)
		return
	}

	playerID := fmt.Sprintf("P%d", len(room.players)+1)
	player := &Player{id: playerID, conn: conn}
	room.lock.Lock()
	room.players[playerID] = player
	room.lock.Unlock()

	room.broadcast(fmt.Sprintf("玩家 %s 加入了房间 %s，当前玩家数: %d", playerID, roomName, len(room.players)))

	go func() {
		defer func() {
			room.lock.Lock()
			delete(room.players, playerID)
			room.lock.Unlock()
			conn.Close()
			room.broadcast(fmt.Sprintf("玩家 %s 离开了房间 %s，当前玩家数: %d", playerID, roomName, len(room.players)))
		}()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				fmt.Println("Read error:", err)
				break
			}
			var guess int
			// 修复：使用 fmt.Sscanf 而不是 fmt.Scanf
			_, err = fmt.Sscanf(string(msg), "%d", &guess)
			if err != nil {
				player.conn.WriteMessage(websocket.TextMessage, []byte("请输入有效的数字"))
				continue
			}

			if guess < room.secret {
				player.conn.WriteMessage(websocket.TextMessage, []byte("太小了"))
			} else if guess > room.secret {
				player.conn.WriteMessage(websocket.TextMessage, []byte("太大了"))
			} else {
				result := fmt.Sprintf("玩家 %s 猜对了！答案是 %d", playerID, room.secret)
				room.broadcast(result)
				// 记录结果到数据库
				room.saveResult(playerID, "win")
				for _, p := range room.players {
					if p.id != playerID {
						room.saveResult(p.id, "lose")
					}
				}
				// 新一轮开始，重置 secret
				room.secret = rand.Intn(100) + 1
				room.broadcast("新一轮开始！请继续猜数字")
			}
		}
	}()
}

func (r *Room) broadcast(msg string) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	for _, p := range r.players {
		p.conn.WriteMessage(websocket.TextMessage, []byte(msg))
	}
}

// 修复：SQL语句参数数量与字段数量一致
func (r *Room) saveResult(playerID, result string) {
	_, err := r.db.Exec("INSERT INTO game_results (player_id, room_name, result) VALUES (?, ?, ?)", playerID, r.name, result)
	if err != nil {
		fmt.Println("保存结果失败:", err)
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())
	dsn := "root:123456@tcp(127.0.0.1:3306)/game_db"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	r := gin.Default()
	server := NewGameServer(db)
	r.GET("/ws/:room", server.handleConnections)
	r.Run(":8080")
}
