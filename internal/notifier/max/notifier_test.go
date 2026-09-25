package max

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"videoguard/internal/events"
)

// TestNewNotifier проверяет создание уведомителя MAX.
func TestNewNotifier(t *testing.T) {
	notifier := NewNotifier("test-token", "test-chat-id", 3, 5*time.Second)

	if notifier == nil {
		t.Fatal("ожидался уведомитель, получено nil")
	}

	if notifier.Name() != "max" {
		t.Errorf("ожидалось имя 'max', получено '%s'", notifier.Name())
	}
}

// TestNotifierFormatMessage проверяет форматирование сообщения.
func TestNotifierFormatMessage(t *testing.T) {
	notifier := NewNotifier("test-token", "test-chat-id", 3, 5*time.Second)

	event := &events.Event{
		ID:        "test-event",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Date(2026, 9, 20, 18, 42, 17, 0, time.UTC),
	}

	message := notifier.formatMessage(event)

	if !strings.Contains(message, "движение") {
		t.Error("ожидалось сообщение о движении")
	}

	if !strings.Contains(message, "camera-1") {
		t.Error("ожидалось имя камеры в сообщении")
	}

	if !strings.Contains(message, "20.09.2026") {
		t.Error("ожидалась дата в сообщении")
	}
}

// TestNotifierSendTextMessageValidation проверяет валидацию параметров для отправки.
func TestNotifierSendTextMessageValidation(t *testing.T) {
	notifier := NewNotifier("", "test-chat-id", 0, 0)

	event := &events.Event{
		ID:        "validation-test",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}

	ctx := context.Background()
	err := notifier.Notify(ctx, event)
	if err == nil {
		t.Error("ожидалась ошибка при пустом токене")
	}
}

// TestNotifierEmptyChatID проверяет обработку пустого chat_id.
func TestNotifierEmptyChatID(t *testing.T) {
	notifier := NewNotifier("test-token", "", 0, 0)

	event := &events.Event{
		ID:        "empty-chat-test",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}

	ctx := context.Background()
	err := notifier.Notify(ctx, event)
	if err == nil {
		t.Error("ожидалась ошибка при пустом chat_id")
	}
}

// TestNotifierMultipleRetries проверяет поведение с несколькими попытками.
// Тест делает реальный запрос к MAX API с таймаутом 1 сек и максимум 2 попытки.
func TestNotifierMultipleRetries(t *testing.T) {
	// Таймаут 1 сек, максимум 2 попытки
	notifier := NewNotifier("test-token", "test-chat-id", 2, 500*time.Millisecond)
	// Переопределить клиент с коротким таймаутом
	notifier.SetHTTPClient(&http.Client{Timeout: 1 * time.Second})

	event := &events.Event{
		ID:        "retry-test",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}

	ctx := context.Background()
	err := notifier.Notify(ctx, event)
	// Ошибка ожидается — токен невалидный
	if err == nil {
		t.Error("ожидалась ошибка при невалидном токене")
	}
}

// TestNotifierContextCancellation проверяет отмену контекста.
func TestNotifierContextCancellation(t *testing.T) {
	notifier := NewNotifier("test-token", "test-chat-id", 0, 0)

	event := &events.Event{
		ID:        "cancel-test",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := notifier.Notify(ctx, event)
	if err == nil {
		t.Error("ожидалась ошибка при отменённом контексте")
	}
}

// TestNotifierFormatMessageDifferentTypes проверяет форматирование разных типов событий.
func TestNotifierFormatMessageDifferentTypes(t *testing.T) {
	notifier := NewNotifier("test-token", "test-chat-id", 0, 0)

	tests := []struct {
		name    string
		event   *events.Event
		wantStr string
	}{
		{
			name: "motion событие",
			event: &events.Event{
				Type:      events.EventTypeMotion,
				CameraID:  "camera-1",
				StartedAt: time.Date(2026, 9, 20, 18, 42, 17, 0, time.UTC),
			},
			wantStr: "движение",
		},
		{
			name: "camera_online событие",
			event: &events.Event{
				Type:      events.EventTypeCameraOnline,
				CameraID:  "camera-1",
				StartedAt: time.Date(2026, 9, 20, 18, 42, 17, 0, time.UTC),
			},
			wantStr: "camera-1",
		},
		{
			name: "camera_offline событие",
			event: &events.Event{
				Type:      events.EventTypeCameraOffline,
				CameraID:  "camera-1",
				StartedAt: time.Date(2026, 9, 20, 18, 42, 17, 0, time.UTC),
			},
			wantStr: "camera-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := notifier.formatMessage(tt.event)
			if !strings.Contains(message, tt.wantStr) {
				t.Errorf("ожидалось '%s' в сообщении, получено: %s", tt.wantStr, message)
			}
		})
	}
}
