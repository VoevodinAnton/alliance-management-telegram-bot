package sqlite

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"

	"alliance-management-telegram-bot/internal/usecase"
)

type FunnelRepo struct {
	db *sql.DB
}

func NewFunnelRepo(dsn string) (*FunnelRepo, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrateFunnel(db); err != nil {
		return nil, err
	}
	return &FunnelRepo{db: db}, nil
}

func migrateFunnel(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS funnel_hits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL,
    state TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_funnel_hits_state ON funnel_hits(state);
CREATE INDEX IF NOT EXISTS idx_funnel_hits_chat_state ON funnel_hits(chat_id, state);
`)
	return err
}

func (r *FunnelRepo) Hit(state usecase.State, chatID int64) error {
	_, err := r.db.Exec(`INSERT INTO funnel_hits(chat_id, state, created_at) VALUES(?,?,?)`, chatID, string(state), time.Now())
	return err
}

func (r *FunnelRepo) Counts() map[usecase.State]int {
	rows, err := r.db.Query(`SELECT state, COUNT(DISTINCT chat_id) FROM funnel_hits GROUP BY state`)
	if err != nil {
		return map[usecase.State]int{}
	}
	defer rows.Close()
	out := map[usecase.State]int{}
	for rows.Next() {
		var state string
		var cnt int
		if err := rows.Scan(&state, &cnt); err == nil {
			out[usecase.State(state)] = cnt
		}
	}
	return out
}
