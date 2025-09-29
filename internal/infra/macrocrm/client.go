package macrocrm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"alliance-management-telegram-bot/internal/domain"
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

	// Полезные поля заявки
	form.Set("phone", lead.Phone)
	// Имя можем не знать; оставим пустым или возьмем из Purpose, если это имя — но пока пусто
	form.Set("name", "Тест")
	// Сформируем читабельное сообщение без указания chat_id
	msg := fmt.Sprintf("Заявка из Telegram\nЦель: %s\nСпальни: %s\nОплата: %s", lead.Purpose, lead.Bedrooms, lead.Payment)
	form.Set("message", msg)

	endpoint := strings.TrimRight(c.BaseURL, "/") + "/estate/request/"
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
	// MacroCRM обычно возвращает 200; считаем успешным любой 2xx
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("macrocrm non-2xx: %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
