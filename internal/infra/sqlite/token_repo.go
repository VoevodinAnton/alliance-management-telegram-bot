package sqlite

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type TokenRepo struct {
	db *sql.DB
}

func NewTokenRepo(dsn string) (*TokenRepo, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrateTokens(db); err != nil {
		return nil, err
	}
	return &TokenRepo{db: db}, nil
}

func migrateTokens(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS broadcast_tokens (
    token TEXT PRIMARY KEY,
    file_id TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);
`)
	return err
}

func (r *TokenRepo) SaveToken(token, fileID string) error {
	_, err := r.db.Exec(`INSERT INTO broadcast_tokens(token, file_id, created_at) VALUES(?, ?, ?) ON CONFLICT(token) DO UPDATE SET file_id=excluded.file_id, created_at=excluded.created_at`, token, fileID, time.Now())
	return err
}

// LookupToken returns fileID, found, error
func (r *TokenRepo) LookupToken(token string) (string, bool, error) {
	var fileID string
	err := r.db.QueryRow(`SELECT file_id FROM broadcast_tokens WHERE token=?`, token).Scan(&fileID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return fileID, true, nil
}
