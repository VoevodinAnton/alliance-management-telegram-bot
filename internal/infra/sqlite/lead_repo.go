package sqlite

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"

	"alliance-management-telegram-bot/internal/domain"
)

type LeadRepo struct {
	db *sql.DB
}

func NewLeadRepo(dsn string) (*LeadRepo, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &LeadRepo{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS leads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL,
    purpose TEXT,
    bedrooms TEXT,
    payment TEXT,
    phone TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_leads_chat_id ON leads(chat_id);
`)
	return err
}

func (r *LeadRepo) SaveLead(lead domain.Lead) error {
	if lead.CreatedAt.IsZero() {
		lead.CreatedAt = time.Now()
	}
	_, err := r.db.Exec(`INSERT INTO leads(chat_id, purpose, bedrooms, payment, phone, created_at) VALUES(?,?,?,?,?,?)`,
		lead.ChatID, lead.Purpose, lead.Bedrooms, lead.Payment, lead.Phone, lead.CreatedAt)
	return err
}
