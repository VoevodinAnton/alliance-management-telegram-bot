package telegram

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	chart "github.com/wcharczuk/go-chart/v2"

	"zim-gallery-bot/internal/domain"
	"zim-gallery-bot/internal/usecase"
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
			// Отметим промо-переход (deep-link /start promo)
			if strings.HasPrefix(text, "/start ") {
				arg := strings.TrimSpace(strings.TrimPrefix(text, "/start "))
				if strings.HasPrefix(arg, "promo") { // поддержка promo, promo_*
					s := h.getSession(chatID)
					s.Promo = true
					s.PromoTag = arg
				}
			}
		} else if update.CallbackQuery != nil {
			chatID = update.CallbackQuery.Message.Chat.ID
			text = update.CallbackQuery.Data
			// handle inline button for PDF: callback data format BPDF:<token>
			if strings.HasPrefix(text, "BPDF:") {
				token := strings.TrimPrefix(text, "BPDF:")
				// lookup real file_id from Sender's registry
				if sender, ok := h.broadcastUC.Sender.(*Sender); ok {
					if fileID, ok2 := sender.LookupButton(token); ok2 {
						// answer callback to remove loading indicator
						cb := tgbotapi.NewCallback(update.CallbackQuery.ID, "Отправляю файл")
						_, _ = h.bot.Request(cb)
						// Send the PDF to the user who clicked (From.ID)
						to := update.CallbackQuery.From.ID
						doc := tgbotapi.NewDocument(to, tgbotapi.FileID(fileID))
						if _, err := h.bot.Send(doc); err != nil {
							if h.logger != nil {
								h.logger.Error("send button pdf failed", "chat_id", to, "error", err)
							}
						}
						continue
					}
				}
				// token not found — answer callback with message
				cb := tgbotapi.NewCallback(update.CallbackQuery.ID, "Файл недоступен")
				_, _ = h.bot.Request(cb)
				continue
			}
		}
		// сохраняем только не-админов; перед этим узнаем, был ли пользователь ранее
		knownUser := false
		if !h.isAdmin(chatID) {
			if h.userRepo != nil {
				if ok, err := h.userRepo.HasUser(chatID); err == nil {
					knownUser = ok
				}
			}
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
			msg.ReplyMarkup = inlineKeyboard([]string{"Создать рассылку", "Удалить последнюю рассылку", "Статистика рассылок", "Воронка", "DAU"})
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
			if text == "Статистика рассылок" {
				h.sendText(chatID, h.broadcastUC.StatsSummary(5))
				continue
			}
			if text == "Удалить последнюю рассылку" {
				msg, err := h.broadcastUC.DeleteLastBroadcast()
				h.sendText(chatID, msg)
				if err != nil && h.logger != nil {
					h.logger.Error("broadcast delete errors", "chat_id", chatID, "error", err)
				}
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
			if text == "DAU" {
				if h.funnel != nil {
					labels, values := h.funnel.DailyActive(7)
					if err := h.sendFunnelChart(chatID, labels, values); err != nil {
						if h.logger != nil {
							h.logger.Error("dau chart failed", "error", err)
						}
						// fallback в текстовом виде
						var b strings.Builder
						b.WriteString("DAU за 14 дней:\n")
						for i := range labels {
							fmt.Fprintf(&b, "%s — %d\n", labels[i], values[i])
						}
						h.sendText(chatID, b.String())
					}
				} else {
					h.sendText(chatID, "DAU недоступен")
				}
				continue
			}
			if s := h.bcastSessions[chatID]; s != nil {
				if m := update.Message; m != nil && len(m.Photo) > 0 {
					ph := m.Photo[len(m.Photo)-1]
					fileID := ph.FileID
					caption := formatHTMLWithEntities(m.Caption, m.CaptionEntities)
					msg, opts := h.broadcastUC.ReceivePhoto(s, fileID, caption)
					h.sendTextWithKeyboard(chatID, msg, opts)
					continue
				}
				if m := update.Message; m != nil && m.Document != nil {
					fileID := m.Document.FileID
					caption := formatHTMLWithEntities(m.Caption, m.CaptionEntities)
					msg, opts := h.broadcastUC.ReceiveDocument(s, fileID, caption)
					h.sendTextWithKeyboard(chatID, msg, opts)
					continue
				}
				if s.State == usecase.BStateConfirm && strings.TrimSpace(text) != "" {
					handled, msg, opts, err := h.broadcastUC.TryAttachButtonLink(s, text)
					if handled {
						h.sendTextWithKeyboard(chatID, msg, opts)
						if err != nil && h.logger != nil {
							h.logger.Warn("broadcast button link rejected", "chat_id", chatID, "error", err)
						}
						continue
					}
				}
				switch s.State {
				case usecase.BStateEnter:
					// Принимаем текст только если он не пустой; иначе ждём фото/документ
					if strings.TrimSpace(text) != "" {
						formattedText := text
						if update.Message != nil {
							formattedText = formatHTMLWithEntities(update.Message.Text, update.Message.Entities)
						}
						msg, opts, _ := h.broadcastUC.ReceiveText(s, formattedText)
						h.sendTextWithKeyboard(chatID, msg, opts)
						continue
					}
					// пустой текст — игнорируем, ждём контент
					continue
				case usecase.BStateConfirm:
					msg, err := h.broadcastUC.ConfirmSend(s, text)
					h.sendTextRemoveKeyboard(chatID, msg)
					if err != nil {
						if h.logger != nil {
							h.logger.Error("broadcast send errors", "chat_id", chatID, "error", err)
						}
					} else {
						if h.logger != nil {
							h.logger.Info("broadcast confirm", "chat_id", chatID)
						}
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
				// Попробуем собрать имя из Telegram профиля
				if update.Message.From != nil {
					name := strings.TrimSpace(strings.TrimSpace(update.Message.From.FirstName + " " + update.Message.From.LastName))
					if name == "" {
						name = strings.TrimPrefix(update.Message.From.UserName, "@")
					}
					if s != nil {
						s.Name = name
					}
				}
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
						if update.Message.From != nil && strings.TrimSpace(s.Name) == "" {
							name := strings.TrimSpace(strings.TrimSpace(update.Message.From.FirstName + " " + update.Message.From.LastName))
							if name == "" {
								name = strings.TrimPrefix(update.Message.From.UserName, "@")
							}
							s.Name = name
						}
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
		// Спец-флоу промо: если /start promo
		if s.Promo && strings.HasPrefix(text, "/start ") && knownUser {
			// Промо флоу только для уже существующих пользователей (проверено до SaveUser)
			if !knownUser {
				// Новый пользователь — идёт по обычной воронке
				s.Promo = false
			} else {
				// если телефон есть — сразу предложим слоты
				hasPhone := false
				if h.leadRepo != nil {
					if ok, err := h.leadRepo.HasPhone(chatID); err == nil {
						hasPhone = ok
					}
				}
				if hasPhone {
					promoText := "Добрый день! Вас заинтересовала наша эксклюзивная финансовая программа. Давайте свяжу вас с нашим адвайзером: расскажет об условиях, ответит на вопросы и рассчитает выгоду с учетом привилегий. В какое время вам удобно завтра принять звонок?"
					slots := []string{"11:00-13:00", "14:00-16:00", "17:00-19:00"}
					h.sendTextWithKeyboard(chatID, promoText, slots)
					// если номер есть — сохранять лид будем после выбора слота текстом
					s.State = usecase.StateFinalMessage
					h.trackFunnel(chatID, s.State)
					continue
				}
				// нет телефона — идем по обычной воронке (приветствие + кнопка "Хочу")
				s.Promo = false
			}
		}

		// Обработка /start (включая /start promo): всегда отправляем приветствие + кнопку "Хочу"
		if strings.HasPrefix(text, "/start") {
			// Принудительно обработаем как обычный старт
			// Это установит s.State = StateIntro и вернет приветствие
			startReply := h.dialog.Handle(s, "/start")
			msg := tgbotapi.NewMessage(chatID, startReply.Text)
			msg.ParseMode = tgbotapi.ModeHTML
			_, _ = h.bot.Send(msg)
			h.sendTextWithKeyboard(chatID, "Несколько уточняющих вопросов, и мы отправим вам подходящее предложение уже через пару минут.", []string{usecase.StartBtn})
			h.trackFunnel(chatID, s.State)
			// Сбрасываем промо-флаг, чтобы не показывать слоты после обычного старта
			s.Promo = false
			continue
		}

		// Обработка выбора слота времени (независимо от promo)
		if text == "11:00-13:00" || text == "14:00-16:00" || text == "17:00-19:00" {
			// Сохраним выбор слота в БД
			if h.leadRepo != nil {
				if err := h.leadRepo.UpdateLastLeadSlotAndSource(chatID, text, s.PromoTag); err != nil {
					if h.logger != nil {
						h.logger.Error("update slot/source failed", "chat_id", chatID, "error", err)
					}
				}
			}
			// Проверим актуально ли запрашивать номер прямо сейчас
			phone := ""
			if h.leadRepo != nil {
				phone, _ = h.leadRepo.GetLastPhone(chatID)
			}
			if strings.TrimSpace(phone) == "" {
				// Номера нет — идем по воронке, запросим по стандартному сценарию
				s.Promo = false
				h.trackFunnel(chatID, s.State)
				// Никаких специальных сообщений — управление вернется в диалог ниже
				// чтобы пользователь получил стандартные вопросы
				continue
			}
			// Телефон есть — подтверждаем и отправляем лид с указанным слотом
			h.sendText(chatID, fmt.Sprintf("Отлично! Отмечу время %s. Наш адвайзер свяжется с вами завтра в выбранный промежуток.", text))
			if h.leadDelivery != nil {
				// Попробуем определить имя для CRM: из сохранённой сессии или из апдейта Telegram
				name := strings.TrimSpace(s.Name)
				if name == "" {
					if update.Message != nil && update.Message.From != nil {
						name = strings.TrimSpace(strings.TrimSpace(update.Message.From.FirstName + " " + update.Message.From.LastName))
						if name == "" {
							name = strings.TrimPrefix(update.Message.From.UserName, "@")
						}
					} else if update.CallbackQuery != nil && update.CallbackQuery.From != nil {
						from := update.CallbackQuery.From
						name = strings.TrimSpace(strings.TrimSpace(from.FirstName + " " + from.LastName))
						if name == "" {
							name = strings.TrimPrefix(from.UserName, "@")
						}
					}
				}
				ld := domain.Lead{ChatID: chatID, Phone: phone, Slot: text, Source: s.PromoTag, Name: name}
				go func(id int64, lead domain.Lead) {
					if h.logger != nil {
						h.logger.Info("macrocrm send start", "chat_id", id)
					}
					if err := h.leadDelivery.SendLead(context.Background(), lead); err != nil {
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
			go func(id int64) {
				time.Sleep(2 * time.Minute)
				h.sessions[id] = &usecase.Session{State: usecase.StateStart}
			}(chatID)
			continue
		}
		reply := h.dialog.Handle(s, text)
		if s.State == usecase.StateRequestPhone {
			btn := tgbotapi.NewKeyboardButtonContact("Отправить номер")
			kb := tgbotapi.NewReplyKeyboard(tgbotapi.NewKeyboardButtonRow(btn))
			kb.ResizeKeyboard = true
			msg := tgbotapi.NewMessage(chatID, reply.Text)
			msg.ParseMode = tgbotapi.ModeHTML
			msg.ReplyMarkup = kb
			_, _ = h.bot.Send(msg)
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
		// Подставляем slot только для промо-сценария, чтобы не тянуть старый слот в новую воронку
		slot := ""
		if strings.TrimSpace(s.PromoTag) != "" {
			if v, err := h.leadRepo.GetLastSlot(chatID); err == nil {
				slot = v
			}
		}
		ld := domain.Lead{ChatID: chatID, Purpose: s.Purpose, Bedrooms: s.Bedrooms, Payment: s.Payment, Phone: s.Phone, Slot: slot, Source: s.PromoTag, Name: s.Name}
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
	h.sendTextRemoveKeyboard(chatID, "Спасибо! Мы получили номер.\n В ближайшее время с вами свяжется адвайзер и предложит персональные условия по вашим параметрам.")
	if h.catalogsEnabled() {
		h.sendCatalogPDF(chatID, s)
	}
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

// catalogsEnabled returns true when catalog PDF sending is enabled via
// the CONTEXT7 environment flag. By default catalogs are disabled.
func (h *Handler) catalogsEnabled() bool {
	return os.Getenv("sendCatalogPDF") == "1"
}

func (h *Handler) applyReply(chatID int64, r usecase.Reply) {
	if r.RemoveKeyboard {
		msg := tgbotapi.NewMessage(chatID, r.Text)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
		_, _ = h.bot.Send(msg)
		return
	}
	if len(r.Options) > 0 {
		h.sendTextWithKeyboard(chatID, r.Text, r.Options)
		return
	}
	// Финального шага выбора канала больше нет
	h.sendText(chatID, r.Text)
}

// sendCatalogPDF отправляет документ из папки collections согласно текущему выбору пользователя
// NOTE: catalogs are sent only after the user provided a phone number in the session.
func (h *Handler) sendCatalogPDF(chatID int64, s *usecase.Session) {
	if s == nil {
		return
	}
	if strings.TrimSpace(s.Phone) == "" {
		return
	}
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
	msg.ParseMode = tgbotapi.ModeHTML
	_, _ = h.bot.Send(msg)
}

func (h *Handler) sendTextWithKeyboard(chatID int64, text string, opts []string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	if len(opts) > 0 {
		msg.ReplyMarkup = inlineKeyboard(opts)
	}
	_, _ = h.bot.Send(msg)
}

func (h *Handler) sendTextRemoveKeyboard(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
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

// formatHTMLWithEntities converts Telegram entities to HTML markup so formatting is preserved on broadcast.
// Offsets/lengths from Telegram are in UTF-16 code units, so we map them back to rune indexes before applying tags.
func formatHTMLWithEntities(text string, entities []tgbotapi.MessageEntity) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if len(entities) == 0 {
		return text
	}

	startTags := make(map[int][]string)
	endTags := make(map[int][]string)

	for _, e := range entities {
		start := utf16IndexToRuneIndex(text, e.Offset)
		end := utf16IndexToRuneIndex(text, e.Offset+e.Length)
		if start < 0 || end < start {
			continue
		}
		open, close := tagsForEntity(e)
		if open == "" && close == "" {
			continue
		}
		startTags[start] = append(startTags[start], open)
		endTags[end] = append(endTags[end], close)
	}

	runes := []rune(text)
	var b strings.Builder
	for i := 0; i <= len(runes); i++ {
		if closes := endTags[i]; len(closes) > 0 {
			for j := len(closes) - 1; j >= 0; j-- {
				b.WriteString(closes[j])
			}
		}
		if opens := startTags[i]; len(opens) > 0 {
			for _, tag := range opens {
				b.WriteString(tag)
			}
		}
		if i < len(runes) {
			// Escape plain text to avoid breaking the HTML we add around entities.
			b.WriteString(html.EscapeString(string(runes[i])))
		}
	}
	return b.String()
}

func tagsForEntity(e tgbotapi.MessageEntity) (string, string) {
	switch strings.ToLower(e.Type) {
	case "bold":
		return "<b>", "</b>"
	case "italic":
		return "<i>", "</i>"
	case "underline":
		return "<u>", "</u>"
	case "strikethrough":
		return "<s>", "</s>"
	case "code":
		return "<code>", "</code>"
	case "pre":
		return "<pre>", "</pre>"
	case "spoiler":
		return "<tg-spoiler>", "</tg-spoiler>"
	case "text_link":
		if strings.TrimSpace(e.URL) == "" {
			return "", ""
		}
		return `<a href="` + html.EscapeString(e.URL) + `">`, "</a>"
	case "text_mention":
		if e.User == nil {
			return "", ""
		}
		return fmt.Sprintf(`<a href="tg://user?id=%d">`, e.User.ID), "</a>"
	default:
		return "", ""
	}
}

// utf16IndexToRuneIndex converts a UTF-16 code-unit offset to a rune index in the given string.
func utf16IndexToRuneIndex(text string, utf16Index int) int {
	if utf16Index <= 0 {
		return 0
	}
	runeIndex := 0
	count := 0
	for _, r := range text {
		if count >= utf16Index {
			break
		}
		count += len(utf16.Encode([]rune{r}))
		runeIndex++
	}
	return runeIndex
}

// Реализация отправителя для юзкейсов
type Sender struct {
	bot       *tgbotapi.BotAPI
	mu        sync.RWMutex
	buttonMap map[string]string
	store     TokenStore
}

func NewSender(bot *tgbotapi.BotAPI, store TokenStore) *Sender {
	return &Sender{bot: bot, buttonMap: make(map[string]string), store: store}
}

func (s *Sender) SendText(chatID int64, text string, parseMode string) (int, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	if strings.TrimSpace(parseMode) != "" {
		msg.ParseMode = parseMode
	} else {
		msg.ParseMode = tgbotapi.ModeHTML
	}
	sent, err := s.bot.Send(msg)
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

func (s *Sender) SendPhoto(chatID int64, fileID string, caption string, parseMode string) (int, error) {
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(fileID))
	photo.Caption = caption
	if strings.TrimSpace(parseMode) != "" {
		photo.ParseMode = parseMode
	}
	sent, err := s.bot.Send(photo)
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

func (s *Sender) SendDocument(chatID int64, fileID string, caption string, parseMode string) (int, error) {
	doc := tgbotapi.NewDocument(chatID, tgbotapi.FileID(fileID))
	doc.Caption = caption
	if strings.TrimSpace(parseMode) != "" {
		doc.ParseMode = parseMode
	}
	sent, err := s.bot.Send(doc)
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

func (s *Sender) SendWithInlineButton(chatID int64, text string, photoFileID string, docFileID string, caption string, parseMode string, buttonText string, buttonDocFileID string, buttonURL string) (int, error) {
	var btn tgbotapi.InlineKeyboardButton
	if strings.TrimSpace(buttonDocFileID) != "" {
		// Register short token for the button PDF and use token in callback data
		token := s.RegisterButton(buttonDocFileID)
		if strings.TrimSpace(token) == "" {
			return 0, fmt.Errorf("button file id empty")
		}
		btn = tgbotapi.NewInlineKeyboardButtonData(buttonText, "BPDF:"+token)
	} else if strings.TrimSpace(buttonURL) != "" {
		btn = tgbotapi.NewInlineKeyboardButtonURL(buttonText, buttonURL)
	} else {
		return 0, fmt.Errorf("button target empty")
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btn))

	if strings.TrimSpace(photoFileID) != "" {
		photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(photoFileID))
		photo.Caption = caption
		if strings.TrimSpace(parseMode) != "" {
			photo.ParseMode = parseMode
		}
		photo.ReplyMarkup = kb
		sent, err := s.bot.Send(photo)
		if err != nil {
			return 0, err
		}
		return sent.MessageID, nil
	}
	if strings.TrimSpace(docFileID) != "" {
		doc := tgbotapi.NewDocument(chatID, tgbotapi.FileID(docFileID))
		doc.Caption = caption
		if strings.TrimSpace(parseMode) != "" {
			doc.ParseMode = parseMode
		}
		doc.ReplyMarkup = kb
		sent, err := s.bot.Send(doc)
		if err != nil {
			return 0, err
		}
		return sent.MessageID, nil
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = kb
	if strings.TrimSpace(parseMode) != "" {
		msg.ParseMode = parseMode
	} else {
		msg.ParseMode = tgbotapi.ModeHTML
	}
	sent, err := s.bot.Send(msg)
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

func (s *Sender) DeleteMessage(chatID int64, messageID int) error {
	cfg := tgbotapi.DeleteMessageConfig{ChatID: chatID, MessageID: messageID}
	_, err := s.bot.Request(cfg)
	return err
}

// internal registry for short tokens -> file_id
func (s *Sender) RegisterButton(fileID string) string {
	if strings.TrimSpace(fileID) == "" {
		return ""
	}
	// generate 8-byte random token hex
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// fallback to timestamp-based token
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	if s.buttonMap == nil {
		s.buttonMap = make(map[string]string)
	}
	s.buttonMap[token] = fileID
	s.mu.Unlock()
	// persist mapping if store provided
	if s.store != nil {
		if err := s.store.SaveToken(token, fileID); err != nil {
			slog.Default().Warn("persist token failed", "token", token, "error", err)
		}
	}
	// log registration for debugging (uses global default logger)
	slog.Default().Info("registered button token", "token", token, "file_id", fileID)
	return token
}

func (s *Sender) LookupButton(token string) (string, bool) {
	s.mu.RLock()
	if s.buttonMap != nil {
		if v, ok := s.buttonMap[token]; ok {
			s.mu.RUnlock()
			return v, true
		}
	}
	s.mu.RUnlock()
	// fallback to persistent store
	if s.store != nil {
		fileID, ok, err := s.store.LookupToken(token)
		if err != nil {
			slog.Default().Warn("token lookup error", "token", token, "error", err)
			return "", false
		}
		if ok {
			// cache in memory
			s.mu.Lock()
			if s.buttonMap == nil {
				s.buttonMap = make(map[string]string)
			}
			s.buttonMap[token] = fileID
			s.mu.Unlock()
			return fileID, true
		}
	}
	slog.Default().Warn("button lookup failed", "token", token)
	return "", false
}

// TokenStore is an optional persistence interface for token->file_id mapping
type TokenStore interface {
	SaveToken(token, fileID string) error
	LookupToken(token string) (string, bool, error)
}

func (h *Handler) sendFunnelChart(chatID int64, labels []string, values []int) error {
	bars := make([]chart.Value, 0, len(labels))
	maxVal := 0
	for i := range labels {
		v := values[i]
		if v == 0 {
			continue
		}
		if v > maxVal {
			maxVal = v
		}
		bars = append(bars, chart.Value{Value: float64(v), Label: labels[i]})
	}
	// если все значения нулевые, показывать обычный график из нулей
	if len(bars) == 0 {
		for i := range labels {
			bars = append(bars, chart.Value{Value: float64(values[i]), Label: labels[i]})
		}
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
		YAxis: chart.YAxis{
			Range:          &chart.ContinuousRange{Min: 0, Max: yMax},
			ValueFormatter: chart.IntValueFormatter,
		},
		Bars: bars,
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
