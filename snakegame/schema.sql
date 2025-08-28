CREATE DATABASE IF NOT EXISTS snake_game DEFAULT CHARACTER SET utf8mb4;

USE snake_game;

CREATE TABLE IF NOT EXISTS snake_score (
    id INT AUTO_INCREMENT PRIMARY KEY,
    player_id VARCHAR(50) NOT NULL,
    room VARCHAR(50) NOT NULL,
    score INT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 查看排行榜
-- SELECT player_id, room, MAX(score) AS best_score, COUNT(*) AS games, MAX(created_at) AS last_play
-- FROM snake_score GROUP BY player_id, room ORDER BY best_score DESC LIMIT 10;