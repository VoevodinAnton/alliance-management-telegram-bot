package memory

import "sync"

type UserRepo struct {
	mu     sync.RWMutex
	chatID map[int64]struct{}
}

func NewUserRepo() *UserRepo {
	return &UserRepo{chatID: make(map[int64]struct{})}
}

func (r *UserRepo) SaveUser(chatID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chatID[chatID] = struct{}{}
	return nil
}

func (r *UserRepo) ListChatIDs() ([]int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]int64, 0, len(r.chatID))
	for id := range r.chatID {
		res = append(res, id)
	}
	return res, nil
}
