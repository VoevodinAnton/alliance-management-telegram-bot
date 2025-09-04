package main

import (
	"log"
	"net/http"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	telegramAdapter "alliance-management-telegram-bot/internal/adapter/telegram"
	memrepo "alliance-management-telegram-bot/internal/infra/memory"
	sqliteRepo "alliance-management-telegram-bot/internal/infra/sqlite"
	"alliance-management-telegram-bot/internal/usecase"
)

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("Переменная окружения TELEGRAM_BOT_TOKEN не задана")
	}

	go func() {
		_ = http.ListenAndServe(":8080", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}))
	}()

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("Ошибка создания бота: %v", err)
	}
	bot.Debug = false
	log.Printf("Авторизован как %s", bot.Self.UserName)

	// SQLite DSN для всех хранилищ
	dsn := os.Getenv("LEADS_SQLITE_DSN")
	if dsn == "" {
		dsn = "leads.db"
	}

	// Репозитории
	userRepo, err := sqliteRepo.NewUserRepo(dsn)
	if err != nil {
		log.Fatalf("users sqlite init error: %v", err)
	}
	dialog := usecase.NewDialog()
	sender := telegramAdapter.NewSender(bot)
	statRepo := memrepo.NewBroadcastStatRepo()
	broadcastUC := usecase.NewBroadcastUsecase(userRepo, sender, statRepo)
	funnelSQLRepo, err := sqliteRepo.NewFunnelRepo(dsn)
	if err != nil {
		log.Fatalf("funnel sqlite init error: %v", err)
	}
	funnelUC := usecase.NewFunnelUsecase(funnelSQLRepo)
	leadRepo, err := sqliteRepo.NewLeadRepo(dsn)
	if err != nil {
		log.Fatalf("sqlite init error: %v", err)
	}

	adminIDs := telegramAdapter.ParseAdminIDsFromEnv()
	handler := telegramAdapter.NewHandler(bot, dialog, userRepo, broadcastUC, adminIDs, funnelUC)
	handler.SetLeadRepository(leadRepo)
	handler.Run()
}
