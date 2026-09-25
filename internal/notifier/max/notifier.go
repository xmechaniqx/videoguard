// Package max предоставляет реализацию уведомителя для MAX (Telegram-подобный мессенджер).
//
// Использует Telegram Bot API для отправки уведомлений.
// Документация: https://core.telegram.org/bots/api
package max

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"videoguard/internal/events"
)

// Notifier отправляет уведомления через MAX (Telegram Bot API).
type Notifier struct {
	botToken   string
	chatID     string
	retryMax   int
	retryDelay time.Duration
	client     *http.Client
}

// NewNotifier создаёт новый уведомитель MAX.
func NewNotifier(botToken, chatID string, retryMax int, retryDelay time.Duration) *Notifier {
	return &Notifier{
		botToken:   botToken,
		chatID:     chatID,
		retryMax:   retryMax,
		retryDelay: retryDelay,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetHTTPClient устанавливает кастомный HTTP-клиент (для тестов).
func (n *Notifier) SetHTTPClient(client *http.Client) {
	n.client = client
}

// Name возвращает имя уведомителя.
func (n *Notifier) Name() string {
	return "max"
}

// Notify отправляет уведомление о событии.
func (n *Notifier) Notify(ctx context.Context, event *events.Event) error {
	// Валидация параметров
	if n.botToken == "" {
		return fmt.Errorf("пустой bot token")
	}
	if n.chatID == "" {
		return fmt.Errorf("пустой chat_id")
	}

	// Сформировать текст сообщения
	text := n.formatMessage(event)

	// Отправить сообщение
	var err error
	for attempt := 0; attempt <= n.retryMax; attempt++ {
		err = n.sendTextMessage(ctx, text)
		if err == nil {
			if attempt > 0 {
				fmt.Printf("[MAX] Уведомление отправлено после %d попыток\n", attempt)
			}
			return nil
		}

		if attempt < n.retryMax {
			fmt.Printf("[MAX] Попытка %d/%d не удалась: %v\n", attempt+1, n.retryMax, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(n.retryDelay):
				// Подождать перед следующей попыткой
			}
		}
	}

	return fmt.Errorf("не удалось отправить уведомление после %d попыток: %w", n.retryMax, err)
}

// formatMessage формирует текст сообщения о событии.
func (n *Notifier) formatMessage(event *events.Event) string {
	timeStr := event.StartedAt.Format("02.01.2006 15:04:05")

	var title string
	switch event.Type {
	case events.EventTypeMotion:
		title = "🚨 Обнаружено движение"
	case events.EventTypeCameraOnline:
		title = "✅ Камера доступна"
	case events.EventTypeCameraOffline:
		title = "❌ Камера недоступна"
	case events.EventTypeCameraReconnecting:
		title = "🔄 Камера переподключается"
	case events.EventTypeRecordingStarted:
		title = "🎥 Начата запись"
	case events.EventTypeRecordingStopped:
		title = "⏹ Запись остановлена"
	case events.EventTypeStorageWarning:
		title = "⚠️ Предупреждение о хранилище"
	case events.EventTypeStorageFull:
		title = "🔴 Хранилище заполнено"
	case events.EventTypeSystemError:
		title = "💥 Ошибка системы"
	default:
		title = string(event.Type)
	}

	msg := fmt.Sprintf("%s\n\n", title)
	msg += fmt.Sprintf("Камера: %s\n", event.CameraID)
	msg += fmt.Sprintf("Время: %s\n", timeStr)
	msg += fmt.Sprintf("Тип: %s\n", event.Type)

	if event.Source != "" {
		msg += fmt.Sprintf("Источник: %s\n", event.Source)
	}

	return msg
}

// sendTextMessage отправляет текстовое сообщение через Telegram Bot API.
func (n *Notifier) sendTextMessage(ctx context.Context, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.botToken)

	// TODO: реализовать отправку JSON-пейлоада
	// Для простоты пока используем form-data
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(nil))
	if err != nil {
		return fmt.Errorf("создать запрос: %w", err)
	}

	// Отправить как form-encoded
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Сформировать form-encoded тело
	formData := fmt.Sprintf("chat_id=%s&text=%s", n.chatID, text)
	req.Body = io.NopCloser(bytes.NewBufferString(formData))

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("отправить запрос: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("неожиданный статус: %d, тело: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SendPhoto отправляет фото (снимок) через Telegram Bot API.
func (n *Notifier) SendPhoto(ctx context.Context, photoPath string, caption string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", n.botToken)

	file, err := os.Open(photoPath)
	if err != nil {
		return fmt.Errorf("открыть фото: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("photo", filepath.Base(photoPath))
	io.Copy(part, file)

	if caption != "" {
		writer.WriteField("caption", caption)
	}
	writer.WriteField("chat_id", n.chatID)
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("создать запрос: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("отправить запрос: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("неожиданный статус: %d, тело: %s", resp.StatusCode, string(body))
	}

	return nil
}
