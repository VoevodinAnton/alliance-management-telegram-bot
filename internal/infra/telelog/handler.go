package telelog

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramHandler отправляет логи в указанный чат Telegram
type TelegramHandler struct {
	bot     *tgbotapi.BotAPI
	chatID  int64
	level   slog.Level
	attrsMu sync.RWMutex
	attrs   []slog.Attr
}

func NewTelegramHandler(bot *tgbotapi.BotAPI, chatID int64, level slog.Level) *TelegramHandler {
	return &TelegramHandler{bot: bot, chatID: chatID, level: level}
}

func (h *TelegramHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *TelegramHandler) Handle(_ context.Context, r slog.Record) error {
	if h.bot == nil || h.chatID == 0 {
		return nil
	}
	// Соберём короткое сообщение
	// Формат: level time msg | key=val ...
	sb := make([]byte, 0, 256)
	sb = append(sb, '[')
	sb = append(sb, []byte(r.Level.String())...)
	sb = append(sb, ']', ' ')
	ts := time.Unix(0, r.Time.UnixNano()).Format("2006-01-02 15:04:05")
	sb = append(sb, []byte(ts)...)
	sb = append(sb, ' ')
	sb = append(sb, []byte(r.Message)...)

	// Атрибуты
	writeAttr := func(a slog.Attr) {
		if a.Equal(slog.Attr{}) {
			return
		}
		sb = append(sb, ' ')
		sb = append(sb, []byte(a.Key)...)
		sb = append(sb, '=')
		sb = append(sb, []byte(valueString(a.Value))...)
	}
	h.attrsMu.RLock()
	for _, a := range h.attrs {
		writeAttr(a)
	}
	h.attrsMu.RUnlock()
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(a)
		return true
	})

	text := string(sb)
	go func() {
		msg := tgbotapi.NewMessage(h.chatID, text)
		_, _ = h.bot.Send(msg)
	}()
	return nil
}

func (h *TelegramHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h2 := *h
	h2.attrsMu.Lock()
	h2.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	h2.attrsMu.Unlock()
	return &h2
}

func (h *TelegramHandler) WithGroup(_ string) slog.Handler { return h }

func valueString(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return strconv.FormatInt(v.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(v.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(v.Float64(), 'f', -1, 64)
	case slog.KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	default:
		return v.String()
	}
}

// MultiHandler фан-аут на два обработчика
type MultiHandler struct{ a, b slog.Handler }

func NewMultiHandler(a, b slog.Handler) *MultiHandler { return &MultiHandler{a: a, b: b} }

func (m *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return m.a.Enabled(ctx, level) || m.b.Enabled(ctx, level)
}

func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	_ = m.a.Handle(ctx, r)
	_ = m.b.Handle(ctx, r)
	return nil
}

func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &MultiHandler{a: m.a.WithAttrs(attrs), b: m.b.WithAttrs(attrs)}
}

func (m *MultiHandler) WithGroup(name string) slog.Handler {
	return &MultiHandler{a: m.a.WithGroup(name), b: m.b.WithGroup(name)}
}
