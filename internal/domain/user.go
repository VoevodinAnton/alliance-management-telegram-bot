package domain

type User struct {
	ChatID int64
}

type UserRepository interface {
	SaveUser(chatID int64) error
	ListChatIDs() ([]int64, error)
}

// Abstraction for sending messages (implemented by Telegram adapter)
type MessageSender interface {
	SendText(chatID int64, text string) error
}
