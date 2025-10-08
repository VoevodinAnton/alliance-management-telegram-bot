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
CREATE INDEX IF NOT EXISTS idx_funnel_hits_created_at ON funnel_hits(created_at);
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

// DailyActiveCounts считает уникальные chat_id по дням за последние days дней
func (r *FunnelRepo) DailyActiveCounts(days int) map[string]int {
	if days <= 0 {
		days = 7
	}
	start := time.Now().AddDate(0, 0, -days+1)
	startEpoch := start.Unix()
	startDate := start.Format("2006-01-02")
	rows, err := r.db.Query(`
WITH ints AS (
  SELECT strftime('%Y-%m-%d', created_at, 'unixepoch', 'localtime') AS d, chat_id
  FROM funnel_hits
  WHERE typeof(created_at)='integer' AND created_at >= ?
),
texts AS (
  SELECT substr(created_at,1,10) AS d, chat_id
  FROM funnel_hits
  WHERE typeof(created_at)='text' AND substr(created_at,1,10) >= ?
),
hits AS (
  SELECT d, chat_id FROM ints
  UNION ALL
  SELECT d, chat_id FROM texts
)
SELECT d, COUNT(DISTINCT chat_id)
FROM hits
GROUP BY d
ORDER BY d
`, startEpoch, startDate)
	if err != nil {
		return map[string]int{}
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var day string
		var cnt int
		if err := rows.Scan(&day, &cnt); err == nil {
			out[day] = cnt
		}
	}
	return out
}
