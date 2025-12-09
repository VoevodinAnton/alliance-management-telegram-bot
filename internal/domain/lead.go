package domain

import "time"

type Lead struct {
	ChatID    int64
	Purpose   string
	Bedrooms  string
	Payment   string
	Phone     string
	Slot      string
	Source    string
	Name      string
	CreatedAt time.Time
}

type LeadRepository interface {
	SaveLead(lead Lead) error
	HasPhone(chatID int64) (bool, error)
	GetLastPhone(chatID int64) (string, error)
	UpdateLastLeadSlotAndSource(chatID int64, slot string, source string) error
	GetLastSlot(chatID int64) (string, error)
}
