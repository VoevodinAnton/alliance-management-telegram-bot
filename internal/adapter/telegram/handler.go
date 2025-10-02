package telegram

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	chart "github.com/wcharczuk/go-chart/v2"

	"alliance-management-telegram-bot/internal/domain"
	"alliance-management-telegram-bot/internal/usecase"
)

type Handler struct {
	bot         *tgbotapi.BotAPI
	dialog      *usecase.Dialog
	userRepo    domain.UserRepository
	broadcastUC *usecase.BroadcastUsecase
	adminIDs    map[int64]struct{}

	sessions      map[int64]*usecase.Session
	bcastSessions map[int64]*usecase.BroadcastSession
	funnel        *usecase.FunnelUsecase
	leadRepo      domain.LeadRepository
	leadDelivery  usecase.LeadDelivery
	logger        *slog.Logger

	// cache for telegram file_ids to speed up repeated sends
	catalogMu      sync.RWMutex
	catalogFileID  map[string]string
	catalogPhotoID map[string]string
}

func NewHandler(bot *tgbotapi.BotAPI, dialog *usecase.Dialog, userRepo domain.UserRepository, broadcastUC *usecase.BroadcastUsecase, adminIDs map[int64]struct{}, funnel *usecase.FunnelUsecase, logger *slog.Logger) *Handler {
	return &Handler{
		bot:            bot,
		dialog:         dialog,
		userRepo:       userRepo,
		broadcastUC:    broadcastUC,
		adminIDs:       adminIDs,
		sessions:       make(map[int64]*usecase.Session),
		bcastSessions:  make(map[int64]*usecase.BroadcastSession),
		funnel:         funnel,
		logger:         logger,
		catalogFileID:  make(map[string]string),
		catalogPhotoID: make(map[string]string),
	}
}

func (h *Handler) SetLeadRepository(repo domain.LeadRepository) { h.leadRepo = repo }

func (h *Handler) SetLeadDelivery(d usecase.LeadDelivery) { h.leadDelivery = d }

// trackFunnel — небольшой хелпер, чтобы не дублировать проверку на nil
func (h *Handler) trackFunnel(chatID int64, state usecase.State) {
	if h.funnel != nil {
		h.funnel.Reach(chatID, state)
	}
}

func ParseAdminIDsFromEnv() map[int64]struct{} {
	ids := map[int64]struct{}{}
	raw := strings.TrimSpace(os.Getenv("ADMIN_CHAT_IDS"))
	if raw == "" {
		return ids
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			ids[id] = struct{}{}
		}
	}
	return ids
}

func (h *Handler) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := h.bot.GetUpdatesChan(u)
	for update := range updates {
		if update.Message == nil && update.CallbackQuery == nil {
			continue
		}
		var chatID int64
		var text string
		if update.Message != nil {
			chatID = update.Message.Chat.ID
			text = update.Message.Text
		} else if update.CallbackQuery != nil {
			chatID = update.CallbackQuery.Message.Chat.ID
			text = update.CallbackQuery.Data
		}
		// сохраняем только не-админов
		if !h.isAdmin(chatID) {
			_ = h.userRepo.SaveUser(chatID)
		}

		if text == "/admin" {
			if !h.isAdmin(chatID) {
				h.sendText(chatID, "Доступ запрещен")
				if h.logger != nil {
					h.logger.Warn("admin denied", "chat_id", chatID)
				}
				continue
			}
			msg := tgbotapi.NewMessage(chatID, "Админ-меню")
			msg.ReplyMarkup = inlineKeyboard([]string{"Создать рассылку", "Статистика", "Воронка"})
			_, _ = h.bot.Send(msg)
			if h.logger != nil {
				h.logger.Info("admin opened menu", "chat_id", chatID)
			}
			continue
		}
		if h.isAdmin(chatID) {
			if text == "Создать рассылку" {
				s := h.getBSession(chatID)
				msg := h.broadcastUC.Start(s)
				h.sendTextWithKeyboard(chatID, msg, nil)
				if h.logger != nil {
					h.logger.Info("broadcast start", "chat_id", chatID)
				}
				continue
			}
			if text == "Статистика" {
				h.sendText(chatID, h.broadcastUC.StatsSummary(5))
				continue
			}
			if text == "Воронка" {
				if h.funnel != nil {
					labels, values := h.funnel.GraphData()
					if err := h.sendFunnelChart(chatID, labels, values); err != nil {
						if h.logger != nil {
							h.logger.Error("funnel chart failed", "error", err)
						}
						h.sendText(chatID, h.funnel.Chart())
					}
				} else {
					h.sendText(chatID, "Воронка недоступна")
				}
				continue
			}
			if s := h.bcastSessions[chatID]; s != nil {
				if m := update.Message; m != nil && len(m.Photo) > 0 {
					ph := m.Photo[len(m.Photo)-1]
					fileID := ph.FileID
					caption := m.Caption
					msg, opts := h.broadcastUC.ReceivePhoto(s, fileID, caption)
					h.sendTextWithKeyboard(chatID, msg, opts)
					continue
				}
				switch s.State {
				case usecase.BStateEnter:
					msg, opts, _ := h.broadcastUC.ReceiveText(s, text)
					h.sendTextWithKeyboard(chatID, msg, opts)
					continue
				case usecase.BStateConfirm:
					msg, _ := h.broadcastUC.ConfirmSend(s, text)
					h.sendTextRemoveKeyboard(chatID, msg)
					if h.logger != nil {
						h.logger.Info("broadcast confirm", "chat_id", chatID)
					}
					continue
				}
			}
			continue
		}

		if update.Message != nil && update.Message.Contact != nil {
			s := h.getSession(chatID)
			if s.State == usecase.StateRequestPhone {
				s.Phone = update.Message.Contact.PhoneNumber
				h.saveAndSendLead(chatID, s)
				go func(id int64) {
					time.Sleep(2 * time.Minute)
					h.sessions[id] = &usecase.Session{State: usecase.StateStart}
				}(chatID)
				continue
			}
		}

		// Принимаем номер текстом, если включен StateRequestPhone
		if update.Message != nil {
			rawText := strings.TrimSpace(update.Message.Text)
			if rawText != "" && !strings.HasPrefix(rawText, "/") { // не перехватывать команды типа /start
				s := h.getSession(chatID)
				if s.State == usecase.StateRequestPhone {
					candidate := rawText
					if looksLikePhone(candidate) {
						s.Phone = candidate
						h.saveAndSendLead(chatID, s)
						go func(id int64) {
							time.Sleep(2 * time.Minute)
							h.sessions[id] = &usecase.Session{State: usecase.StateStart}
						}(chatID)
						continue
					} else {
						h.sendText(chatID, "Похоже, это не номер телефона. Пришлите номер в формате +7XXXXXXXXXX или нажмите кнопку ‘Отправить номер’.")
						h.trackFunnel(chatID, s.State)
						continue
					}
				}
			}
		}

		s := h.getSession(chatID)
		reply := h.dialog.Handle(s, text)
		if text == usecase.StartBtn {
			h.sendText(chatID, "Несколько уточняющих вопросов, и мы отправим вам подходящее предложение уже через пару минут.")
		}
		if s.State == usecase.StateRequestPhone {
			btn := tgbotapi.NewKeyboardButtonContact("Отправить номер")
			kb := tgbotapi.NewReplyKeyboard(tgbotapi.NewKeyboardButtonRow(btn))
			kb.ResizeKeyboard = true
			msg := tgbotapi.NewMessage(chatID, reply.Text)
			msg.ReplyMarkup = kb
			_, _ = h.bot.Send(msg)
			// Сразу приложим релевантный каталог (асинхронно с кэшем file_id)
			h.sendCatalogPDF(chatID, s)
			h.trackFunnel(chatID, s.State)
			continue
		}
		h.trackFunnel(chatID, s.State)
		h.applyReply(chatID, reply)

		// финального шага нет — очистку сессии выполняем после RequestPhone/LeadSaved
	}
}

// saveAndSendLead сохраняет лид, отправляет в MacroCRM и уведомляет пользователя
func (h *Handler) saveAndSendLead(chatID int64, s *usecase.Session) {
	if s == nil {
		return
	}
	if h.leadRepo != nil {
		ld := domain.Lead{ChatID: chatID, Purpose: s.Purpose, Bedrooms: s.Bedrooms, Payment: s.Payment, Phone: s.Phone}
		if err := h.leadRepo.SaveLead(ld); err != nil {
			if h.logger != nil {
				h.logger.Error("lead save failed", "chat_id", chatID, "error", err)
			}
		} else {
			if h.logger != nil {
				h.logger.Info("lead saved", "chat_id", chatID)
			}
		}
		if h.leadDelivery != nil {
			go func(id int64, ld domain.Lead) {
				if h.logger != nil {
					h.logger.Info("macrocrm send start", "chat_id", id)
				}
				if err := h.leadDelivery.SendLead(context.Background(), ld); err != nil {
					if h.logger != nil {
						h.logger.Error("macrocrm send failed", "chat_id", id, "error", err)
					}
				} else {
					if h.logger != nil {
						h.logger.Info("macrocrm send success", "chat_id", id)
					}
				}
			}(chatID, ld)
		}
	}
	h.trackFunnel(chatID, usecase.StateLeadSaved)
	h.sendTextRemoveKeyboard(chatID, "Спасибо! Мы получили ваш номер. Наш эксперт свяжется с вами в ближайшее время.")
}

func (h *Handler) isAdmin(chatID int64) bool {
	if len(h.adminIDs) == 0 {
		return false
	}
	_, ok := h.adminIDs[chatID]
	return ok
}

func (h *Handler) getSession(chatID int64) *usecase.Session {
	if s, ok := h.sessions[chatID]; ok {
		return s
	}
	s := &usecase.Session{State: usecase.StateStart}
	h.sessions[chatID] = s
	return s
}

func (h *Handler) getBSession(chatID int64) *usecase.BroadcastSession {
	if s, ok := h.bcastSessions[chatID]; ok {
		return s
	}
	s := &usecase.BroadcastSession{State: usecase.BStateIdle}
	h.bcastSessions[chatID] = s
	return s
}

func (h *Handler) applyReply(chatID int64, r usecase.Reply) {
	if r.RemoveKeyboard {
		msg := tgbotapi.NewMessage(chatID, r.Text)
		msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
		_, _ = h.bot.Send(msg)
		// Попробуем отправить релевантный PDF каталог
		s := h.getSession(chatID)
		h.sendCatalogPDF(chatID, s)
		return
	}
	if len(r.Options) > 0 {
		h.sendTextWithKeyboard(chatID, r.Text, r.Options)
		// Если следующий шаг — запрос телефона, всё равно приложим каталог прямо сейчас
		if r.AdvanceTo == usecase.StateRequestPhone {
			s := h.getSession(chatID)
			h.sendCatalogPDF(chatID, s)
		}
		return
	}
	// Финального шага выбора канала больше нет
	h.sendText(chatID, r.Text)
}

// sendCatalogPDF отправляет документ из папки collections согласно текущему выбору пользователя
func (h *Handler) sendCatalogPDF(chatID int64, s *usecase.Session) {
	filePath := usecase.CatalogFileFor(s)
	if strings.TrimSpace(filePath) == "" {
		return
	}
	go func(path string) {
		// try send preview image before PDF if exists
		base := strings.TrimSuffix(path, filepath.Ext(path))
		for _, ext := range []string{".jpg", ".jpeg", ".png"} {
			preview := base + ext
			if _, err := os.Stat(preview); err == nil {
				// cached photo id?
				h.catalogMu.RLock()
				cachedPhoto := h.catalogPhotoID[preview]
				h.catalogMu.RUnlock()
				if strings.TrimSpace(cachedPhoto) != "" {
					photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(cachedPhoto))
					if _, err := h.bot.Send(photo); err == nil {
						if h.logger != nil {
							h.logger.Info("catalog preview sent via cache", "chat_id", chatID, "file", preview)
						}
						break
					}
					if h.logger != nil {
						h.logger.Warn("cached photo_id failed, uploading preview", "chat_id", chatID, "file", preview)
					}
				}
				// upload preview
				photo := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(preview))
				msg, err := h.bot.Send(photo)
				if err == nil {
					if len(msg.Photo) > 0 {
						// take last size id
						sizes := msg.Photo
						id := sizes[len(sizes)-1].FileID
						if strings.TrimSpace(id) != "" {
							h.catalogMu.Lock()
							h.catalogPhotoID[preview] = id
							h.catalogMu.Unlock()
						}
					}
					if h.logger != nil {
						h.logger.Info("catalog preview sent", "chat_id", chatID, "file", preview)
					}
				} else {
					if h.logger != nil {
						h.logger.Error("send catalog preview failed", "chat_id", chatID, "file", preview, "error", err)
					}
				}
				break
			}
		}
		// try cached file_id first
		h.catalogMu.RLock()
		cachedID := h.catalogFileID[path]
		h.catalogMu.RUnlock()

		if strings.TrimSpace(cachedID) != "" {
			if h.logger != nil {
				h.logger.Info("catalog pdf send via cache", "chat_id", chatID, "file", path)
			}
			doc := tgbotapi.NewDocument(chatID, tgbotapi.FileID(cachedID))
			if _, err := h.bot.Send(doc); err == nil {
				return
			}
			// fallback to upload if cached id failed
			if h.logger != nil {
				h.logger.Warn("cached file_id failed, uploading file", "chat_id", chatID, "file", path)
			}
		}

		// upload file and cache file_id
		doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(path))
		msg, err := h.bot.Send(doc)
		if err != nil {
			if h.logger != nil {
				h.logger.Error("send catalog pdf failed", "chat_id", chatID, "file", path, "error", err)
			}
			return
		}
		if msg.Document != nil && strings.TrimSpace(msg.Document.FileID) != "" {
			h.catalogMu.Lock()
			h.catalogFileID[path] = msg.Document.FileID
			h.catalogMu.Unlock()
		}
		if h.logger != nil {
			h.logger.Info("catalog pdf sent", "chat_id", chatID, "file", path)
		}
	}(filePath)
}

func (h *Handler) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	_, _ = h.bot.Send(msg)
}

func (h *Handler) sendTextWithKeyboard(chatID int64, text string, opts []string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if len(opts) > 0 {
		msg.ReplyMarkup = inlineKeyboard(opts)
	}
	_, _ = h.bot.Send(msg)
}

func (h *Handler) sendTextRemoveKeyboard(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	_, _ = h.bot.Send(msg)
}

func inlineKeyboard(opts []string) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(opts))
	for _, o := range opts {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(o, o),
		))
	}
	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// простая эвристика валидации телефона
func looksLikePhone(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// допускаем +7, 7, 8 в начале и 10-11 цифр суммарно
	digits := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if strings.HasPrefix(s, "+7") && digits == 11 {
		return true
	}
	if (strings.HasPrefix(s, "8") || strings.HasPrefix(s, "7")) && digits == 11 {
		return true
	}
	if digits == 10 { // без кода страны
		return true
	}
	return false
}

// Реализация отправителя для юзкейсов
type Sender struct{ bot *tgbotapi.BotAPI }

func NewSender(bot *tgbotapi.BotAPI) *Sender { return &Sender{bot: bot} }

func (s *Sender) SendText(chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := s.bot.Send(msg)
	return err
}

func (s *Sender) SendPhoto(chatID int64, fileID string, caption string) error {
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(fileID))
	photo.Caption = caption
	_, err := s.bot.Send(photo)
	return err
}

func (h *Handler) sendFunnelChart(chatID int64, labels []string, values []int) error {
	bars := make([]chart.Value, 0, len(labels))
	maxVal := 0
	for i := range labels {
		v := values[i]
		if v > maxVal {
			maxVal = v
		}
		bars = append(bars, chart.Value{Value: float64(v), Label: labels[i]})
	}
	// Избежать ошибки invalid data range при нулевых значениях
	yMax := float64(maxVal)
	if yMax <= 0 {
		yMax = 1
	}
	graph := chart.BarChart{
		Width:    1100,
		Height:   600,
		BarWidth: 56,
		Background: chart.Style{Padding: chart.Box{
			Top:    50,
			Left:   16,
			Right:  16,
			Bottom: 0,
		}},
		YAxis: chart.YAxis{Range: &chart.ContinuousRange{Min: 0, Max: yMax}},
		Bars:  bars,
	}
	buf := bytes.NewBuffer(nil)
	if err := graph.Render(chart.PNG, buf); err != nil {
		return err
	}
	fname := "funnel_" + strconv.FormatInt(time.Now().UnixNano(), 10) + ".png"
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{Name: fname, Bytes: buf.Bytes()})
	_, err := h.bot.Send(photo)
	return err
}
