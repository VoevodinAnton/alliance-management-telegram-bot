package domain

import "time"

type Lead struct {
	ChatID    int64
	Purpose   string
	Bedrooms  string
	Payment   string
	Phone     string
	CreatedAt time.Time
}

type LeadRepository interface {
	SaveLead(lead Lead) error
}
