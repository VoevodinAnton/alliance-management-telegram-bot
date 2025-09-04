package sqlite

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type UserRepo struct {
	db *sql.DB
}

func NewUserRepo(dsn string) (*UserRepo, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrateUsers(db); err != nil {
		return nil, err
	}
	return &UserRepo{db: db}, nil
}

func migrateUsers(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS users (
    chat_id INTEGER PRIMARY KEY,
    created_at TIMESTAMP NOT NULL
);
`)
	return err
}

func (r *UserRepo) SaveUser(chatID int64) error {
	// upsert by primary key
	_, err := r.db.Exec(`INSERT INTO users(chat_id, created_at) VALUES(?, ?) ON CONFLICT(chat_id) DO NOTHING`, chatID, time.Now())
	return err
}

func (r *UserRepo) ListChatIDs() ([]int64, error) {
	rows, err := r.db.Query(`SELECT chat_id FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 128)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
