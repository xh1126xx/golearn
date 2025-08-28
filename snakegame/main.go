package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"
)

// WebSocket升级器，允许所有来源连接
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// 点结构体，表示坐标
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Snake结构体，表示一条蛇
type Snake struct {
	ID    string  `json:"id"`    // 玩家ID
	Body  []Point `json:"body"`  // 蛇身体坐标
	Dir   string  `json:"dir"`   // 当前方向
	Score int     `json:"score"` // 得分
	Alive bool    `json:"alive"` // 是否存活

	conn *websocket.Conn `json:"-"` // WebSocket连接（不序列化）
}

// 房间结构体，管理一局游戏
type Room struct {
	name    string
	width   int
	height  int
	players map[string]*Snake // 所有玩家
	food    Point             // 食物坐标
	lock    sync.Mutex        // 并发锁
	db      *sql.DB           // 数据库连接

	onceLoop sync.Once     // 保证runLoop只启动一次
	stopCh   chan struct{} // 停止信号
}

// 游戏服务器结构体，管理所有房间
type GameServer struct {
	rooms map[string]*Room
	lock  sync.Mutex
	db    *sql.DB
}

// 创建新游戏服务器
func NewGameServer(db *sql.DB) *GameServer {
	return &GameServer{
		rooms: make(map[string]*Room),
		db:    db,
	}
}

// 获取房间，不存在则新建并启动循环
func (s *GameServer) getRoom(name string) *Room {
	s.lock.Lock()
	defer s.lock.Unlock()

	room, exists := s.rooms[name]
	if !exists {
		room = &Room{
			name:    name,
			width:   20,
			height:  20,
			players: make(map[string]*Snake),
			food:    Point{X: rand.Intn(20), Y: rand.Intn(20)},
			db:      s.db,
			stopCh:  make(chan struct{}),
		}
		s.rooms[name] = room
		// 只启动一次循环
		room.onceLoop.Do(func() {
			go room.runLoop()
		})
	}
	return room
}

// 房间主循环，定时更新游戏状态
func (r *Room) runLoop() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.update()
		case <-r.stopCh:
			return
		}
	}
}

// 更新所有蛇状态并广播
func (r *Room) update() {
	r.lock.Lock()
	defer r.lock.Unlock()

	for _, snake := range r.players {
		if !snake.Alive || len(snake.Body) == 0 {
			continue
		}

		head := snake.Body[0]
		next := head
		// 根据方向计算下一个点
		switch snake.Dir {
		case "up":
			next.Y--
		case "down":
			next.Y++
		case "left":
			next.X--
		case "right":
			next.X++
		}

		// 撞墙判定
		if next.X < 0 || next.X >= r.width || next.Y < 0 || next.Y >= r.height {
			if snake.Alive {
				snake.Alive = false
				r.saveScore(snake.ID, snake.Score)
			}
			continue
		}

		var newBody []Point

		// 撞自己判定
		selfHit := false
		for _, b := range snake.Body {
			if next == b {
				selfHit = true
				break
			}
		}
		if selfHit {
			if snake.Alive {
				snake.Alive = false
				r.saveScore(snake.ID, snake.Score)
			}
			continue
		}

		// 撞其他玩家判定
		otherHit := false
		for _, other := range r.players {
			if other.ID == snake.ID {
				continue
			}
			for _, b := range other.Body {
				if next == b {
					otherHit = true
					break
				}
			}
			if otherHit {
				break
			}
		}
		if otherHit {
			if snake.Alive {
				snake.Alive = false
				r.saveScore(snake.ID, snake.Score)
			}
			continue
		}

		// 正常前进
		newBody = append([]Point{next}, snake.Body[:len(snake.Body)-1]...)
		snake.Body = newBody

		// 吃食物判定
		if next == r.food {
			snake.Score++
			tail := snake.Body[len(snake.Body)-1]
			snake.Body = append(snake.Body, tail)
			r.food = r.randomEmptyCell()
		}
	}

	// 广播当前状态给所有玩家
	state := map[string]interface{}{
		"type":    "state",
		"players": r.snapshotPlayers(),
		"food":    r.food,
		"room":    r.name,
		"w":       r.width,
		"h":       r.height,
	}
	data, _ := json.Marshal(state)
	for _, s := range r.players {
		if s.conn != nil {
			_ = s.conn.WriteMessage(websocket.TextMessage, data)
		}
	}
}

// 复制所有玩家状态（用于广播）
func (r *Room) snapshotPlayers() map[string]*Snake {
	out := make(map[string]*Snake, len(r.players))
	for id, s := range r.players {
		cp := &Snake{
			ID:    s.ID,
			Body:  append([]Point(nil), s.Body...),
			Dir:   s.Dir,
			Score: s.Score,
			Alive: s.Alive,
		}
		out[id] = cp
	}
	return out
}

// 随机生成一个未被占用的点作为食物
func (r *Room) randomEmptyCell() Point {
	for i := 0; i < 200; i++ {
		p := Point{X: rand.Intn(r.width), Y: rand.Intn(r.height)}
		occupied := false
		for _, s := range r.players {
			for _, b := range s.Body {
				if p == b {
					occupied = true
					break
				}
			}
			if occupied {
				break
			}
		}
		if !occupied {
			return p
		}
	}
	return r.food
}

// 保存玩家得分到数据库
func (r *Room) saveScore(playerID string, score int) {
	_, err := r.db.Exec("INSERT INTO snake_score (player_id, room, score) VALUES (?, ?, ?)",
		playerID, r.name, score)
	if err != nil {
		log.Println("DB insert error:", err)
	}
}

// 处理WebSocket连接，玩家加入房间
func (s *GameServer) handleWS(c *gin.Context) {
	roomName := c.Param("room")
	room := s.getRoom(roomName)

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}

	room.lock.Lock()
	playerID := fmt.Sprintf("P%d", len(room.players)+1)
	snake := &Snake{
		ID:    playerID,
		Body:  []Point{{X: rand.Intn(room.width), Y: rand.Intn(room.height)}},
		Dir:   "right",
		Score: 0,
		Alive: true,
		conn:  conn,
	}
	room.players[playerID] = snake
	room.lock.Unlock()

	// 发送欢迎信息
	welcome := map[string]interface{}{
		"type":    "welcome",
		"player":  playerID,
		"room":    room.name,
		"w":       room.width,
		"h":       room.height,
		"food":    room.food,
		"players": room.snapshotPlayers(),
	}
	_ = conn.WriteJSON(welcome)

	// 监听玩家消息
	go func() {
		defer func() {
			room.lock.Lock()
			if snake.Alive {
				room.saveScore(snake.ID, snake.Score)
			}
			delete(room.players, playerID)
			room.lock.Unlock()
			_ = conn.Close()

			// 广播玩家离开
			msg := map[string]string{"type": "leave", "player": playerID}
			data, _ := json.Marshal(msg)
			room.lock.Lock()
			for _, s := range room.players {
				if s.conn != nil {
					_ = s.conn.WriteMessage(websocket.TextMessage, data)
				}
			}
			room.lock.Unlock()
		}()

		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if mt != websocket.TextMessage {
				continue
			}
			cmd := string(msg)
			switch cmd {
			case "up", "down", "left", "right":
				// 方向变更，不能反向
				room.lock.Lock()
				if (snake.Dir == "up" && cmd != "down") ||
					(snake.Dir == "down" && cmd != "up") ||
					(snake.Dir == "left" && cmd != "right") ||
					(snake.Dir == "right" && cmd != "left") {
					snake.Dir = cmd
				}
				room.lock.Unlock()
			case "ping":
				_ = conn.WriteMessage(websocket.TextMessage, []byte("pong"))
			}
		}
	}()
}

// 排行榜结构体
type RankRow struct {
	PlayerID string `json:"player_id"`
	Room     string `json:"room"`
	Best     int    `json:"best_score"`
	Games    int    `json:"games"`
	Last     string `json:"last_play"`
}

// 查询排行榜接口
func (s *GameServer) leaderboard(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "10")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 100 {
		limit = 10
	}
	room := c.DefaultQuery("room", "%")

	rows, err := s.db.Query(`
		SELECT player_id, room, MAX(score) AS best_score, COUNT(*) AS games, MAX(created_at) AS last_play
		FROM snake_score
		WHERE room LIKE ?
		GROUP BY player_id, room
		ORDER BY best_score DESC, last_play DESC
		LIMIT ?`, room, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db query error"})
		return
	}
	defer rows.Close()

	var out []RankRow
	for rows.Next() {
		var r RankRow
		if err := rows.Scan(&r.PlayerID, &r.Room, &r.Best, &r.Games, &r.Last); err == nil {
			out = append(out, r)
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// 健康检查接口
func (s *GameServer) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "time": time.Now().Format(time.RFC3339)})
}

// 程序入口
func main() {
	rand.Seed(time.Now().UnixNano())

	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "root:123456@tcp(127.0.0.1:3306)/snake_game?parseTime=true"
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("open db error: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("db ping error: %v", err)
	}

	server := NewGameServer(db)

	r := gin.Default()
	r.GET("/ws/:room", server.handleWS)           // WebSocket游戏接口
	r.GET("/api/leaderboard", server.leaderboard) // 排行榜接口
	r.GET("/health", server.health)               // 健康检查
	r.StaticFile("/", "./client.html")            // 前端页面

	r.NoRoute(func(c *gin.Context) {
		c.File("./client.html")
	})

	addr := ":8080"
	log.Printf("Snake game server running at %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
