package main

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	telegramAdapter "zim-gallery-bot/internal/adapter/telegram"
	"zim-gallery-bot/internal/infra/macrocrm"
	sqliteRepo "zim-gallery-bot/internal/infra/sqlite"
	"zim-gallery-bot/internal/infra/telelog"
	"zim-gallery-bot/internal/usecase"
)

func main() {
	// Логгер: stdout + опционально в Telegram
	baseHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	var logHandler slog.Handler = baseHandler
	if chat := os.Getenv("LOG_CHAT_ID"); chat != "" {
		if id, err := strconv.ParseInt(chat, 10, 64); err == nil {
			// Bot API уже создадим ниже и подменим логгер после инициализации
			// временно используем базовый, потом пересоздадим logger
			_ = id
		}
	}
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		logger.Error("env TELEGRAM_BOT_TOKEN is not set")
		os.Exit(1)
	}

	go func() {
		_ = http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}))
	}()

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		logger.Error("failed to create bot", "error", err)
		os.Exit(1)
	}
	bot.Debug = false

	// Если указан LOG_CHAT_ID — добавим телеграм-хендлер
	if chat := os.Getenv("LOG_CHAT_ID"); chat != "" {
		if id, err := strconv.ParseInt(chat, 10, 64); err == nil {
			// Поддержка отдельного бота для логов
			logBot := bot
			if logTok := os.Getenv("LOG_BOT_TOKEN"); strings.TrimSpace(logTok) != "" {
				if lb, err := tgbotapi.NewBotAPI(logTok); err == nil {
					logBot = lb
				} else {
					logger.Warn("log bot init failed, fallback to main bot", "error", err)
				}
			}
			th := telelog.NewTelegramHandler(logBot, id, slog.LevelInfo)
			mh := telelog.NewMultiHandler(baseHandler, th)
			logger = slog.New(mh)
			slog.SetDefault(logger)
		}
	}

	// Лог после настройки телеграм-хендлера, чтобы он попал в лог-чат
	logger.Info("bot authorized", "username", bot.Self.UserName)

	// SQLite DSN для всех хранилищ
	dsn := os.Getenv("LEADS_SQLITE_DSN")
	if dsn == "" {
		dsn = "leads.db"
	}

	// Репозитории
	userRepo, err := sqliteRepo.NewUserRepo(dsn)
	if err != nil {
		logger.Error("users sqlite init error", "error", err)
		os.Exit(1)
	}
	dialog := usecase.NewDialog()
	// token repo for persistent inline-button tokens
	tokenRepo, err := sqliteRepo.NewTokenRepo(dsn)
	if err != nil {
		logger.Error("token sqlite init error", "error", err)
		os.Exit(1)
	}
	sender := telegramAdapter.NewSender(bot, tokenRepo)
	statRepo, err := sqliteRepo.NewBroadcastStatRepo(dsn)
	if err != nil {
		logger.Error("broadcast stat sqlite init error", "error", err)
		os.Exit(1)
	}
	broadcastUC := usecase.NewBroadcastUsecase(userRepo, sender, statRepo)
	funnelSQLRepo, err := sqliteRepo.NewFunnelRepo(dsn)
	if err != nil {
		logger.Error("funnel sqlite init error", "error", err)
		os.Exit(1)
	}
	funnelUC := usecase.NewFunnelUsecase(funnelSQLRepo)
	leadRepo, err := sqliteRepo.NewLeadRepo(dsn)
	if err != nil {
		logger.Error("leads sqlite init error", "error", err)
		os.Exit(1)
	}

	// MacroCRM client
	macroDomain := os.Getenv("MACROCRM_DOMAIN")
	macroSecret := os.Getenv("MACROCRM_APP_SECRET")
	macroBase := os.Getenv("MACROCRM_BASE_URL") // опционально, по умолчанию официальный хост
	var macroClient *macrocrm.Client
	if macroDomain != "" && macroSecret != "" {
		opts := []func(*macrocrm.Client){}
		if macroBase != "" {
			opts = append(opts, macrocrm.WithBaseURL(macroBase))
		}
		macroClient = macrocrm.NewClient(macroDomain, macroSecret, opts...)
	} else {
		logger.Warn("macrocrm is not configured: set MACROCRM_DOMAIN and MACROCRM_APP_SECRET to enable CRM sending")
	}

	adminIDs := telegramAdapter.ParseAdminIDsFromEnv()
	handler := telegramAdapter.NewHandler(bot, dialog, userRepo, broadcastUC, adminIDs, funnelUC, logger)
	handler.SetLeadRepository(leadRepo)
	if macroClient != nil {
		// внедряем как абстракцию доставки лида
		handler.SetLeadDelivery(macroClient)
	}
	handler.Run()
}
