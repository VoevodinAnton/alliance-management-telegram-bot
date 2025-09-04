package memory

import (
	"sync"

	"alliance-management-telegram-bot/internal/usecase"
)

type FunnelRepo struct {
	mu     sync.RWMutex
	counts map[usecase.State]map[int64]struct{}
}

func NewFunnelRepo() *FunnelRepo {
	return &FunnelRepo{counts: make(map[usecase.State]map[int64]struct{})}
}

func (r *FunnelRepo) Hit(state usecase.State, chatID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.counts[state]
	if !ok {
		m = make(map[int64]struct{})
		r.counts[state] = m
	}
	m[chatID] = struct{}{}
	return nil
}

func (r *FunnelRepo) Counts() map[usecase.State]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[usecase.State]int, len(r.counts))
	for s, set := range r.counts {
		out[s] = len(set)
	}
	return out
}
