package macrocrm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"zim-gallery-bot/internal/domain"
)

// Client отправляет лиды в MacroCRM (SberCRM)
type Client struct {
	// Базовый хост API, по умолчанию https://api.macro.sbercrm.com
	BaseURL    string
	Domain     string
	AppSecret  string
	Action     string
	HTTPClient *http.Client
}

func NewClient(domain, appSecret string, opts ...func(*Client)) *Client {
	c := &Client{
		BaseURL:    "https://api.macro.sbercrm.com",
		Domain:     domain,
		AppSecret:  appSecret,
		Action:     "question",
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func WithBaseURL(baseURL string) func(*Client) {
	return func(c *Client) {
		if strings.TrimSpace(baseURL) != "" {
			c.BaseURL = baseURL
		}
	}
}

func WithAction(action string) func(*Client) {
	return func(c *Client) {
		if strings.TrimSpace(action) != "" {
			c.Action = action
		}
	}
}

// SendLead формирует запрос на создание заявки в MacroCRM.
// Отправляет минимум телефон и текстовое описание, остальное — как есть из структуры лида.
// Реализация интерфейса usecase.LeadDelivery
func (c *Client) SendLead(ctx context.Context, lead domain.Lead) error {
	if c == nil {
		return errors.New("macrocrm client is nil")
	}
	if strings.TrimSpace(c.Domain) == "" || strings.TrimSpace(c.AppSecret) == "" {
		return errors.New("macrocrm domain/app_secret are not set")
	}
	if strings.TrimSpace(lead.Phone) == "" {
		return errors.New("lead phone is empty")
	}

	ts := time.Now().Unix()
	tsStr := strconv.FormatInt(ts, 10)
	token := md5Hex(c.Domain + tsStr + c.AppSecret)

	form := url.Values{}
	form.Set("domain", c.Domain)
	form.Set("time", tsStr)
	form.Set("token", token)
	form.Set("action", c.Action)
	if src := strings.TrimSpace(lead.Source); src != "" {
		// Передаем источник как несколько полей на всякий случай
		form.Set("source", src)
		form.Set("utm_source", src)
	}

	// Полезные поля заявки
	form.Set("phone", lead.Phone)
	// Имя пользователя, если известно
	if strings.TrimSpace(lead.Name) != "" {
		form.Set("name", lead.Name)
	}
	// Сформируем читабельное сообщение без указания chat_id
	extra := ""
	if strings.TrimSpace(lead.Slot) != "" {
		extra = "\nВремя звонка: " + lead.Slot
	}
	srcLine := ""
	if src := strings.TrimSpace(lead.Source); src != "" {
		srcLine = "\nИсточник: " + src
	}
	msg := fmt.Sprintf("Заявка из Telegram\nЦель: %s\nСпальни: %s\nОплата: %s%s%s", lead.Purpose, lead.Bedrooms, lead.Payment, extra, srcLine)
	form.Set("message", msg)

	endpoint := strings.TrimRight(c.BaseURL, "/") + "/estate/request/"

	// Логируем тело запроса (маскируем token) в человекочитаемом виде
	{
		masked := url.Values{}
		for k, vals := range form {
			if strings.EqualFold(k, "token") {
				masked[k] = []string{"***"}
				continue
			}
			vv := make([]string, len(vals))
			copy(vv, vals)
			masked[k] = vv
		}
		fields := map[string]string{
			"action":     masked.Get("action"),
			"domain":     masked.Get("domain"),
			"name":       masked.Get("name"),
			"phone":      masked.Get("phone"),
			"message":    masked.Get("message"),
			"source":     masked.Get("source"),
			"utm_source": masked.Get("utm_source"),
			"time":       masked.Get("time"),
			"token":      masked.Get("token"), // будет "***"
		}
		slog.Info("macrocrm send lead", "endpoint", endpoint, "params", fields)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Прочитаем тело для логики ошибок MacroCRM (иногда 200, но строкой ошибка)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyStr := strings.TrimSpace(string(body))
	slog.Info("macrocrm response", "status", resp.StatusCode, "body", bodyStr)
	// MacroCRM обычно возвращает 200; считаем успешным любой 2xx, но проверим содержание
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("macrocrm non-2xx: %d: %s", resp.StatusCode, bodyStr)
	}
	low := strings.ToLower(bodyStr)
	if strings.Contains(low, "error") || strings.Contains(low, "ошибка") || strings.Contains(low, "fail") {
		return fmt.Errorf("macrocrm 2xx but error body: %s", bodyStr)
	}
	return nil
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
