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
	SendDocument(chatID int64, fileID string, caption string) error
	SendWithInlineButton(chatID int64, text string, photoFileID string, docFileID string, caption string, buttonText string, buttonDocFileID string) error
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
	State           BroadcastState
	Text            string
	PhotoFileID     string
	Caption         string
	DocFileID       string
	ButtonText      string
	ButtonDocFileID string
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
	s.DocFileID = ""
	return "Введите текст рассылки сообщением или пришлите фото/документ (PDF) с подписью."
}

func (u *BroadcastUsecase) ReceiveText(s *BroadcastSession, text string) (string, []string, error) {
	if strings.TrimSpace(text) == "" {
		return "Текст не должен быть пустым. Введите текст рассылки:", nil, errors.New("empty")
	}
	s.Text = text
	s.PhotoFileID = ""
	s.Caption = ""
	s.DocFileID = ""
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
	s.DocFileID = ""
	s.State = BStateConfirm
	return "Подтвердите отправку рассылки с фото:", []string{"Отправить", "Отмена"}
}

func (u *BroadcastUsecase) ReceiveDocument(s *BroadcastSession, fileID, caption string) (string, []string) {
	if strings.TrimSpace(fileID) == "" {
		return "Не удалось получить документ. Пришлите файл ещё раз.", nil
	}
	// Special case: if caption starts with BUTTON:label then treat this document
	// as the PDF that will be sent when inline button is clicked.
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(caption)), "BUTTON:") {
		// parse label after BUTTON:
		parts := strings.SplitN(caption, ":", 2)
		label := "Файл"
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			label = strings.TrimSpace(parts[1])
		}
		s.ButtonText = label
		s.ButtonDocFileID = fileID
		// keep existing content (text/photo) and stay in confirm state
		s.State = BStateConfirm
		return "Кнопка прикреплена к рассылке:", []string{"Отправить", "Отмена"}
	}

	// otherwise treat document as main content of the broadcast
	s.DocFileID = fileID
	s.Caption = caption
	s.Text = ""
	s.PhotoFileID = ""
	s.State = BStateConfirm
	return "Подтвердите отправку рассылки с документом:", []string{"Отправить", "Отмена"}
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
	var firstErr error
	for _, id := range ids {
		var sendErr error
		// If an inline button is set, send content with inline button attached.
		if s.ButtonText != "" && s.ButtonDocFileID != "" {
			sendErr = u.Sender.SendWithInlineButton(id, s.Text, s.PhotoFileID, s.DocFileID, s.Caption, s.ButtonText, s.ButtonDocFileID)
		} else if s.PhotoFileID != "" {
			sendErr = u.Sender.SendPhoto(id, s.PhotoFileID, s.Caption)
		} else if s.DocFileID != "" {
			// caption у документа ограничен; передаём Caption, если есть, иначе Text
			cap := s.Caption
			if strings.TrimSpace(cap) == "" {
				cap = s.Text
			}
			sendErr = u.Sender.SendDocument(id, s.DocFileID, cap)
		} else {
			sendErr = u.Sender.SendText(id, s.Text)
		}
		if sendErr != nil {
			failed++
			if firstErr == nil {
				firstErr = sendErr
			}
			continue
		}
		sent++
	}
	s.State = BStateIdle
	s.Text = ""
	s.PhotoFileID = ""
	s.Caption = ""
	_ = u.Stat.Save(BroadcastStat{Total: len(ids), Sent: sent, Failed: failed})
	summary := fmt.Sprintf("Рассылка отправлена: %d успешно, %d с ошибками.", sent, failed)
	if failed > 0 {
		return summary, fmt.Errorf("%d failed; sample error: %w", failed, firstErr)
	}
	return summary, nil
}

func (u *BroadcastUsecase) StatsSummary(n int) string {
	stats, err := u.Stat.ListRecent(n)
	if err != nil || len(stats) == 0 {
		return "Статистика рассылок недоступна или отсутствует"
	}
	var b strings.Builder
	b.WriteString("Последние рассылки:\n")
	for i, s := range stats {
		fmt.Fprintf(&b, "%d) %s — всего: %d, отправлено: %d, ошибки: %d\n", i+1, s.CreatedAt.Format("2006-01-02 15:04"), s.Total, s.Sent, s.Failed)
	}
	return b.String()
}
