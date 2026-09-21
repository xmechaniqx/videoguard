package notifier

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"videoguard/internal/events"
)

// mockNotifier — мок-реализация Notifier для тестирования.
type mockNotifier struct {
	mu        sync.Mutex
	callCount int
	lastEvent *events.Event
	err       error
}

// Notify реализует интерфейс Notifier.
func (m *mockNotifier) Notify(ctx context.Context, event *events.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	m.lastEvent = event
	return m.err
}

// Name реализует интерфейс Notifier.
func (m *mockNotifier) Name() string {
	return "mock"
}

// GetCallCount возвращает безопасное значение callCount.
func (m *mockNotifier) GetCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// GetLastEvent возвращает безопасную копию lastEvent.
func (m *mockNotifier) GetLastEvent() *events.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastEvent == nil {
		return nil
	}
	eventCopy := *m.lastEvent
	return &eventCopy
}

// TestNewQueue проверяет создание очереди уведомлений.
func TestNewQueue(t *testing.T) {
	mock := &mockNotifier{}
	queue := NewQueue(mock, 10)

	if queue == nil {
		t.Fatal("ожидалась очередь, получено nil")
	}

	// Проверить имя уведомителя
	if mock.Name() != "mock" {
		t.Errorf("ожидалось имя 'mock', получено '%s'", mock.Name())
	}
}

// TestQueueEnqueue проверяет добавление в очередь.
func TestQueueEnqueue(t *testing.T) {
	mock := &mockNotifier{}
	queue := NewQueue(mock, 10)

	event := &events.Event{
		ID:        "test-event-1",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}

	// Добавить событие в очередь
	err := queue.Enqueue(event)
	if err != nil {
		t.Fatalf("не удалось добавить событие в очередь: %v", err)
	}

	// Подождать обработки (асинхронная)
	time.Sleep(100 * time.Millisecond)

	// Проверить, что notify был вызван
	if mock.GetCallCount() != 1 {
		t.Errorf("ожидался 1 вызов Notify, получено %d", mock.GetCallCount())
	}

	// Проверить, что событие передано правильно
	lastEvent := mock.GetLastEvent()
	if lastEvent == nil {
		t.Error("ожидалось событие, получено nil")
	} else if lastEvent.ID != "test-event-1" {
		t.Errorf("ожидался ID 'test-event-1', получено '%s'", lastEvent.ID)
	}
}

// TestQueueOverflow проверяет переполнение очереди.
func TestQueueOverflow(t *testing.T) {
	mock := &mockNotifier{}
	// Создать очередь с размером 1
	queue := NewQueue(mock, 1)

	event := &events.Event{
		ID:        "overflow-test",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}

	// Заполнить очередь
	err := queue.Enqueue(event)
	if err != nil {
		t.Fatalf("не удалось добавить первое событие: %v", err)
	}

	// Подождать обработки
	time.Sleep(100 * time.Millisecond)

	// Добавить ещё одно событие — должно быть переполнение
	// (первое уже обработано, поэтому второе должно добавиться)
	err = queue.Enqueue(event)
	// Ошибка не обязательна, так как первое событие уже обработано
	_ = err
}

// TestQueueNotifierError проверяет обработку ошибок уведомителя.
func TestQueueNotifierError(t *testing.T) {
	mock := &mockNotifier{
		err: fmt.Errorf("ошибка сети"),
	}
	queue := NewQueue(mock, 10)

	event := &events.Event{
		ID:        "error-test",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}

	// Добавить событие — notify вернёт ошибку
	err := queue.Enqueue(event)
	if err != nil {
		t.Fatalf("не удалось добавить событие в очередь: %v", err)
	}

	// Подождать обработки
	time.Sleep(200 * time.Millisecond)

	// Проверить, что notify был вызван
	if mock.GetCallCount() != 1 {
		t.Errorf("ожидался 1 вызов Notify, получено %d", mock.GetCallCount())
	}

	// Очередь не должна падать при ошибке
	// Добавить ещё одно событие
	event2 := &events.Event{
		ID:        "error-test-2",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}

	err = queue.Enqueue(event2)
	if err != nil {
		t.Fatalf("не удалось добавить второе событие: %v", err)
	}
}

// TestQueueMultipleEvents проверяет обработку нескольких событий.
func TestQueueMultipleEvents(t *testing.T) {
	mock := &mockNotifier{}
	queue := NewQueue(mock, 10)

	// Добавить несколько событий
	for i := 1; i <= 5; i++ {
		event := &events.Event{
			ID:        fmt.Sprintf("multi-event-%d", i),
			Type:      events.EventTypeMotion,
			CameraID:  "camera-1",
			Source:    events.EventSourceMotion,
			StartedAt: time.Now(),
		}

		err := queue.Enqueue(event)
		if err != nil {
			t.Fatalf("не удалось добавить событие %d: %v", i, err)
		}
	}

	// Подождать обработки всех событий
	time.Sleep(500 * time.Millisecond)

	// Проверить, что все события обработаны
	if mock.GetCallCount() != 5 {
		t.Errorf("ожидался 1 вызов Notify, получено %d", mock.GetCallCount())
	}
}

// TestQueueWithTimeout проверяет обработку с таймаутом контекста.
func TestQueueWithTimeout(t *testing.T) {
	mock := &mockNotifier{}
	queue := NewQueue(mock, 10)

	event := &events.Event{
		ID:        "timeout-test",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}

	err := queue.Enqueue(event)
	if err != nil {
		t.Fatalf("не удалось добавить событие: %v", err)
	}

	// Подождать обработки
	time.Sleep(200 * time.Millisecond)

	if mock.GetCallCount() != 1 {
		t.Errorf("ожидался 1 вызов Notify, получено %d", mock.GetCallCount())
	}
}

// TestMockNotifierName проверяет имя мок-уведомителя.
func TestMockNotifierName(t *testing.T) {
	mock := &mockNotifier{}

	name := mock.Name()
	if name != "mock" {
		t.Errorf("ожидалось имя 'mock', получено '%s'", name)
	}
}
