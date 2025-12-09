package sqlite

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"

	"zim-gallery-bot/internal/domain"
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
    slot TEXT,
    source TEXT,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_leads_chat_id ON leads(chat_id);
`)
	if err != nil {
		return err
	}
	// Для уже существующих БД добавим недостающие колонки (если они уже есть — ошибки игнорируем)
	_, _ = db.Exec(`ALTER TABLE leads ADD COLUMN slot TEXT`)
	_, _ = db.Exec(`ALTER TABLE leads ADD COLUMN source TEXT`)
	return nil
}

func (r *LeadRepo) SaveLead(lead domain.Lead) error {
	if lead.CreatedAt.IsZero() {
		lead.CreatedAt = time.Now()
	}
	_, err := r.db.Exec(`INSERT INTO leads(chat_id, purpose, bedrooms, payment, phone, slot, source, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		lead.ChatID, lead.Purpose, lead.Bedrooms, lead.Payment, lead.Phone, lead.Slot, lead.Source, lead.CreatedAt)
	return err
}

func (r *LeadRepo) HasPhone(chatID int64) (bool, error) {
	var cnt int
	err := r.db.QueryRow(`SELECT COUNT(1) FROM leads WHERE chat_id=? AND phone IS NOT NULL AND phone<>''`, chatID).Scan(&cnt)
	if err != nil {
		return false, err
	}
	return cnt > 0, nil
}

func (r *LeadRepo) GetLastPhone(chatID int64) (string, error) {
	var phone string
	err := r.db.QueryRow(`SELECT phone FROM leads WHERE chat_id=? AND phone<>'' ORDER BY id DESC LIMIT 1`, chatID).Scan(&phone)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return phone, nil
}

func (r *LeadRepo) UpdateLastLeadSlotAndSource(chatID int64, slot string, source string) error {
	// SQLite-safe: обновим по последнему id подзапросом
	_, err := r.db.Exec(`UPDATE leads SET slot=?, source=? WHERE id=(SELECT id FROM leads WHERE chat_id=? ORDER BY id DESC LIMIT 1)`, slot, source, chatID)
	return err
}

func (r *LeadRepo) GetLastSlot(chatID int64) (string, error) {
	var slot string
	err := r.db.QueryRow(`SELECT slot FROM leads WHERE chat_id=? AND slot<>'' ORDER BY id DESC LIMIT 1`, chatID).Scan(&slot)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return slot, nil
}
