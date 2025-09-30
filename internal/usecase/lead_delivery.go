package usecase

import (
	"context"

	"alliance-management-telegram-bot/internal/domain"
)

// LeadDelivery описывает внешний канал доставки лида (CRM, вебхуки и т.п.)
type LeadDelivery interface {
	SendLead(ctx context.Context, lead domain.Lead) error
}
