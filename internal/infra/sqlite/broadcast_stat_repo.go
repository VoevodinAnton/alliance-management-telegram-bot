package sqlite

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"

	"alliance-management-telegram-bot/internal/usecase"
)

type BroadcastStatRepo struct {
	db *sql.DB
}

func NewBroadcastStatRepo(dsn string) (*BroadcastStatRepo, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrateBroadcastStat(db); err != nil {
		return nil, err
	}
	return &BroadcastStatRepo{db: db}, nil
}

func migrateBroadcastStat(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS broadcast_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    total INTEGER NOT NULL,
    sent INTEGER NOT NULL,
    failed INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL
);
`)
	return err
}

func (r *BroadcastStatRepo) Save(stat usecase.BroadcastStat) error {
	if stat.CreatedAt.IsZero() {
		stat.CreatedAt = time.Now()
	}
	_, err := r.db.Exec(`INSERT INTO broadcast_stats(total, sent, failed, created_at) VALUES(?,?,?,?)`, stat.Total, stat.Sent, stat.Failed, stat.CreatedAt)
	return err
}

func (r *BroadcastStatRepo) ListRecent(n int) ([]usecase.BroadcastStat, error) {
	if n <= 0 {
		n = 10
	}
	rows, err := r.db.Query(`SELECT total, sent, failed, created_at FROM broadcast_stats ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]usecase.BroadcastStat, 0, n)
	for rows.Next() {
		var s usecase.BroadcastStat
		if err := rows.Scan(&s.Total, &s.Sent, &s.Failed, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
