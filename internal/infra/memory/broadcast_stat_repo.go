package memory

import (
	"sync"
	"time"

	"alliance-management-telegram-bot/internal/usecase"
)

type BroadcastStatRepo struct {
	mu    sync.RWMutex
	stats []usecase.BroadcastStat
}

func NewBroadcastStatRepo() *BroadcastStatRepo {
	return &BroadcastStatRepo{stats: make([]usecase.BroadcastStat, 0, 32)}
}

func (r *BroadcastStatRepo) Save(stat usecase.BroadcastStat) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stat.CreatedAt = time.Now()
	r.stats = append(r.stats, stat)
	return nil
}

func (r *BroadcastStatRepo) ListRecent(n int) ([]usecase.BroadcastStat, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n <= 0 || n > len(r.stats) {
		n = len(r.stats)
	}
	// вернуть последние n в обратном хронологическом порядке
	res := make([]usecase.BroadcastStat, 0, n)
	for i := len(r.stats) - 1; i >= 0 && len(res) < n; i-- {
		res = append(res, r.stats[i])
	}
	return res, nil
}
