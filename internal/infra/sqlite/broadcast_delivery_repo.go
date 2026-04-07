package sqlite

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"

	"zim-gallery-bot/internal/usecase"
)

type BroadcastDeliveryRepo struct {
	db *sql.DB
}

func NewBroadcastDeliveryRepo(dsn string) (*BroadcastDeliveryRepo, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrateBroadcastDeliveries(db); err != nil {
		return nil, err
	}
	return &BroadcastDeliveryRepo{db: db}, nil
}

func migrateBroadcastDeliveries(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS broadcast_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    broadcast_key TEXT NOT NULL,
    chat_id INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL,
    deleted_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_broadcast_deliveries_key ON broadcast_deliveries(broadcast_key);
CREATE INDEX IF NOT EXISTS idx_broadcast_deliveries_created ON broadcast_deliveries(created_at);
`)
	return err
}

func (r *BroadcastDeliveryRepo) Save(delivery usecase.BroadcastDelivery) error {
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = time.Now()
	}
	_, err := r.db.Exec(
		`INSERT INTO broadcast_deliveries(broadcast_key, chat_id, message_id, created_at) VALUES(?,?,?,?)`,
		delivery.BroadcastKey,
		delivery.ChatID,
		delivery.MessageID,
		delivery.CreatedAt,
	)
	return err
}

func (r *BroadcastDeliveryRepo) ListLastBroadcast() ([]usecase.BroadcastDelivery, error) {
	var key string
	err := r.db.QueryRow(`
SELECT broadcast_key
FROM broadcast_deliveries
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1
`).Scan(&key)
	if err == sql.ErrNoRows {
		return []usecase.BroadcastDelivery{}, nil
	}
	if err != nil {
		return nil, err
	}

	rows, err := r.db.Query(`
SELECT broadcast_key, chat_id, message_id, created_at
FROM broadcast_deliveries
WHERE broadcast_key = ? AND deleted_at IS NULL
ORDER BY id ASC
`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]usecase.BroadcastDelivery, 0, 64)
	for rows.Next() {
		var d usecase.BroadcastDelivery
		if err := rows.Scan(&d.BroadcastKey, &d.ChatID, &d.MessageID, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (r *BroadcastDeliveryRepo) MarkBroadcastDeleted(broadcastKey string, deletedAt time.Time) error {
	if deletedAt.IsZero() {
		deletedAt = time.Now()
	}
	_, err := r.db.Exec(`UPDATE broadcast_deliveries SET deleted_at=? WHERE broadcast_key=? AND deleted_at IS NULL`, deletedAt, broadcastKey)
	return err
}
