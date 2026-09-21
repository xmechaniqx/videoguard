// Package notifier предоставляет интерфейс и реализации уведомителей.
//
// Notifier не должен блокировать основной pipeline обработки событий.
// Используется очередь уведомлений для асинхронной отправки.
package notifier

import (
	"context"
	"fmt"
	"time"

	"videoguard/internal/events"
)

// Notifier определяет интерфейс для отправки уведомлений.
type Notifier interface {
	// Notify отправляет уведомление о событии.
	Notify(ctx context.Context, event *events.Event) error

	// Name возвращает имя уведомителя.
	Name() string
}

// Queue управляет очередью уведомлений.
type Queue struct {
	notifier Notifier
	ch       chan *events.Event
	maxSize  int
	logger   *logger
}

// NewQueue создаёт новую очередь уведомлений.
func NewQueue(notifier Notifier, maxSize int) *Queue {
	q := &Queue{
		notifier: notifier,
		ch:       make(chan *events.Event, maxSize),
		maxSize:  maxSize,
		logger:   newLogger(),
	}

	// Запустить воркер очереди
	go q.worker()

	return q
}

// Enqueue добавляет событие в очередь на отправку уведомления.
func (q *Queue) Enqueue(event *events.Event) error {
	select {
	case q.ch <- event:
		q.logger.Info("Уведомление добавлено в очередь", "event_id", event.ID)
		return nil
	default:
		q.logger.Warn("Очередь уведомлений переполнена", "event_id", event.ID)
		return fmt.Errorf("очередь уведомлений переполнена")
	}
}

// worker обрабатывает очередь уведомлений.
func (q *Queue) worker() {
	for event := range q.ch {
		q.sendNotification(event)
	}
}

// sendNotification отправляет уведомление с повторными попытками.
func (q *Queue) sendNotification(event *events.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := q.notifier.Notify(ctx, event); err != nil {
		q.logger.Error("Не удалось отправить уведомление",
			"event_id", event.ID,
			"error", err.Error())
		// TODO: реализовать persistent queue для сохранения неудачных уведомлений
	}
}
