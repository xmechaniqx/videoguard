package notifier

import (
	"context"
	"sync"
	"testing"
	"time"

	"videoguard/internal/events"
)

// dedupMockNotifier — заглушка для Notifier, считает количество вызовов.
type dedupMockNotifier struct {
	mu        sync.Mutex
	calls     int
	lastEvent *events.Event
}

func (m *dedupMockNotifier) Notify(ctx context.Context, event *events.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastEvent = event
	return nil
}

func (m *dedupMockNotifier) Name() string {
	return "mock"
}

func (m *dedupMockNotifier) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *dedupMockNotifier) LastEvent() *events.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastEvent == nil {
		return nil
	}
	e := *m.lastEvent
	return &e
}

func TestDeduplicatorMotionCoalescing(t *testing.T) {
	mock := &dedupMockNotifier{}
	dedup := NewDeduplicator(mock, 5*time.Second)

	ctx := context.Background()

	// Отправить 5 motion-событий с интервалом 1с
	for i := 0; i < 5; i++ {
		event := &events.Event{
			CameraID:  "cam-1",
			Type:      events.EventTypeMotion,
			Source:    events.EventSourceMotion,
			StartedAt: time.Now(),
		}
		dedup.Notify(ctx, event)
		time.Sleep(100 * time.Millisecond)
	}

	// Первое событие отправлено немедленно, остальные должны быть агрегированы
	// Но cooldown ещё не истёк, поэтому pending events не отправлены
	count := mock.CallCount()
	if count != 1 {
		t.Errorf("ожидается 1 отправка (первое событие), получено: %d", count)
	}

	// Подождать пока cooldown истечёт, затем принудительно flush
	time.Sleep(6 * time.Second)
	dedup.Flush()

	count = mock.CallCount()
	if count != 2 {
		t.Errorf("ожидается 2 отправки (первое + агрегированное), получено: %d", count)
	}

	// Проверить агрегированное событие
	last := mock.LastEvent()
	if last == nil {
		t.Fatal("last event is nil")
	}
	if meta, ok := last.Metadata["aggregated"].(bool); !ok || !meta {
		t.Error("агрегированное событие должно иметь metadata.aggregated=true")
	}
	// motion_count = 4 (события 2-5, первое отправлено сразу)
	if count, ok := last.Metadata["motion_count"].(int); !ok || count != 4 {
		t.Errorf("ожидается motion_count=4 (агрегированные), получено: %v", last.Metadata["motion_count"])
	}
}

func TestDeduplicatorNonMotionPassthrough(t *testing.T) {
	mock := &dedupMockNotifier{}
	dedup := NewDeduplicator(mock, 5*time.Second)

	ctx := context.Background()

	// camera_online должен пройти без задержки
	event := &events.Event{
		CameraID:  "cam-1",
		Type:      events.EventTypeCameraOnline,
		Source:    events.EventSourceCamera,
		StartedAt: time.Now(),
	}
	err := dedup.Notify(ctx, event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	count := mock.CallCount()
	if count != 1 {
		t.Errorf("ожидается 1 отправка, получено: %d", count)
	}
}

func TestDeduplicatorMultipleCameras(t *testing.T) {
	mock := &dedupMockNotifier{}
	dedup := NewDeduplicator(mock, 2*time.Second)

	ctx := context.Background()

	// Motion для cam-1
	event1 := &events.Event{
		CameraID:  "cam-1",
		Type:      events.EventTypeMotion,
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}
	dedup.Notify(ctx, event1)

	// Motion для cam-2 (должен пройти отдельно)
	event2 := &events.Event{
		CameraID:  "cam-2",
		Type:      events.EventTypeMotion,
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	}
	dedup.Notify(ctx, event2)

	count := mock.CallCount()
	if count != 2 {
		t.Errorf("ожидается 2 отправки (разные камеры), получено: %d", count)
	}
}

func TestDeduplicatorConcurrency(t *testing.T) {
	mock := &dedupMockNotifier{}
	dedup := NewDeduplicator(mock, 1*time.Second)

	ctx := context.Background()
	var wg sync.WaitGroup

	// 20 горутин отправляют motion-события
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event := &events.Event{
				CameraID:  "cam-1",
				Type:      events.EventTypeMotion,
				Source:    events.EventSourceMotion,
				StartedAt: time.Now(),
			}
			_ = dedup.Notify(ctx, event)
		}()
	}

	wg.Wait()

	// Ждём истечения cooldown и flush
	time.Sleep(2 * time.Second)
	dedup.Flush()

	count := mock.CallCount()
	// Должно быть не более 2: первое + агрегированное
	if count > 2 {
		t.Errorf("ожидается <=2 отправкок при конкурентности, получено: %d", count)
	}
}

func TestDeduplicatorCooldownReset(t *testing.T) {
	mock := &dedupMockNotifier{}
	dedup := NewDeduplicator(mock, 1*time.Second)

	ctx := context.Background()

	// Первое событие
	dedup.Notify(ctx, &events.Event{
		CameraID:  "cam-1",
		Type:      events.EventTypeMotion,
		StartedAt: time.Now(),
	})

	time.Sleep(100 * time.Millisecond)

	// Второе событие (в cooldown)
	dedup.Notify(ctx, &events.Event{
		CameraID:  "cam-1",
		Type:      events.EventTypeMotion,
		StartedAt: time.Now(),
	})

	// Ждём истечения cooldown и flush
	time.Sleep(2 * time.Second)
	dedup.Flush()

	count := mock.CallCount()
	if count != 2 {
		t.Errorf("ожидается 2 отправки (первое + flush), получено: %d", count)
	}
}
