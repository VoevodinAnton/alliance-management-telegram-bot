package usecase

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type BroadcastState string

const (
	BStateIdle    BroadcastState = "idle"
	BStateEnter   BroadcastState = "enter_text"
	BStateConfirm BroadcastState = "confirm"
)

type BroadcastRepository interface {
	ListChatIDs() ([]int64, error)
}

type BroadcastSender interface {
	SendText(chatID int64, text string) error
	SendPhoto(chatID int64, fileID string, caption string) error
}

type BroadcastStat struct {
	Total     int
	Sent      int
	Failed    int
	CreatedAt time.Time
}

type BroadcastStatRepository interface {
	Save(stat BroadcastStat) error
	ListRecent(n int) ([]BroadcastStat, error)
}

type BroadcastSession struct {
	State       BroadcastState
	Text        string
	PhotoFileID string
	Caption     string
}

type BroadcastUsecase struct {
	Repo   BroadcastRepository
	Sender BroadcastSender
	Stat   BroadcastStatRepository
}

func NewBroadcastUsecase(repo BroadcastRepository, sender BroadcastSender, stat BroadcastStatRepository) *BroadcastUsecase {
	return &BroadcastUsecase{Repo: repo, Sender: sender, Stat: stat}
}

func (u *BroadcastUsecase) Start(s *BroadcastSession) string {
	s.State = BStateEnter
	s.Text = ""
	s.PhotoFileID = ""
	s.Caption = ""
	return "Введите текст рассылки сообщением или пришлите фото с подписью."
}

func (u *BroadcastUsecase) ReceiveText(s *BroadcastSession, text string) (string, []string, error) {
	if strings.TrimSpace(text) == "" {
		return "Текст не должен быть пустым. Введите текст рассылки:", nil, errors.New("empty")
	}
	s.Text = text
	s.PhotoFileID = ""
	s.Caption = ""
	s.State = BStateConfirm
	return "Подтвердите отправку рассылки:", []string{"Отправить", "Отмена"}, nil
}

func (u *BroadcastUsecase) ReceivePhoto(s *BroadcastSession, fileID, caption string) (string, []string) {
	if strings.TrimSpace(fileID) == "" {
		return "Не удалось получить изображение. Пришлите фото еще раз.", nil
	}
	s.PhotoFileID = fileID
	s.Caption = caption
	s.Text = ""
	s.State = BStateConfirm
	return "Подтвердите отправку рассылки с фото:", []string{"Отправить", "Отмена"}
}

func (u *BroadcastUsecase) ConfirmSend(s *BroadcastSession, cmd string) (string, error) {
	if cmd == "Отмена" {
		s.State = BStateIdle
		s.Text = ""
		s.PhotoFileID = ""
		s.Caption = ""
		return "Рассылка отменена.", nil
	}
	if cmd != "Отправить" {
		return "Выберите: Отправить или Отмена", nil
	}
	ids, err := u.Repo.ListChatIDs()
	if err != nil {
		return "Не удалось получить список пользователей", err
	}
	var sent, failed int
	for _, id := range ids {
		var sendErr error
		if s.PhotoFileID != "" {
			sendErr = u.Sender.SendPhoto(id, s.PhotoFileID, s.Caption)
		} else {
			sendErr = u.Sender.SendText(id, s.Text)
		}
		if sendErr != nil {
			failed++
			continue
		}
		sent++
	}
	s.State = BStateIdle
	s.Text = ""
	s.PhotoFileID = ""
	s.Caption = ""
	_ = u.Stat.Save(BroadcastStat{Total: len(ids), Sent: sent, Failed: failed})
	return fmt.Sprintf("Рассылка отправлена: %d успешно, %d с ошибками.", sent, failed), nil
}

func (u *BroadcastUsecase) StatsSummary(n int) string {
	stats, err := u.Stat.ListRecent(n)
	if err != nil || len(stats) == 0 {
		return "Статистика недоступна или отсутствует"
	}
	var b strings.Builder
	b.WriteString("Последние рассылки:\n")
	for i, s := range stats {
		fmt.Fprintf(&b, "%d) %s — всего: %d, отправлено: %d, ошибки: %d\n", i+1, s.CreatedAt.Format("2006-01-02 15:04"), s.Total, s.Sent, s.Failed)
	}
	return b.String()
}
