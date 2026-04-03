package usecase

import (
	"errors"
	"fmt"
	"net/url"
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
	SendText(chatID int64, text string, parseMode string) error
	SendPhoto(chatID int64, fileID string, caption string, parseMode string) error
	SendDocument(chatID int64, fileID string, caption string, parseMode string) error
	SendWithInlineButton(chatID int64, text string, photoFileID string, docFileID string, caption string, parseMode string, buttonText string, buttonDocFileID string, buttonURL string) error
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
	State            BroadcastState
	Text             string
	TextParseMode    string
	PhotoFileID      string
	Caption          string
	CaptionParseMode string
	DocFileID        string
	ButtonText       string
	ButtonDocFileID  string
	ButtonURL        string
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
	s.TextParseMode = ""
	s.PhotoFileID = ""
	s.Caption = ""
	s.CaptionParseMode = ""
	s.DocFileID = ""
	s.ButtonText = ""
	s.ButtonDocFileID = ""
	s.ButtonURL = ""
	return "Отправьте текст или фото/документ (PDF) с подписью. Кнопка: сообщение BUTTON_URL:Текст|https://example.com или PDF с подписью BUTTON:Текст."
}

func (u *BroadcastUsecase) ReceiveText(s *BroadcastSession, text string) (string, []string, error) {
	if strings.TrimSpace(text) == "" {
		return "Текст не должен быть пустым. Введите текст рассылки:", nil, errors.New("empty")
	}
	s.Text = text
	s.TextParseMode = detectParseMode(text)
	s.PhotoFileID = ""
	s.Caption = ""
	s.CaptionParseMode = ""
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
	s.CaptionParseMode = detectParseMode(caption)
	s.Text = ""
	s.TextParseMode = ""
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
		s.ButtonURL = ""
		// keep existing content (text/photo) and stay in confirm state
		s.State = BStateConfirm
		// for button documents no caption is sent
		return "Кнопка прикреплена к рассылке:", []string{"Отправить", "Отмена"}
	}

	// otherwise treat document as main content of the broadcast
	s.DocFileID = fileID
	s.Caption = caption
	s.CaptionParseMode = detectParseMode(caption)
	s.Text = ""
	s.TextParseMode = ""
	s.PhotoFileID = ""
	s.State = BStateConfirm
	return "Подтвердите отправку рассылки с документом:", []string{"Отправить", "Отмена"}
}

// TryAttachButtonLink ожидает сообщение вида BUTTON_URL:Текст|https://... и прикрепляет кнопку со ссылкой.
// Возвращает handled=false, если строка не про кнопку.
func (u *BroadcastUsecase) TryAttachButtonLink(s *BroadcastSession, text string) (handled bool, response string, opts []string, err error) {
	raw := strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToUpper(raw), "BUTTON_URL:") {
		return false, "", nil, nil
	}
	confirmOpts := []string{"Отправить", "Отмена"}
	payload := strings.TrimSpace(raw[len("BUTTON_URL:"):])
	if payload == "" {
		return true, "Укажите ссылку после BUTTON_URL:текст|https://example.com", confirmOpts, errors.New("button url empty")
	}
	label := "Открыть ссылку"
	link := payload
	if strings.Contains(payload, "|") {
		parts := strings.SplitN(payload, "|", 2)
		if strings.TrimSpace(parts[0]) != "" {
			label = strings.TrimSpace(parts[0])
		}
		link = strings.TrimSpace(parts[1])
	}
	if link == "" {
		return true, "Укажите ссылку после разделителя |, пример: BUTTON_URL:Подробнее|https://example.com", confirmOpts, errors.New("button url missing")
	}
	if !strings.HasPrefix(strings.ToLower(link), "http://") && !strings.HasPrefix(strings.ToLower(link), "https://") {
		return true, "Ссылка должна начинаться с http:// или https://", confirmOpts, errors.New("invalid scheme")
	}
	if _, parseErr := url.ParseRequestURI(link); parseErr != nil {
		return true, "Не удалось разобрать ссылку. Пример: BUTTON_URL:Подробнее|https://example.com", confirmOpts, parseErr
	}
	s.ButtonText = label
	s.ButtonURL = link
	s.ButtonDocFileID = ""
	s.State = BStateConfirm
	return true, "Кнопка со ссылкой прикреплена к рассылке:", []string{"Отправить", "Отмена"}, nil
}

func (u *BroadcastUsecase) ConfirmSend(s *BroadcastSession, cmd string) (string, error) {
	if cmd == "Отмена" {
		s.State = BStateIdle
		s.Text = ""
		s.TextParseMode = ""
		s.PhotoFileID = ""
		s.Caption = ""
		s.CaptionParseMode = ""
		s.ButtonText = ""
		s.ButtonDocFileID = ""
		s.ButtonURL = ""
		return "Рассылка отменена.", nil
	}
	if cmd != "Отправить" {
		return "Выберите: Отправить или Отмена", nil
	}
	if strings.TrimSpace(s.Text) == "" && strings.TrimSpace(s.PhotoFileID) == "" && strings.TrimSpace(s.DocFileID) == "" {
		return "Добавьте текст, фото или документ перед отправкой.", errors.New("no content to send")
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
		if s.ButtonText != "" && (s.ButtonDocFileID != "" || s.ButtonURL != "") {
			parseMode := s.TextParseMode
			if s.PhotoFileID != "" || s.DocFileID != "" {
				parseMode = s.CaptionParseMode
			}
			sendErr = u.Sender.SendWithInlineButton(id, s.Text, s.PhotoFileID, s.DocFileID, s.Caption, parseMode, s.ButtonText, s.ButtonDocFileID, s.ButtonURL)
		} else if s.PhotoFileID != "" {
			sendErr = u.Sender.SendPhoto(id, s.PhotoFileID, s.Caption, s.CaptionParseMode)
		} else if s.DocFileID != "" {
			// caption у документа ограничен; передаём Caption, если есть, иначе Text
			cap := s.Caption
			if strings.TrimSpace(cap) == "" {
				cap = s.Text
			}
			sendErr = u.Sender.SendDocument(id, s.DocFileID, cap, s.CaptionParseMode)
		} else {
			sendErr = u.Sender.SendText(id, s.Text, s.TextParseMode)
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
	s.TextParseMode = ""
	s.PhotoFileID = ""
	s.Caption = ""
	s.CaptionParseMode = ""
	s.ButtonText = ""
	s.ButtonDocFileID = ""
	s.ButtonURL = ""
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

// detectParseMode пытается угадать, нужно ли включить Markdown или HTML
// для сохранения форматирования в исходном тексте/подписи.
func detectParseMode(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "<b>") || strings.Contains(lower, "</b>") ||
		strings.Contains(lower, "<i>") || strings.Contains(lower, "</i>") ||
		strings.Contains(lower, "<code>") || strings.Contains(lower, "</code>") ||
		strings.Contains(lower, "<pre>") || strings.Contains(lower, "</pre>") ||
		strings.Contains(lower, "<a ") {
		return "HTML"
	}
	if strings.Contains(raw, "*") || strings.Contains(raw, "_") || strings.Contains(raw, "`") || strings.Contains(raw, "[") {
		return "Markdown"
	}
	return ""
}
