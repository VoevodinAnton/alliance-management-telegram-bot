package main

import (
	"log/slog"
	"net/http"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	telegramAdapter "alliance-management-telegram-bot/internal/adapter/telegram"
	"alliance-management-telegram-bot/internal/infra/macrocrm"
	sqliteRepo "alliance-management-telegram-bot/internal/infra/sqlite"
	"alliance-management-telegram-bot/internal/usecase"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
	sender := telegramAdapter.NewSender(bot)
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
		handler.SetMacroCRMClient(macroClient)
	}
	handler.Run()
}
