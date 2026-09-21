package notifier

import (
	"context"
	"sync"
	"time"

	"videoguard/internal/events"
)

// Deduplicator предотвращает спам уведомлениями для одной камеры.
//
// Принцип работы:
// - Для события типа motion применяется cooldown per camera
// - Если в течение cooldown приходит новое motion — счётчик инкрементируется, отправка пропускается
// - После истечения cooldown отправляется одно агрегированное уведомление с количеством событий
// - События других типов (camera_online, camera_offline и т.д.) проходят без задержки
type Deduplicator struct {
	notifier Notifier
	cooldown time.Duration

	mu        sync.RWMutex
	cooldowns map[string]time.Time     // cameraID -> время последнего отправленного уведомления
	pending   map[string]*pendingEvent // cameraID -> ожидающее агрегированное событие
}

// pendingEvent хранит агрегированное motion-событие.
type pendingEvent struct {
	cameraID string
	count    int       // количество событий в окне
	firstAt  time.Time // время первого события
	lastAt   time.Time // время последнего события
}

// NewDeduplicator создаёт deduplicator поверх переднего notifier.
func NewDeduplicator(notifier Notifier, cooldown time.Duration) *Deduplicator {
	d := &Deduplicator{
		notifier:  notifier,
		cooldown:  cooldown,
		cooldowns: make(map[string]time.Time),
		pending:   make(map[string]*pendingEvent),
	}

	// Запустить фоновый воркер для отправки pending событий по истечении cooldown
	go d.flushWorker()

	return d
}

// Notify реализует интерфейс Notifier, применяя deduplication.
func (d *Deduplicator) Notify(ctx context.Context, event *events.Event) error {
	// Для motion событий — применяем deduplication
	if event.Type == events.EventTypeMotion {
		d.enqueueMotion(event)
		return nil
	}

	// Для всех остальных событий — отправляем немедленно
	return d.notifier.Notify(ctx, event)
}

// Name возвращает имя базового notifier.
func (d *Deduplicator) Name() string {
	return d.notifier.Name() + "/dedup"
}

// enqueueMotion добавляет motion-событие в очередь агрегации.
func (d *Deduplicator) enqueueMotion(event *events.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()

	camID := event.CameraID
	now := time.Now()

	// Проверяем, не в cooldown ли мы для этой камеры
	lastSent, inCooldown := d.cooldowns[camID]
	if inCooldown && now.Sub(lastSent) < d.cooldown {
		// В cooldown — инкрементируем счётчик
		p, exists := d.pending[camID]
		if !exists {
			p = &pendingEvent{
				cameraID: camID,
				firstAt:  now,
				lastAt:   now,
			}
			d.pending[camID] = p
		}
		p.count++
		p.lastAt = now
		return
	}

	// Не в cooldown — отправляем немедленно
	_ = d.notifier.Notify(ctxForDedup, event)
	d.cooldowns[camID] = now

	// Удаляем pending если был
	delete(d.pending, camID)
}

// flushWorker периодически проверяет pending события и отправляет те, у которых истёк cooldown.
func (d *Deduplicator) flushWorker() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.flushPending()
		}
	}
}

// Flush немедленно отправляет все pending события.
// Вызывается из тестов или при graceful shutdown.
func (d *Deduplicator) Flush() {
	d.flushPending()
}

// flushPending отправляет все pending события, для которых истёк cooldown.
func (d *Deduplicator) flushPending() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	for camID, p := range d.pending {
		// Проверяем, истёк ли cooldown с момента последнего события
		if now.Sub(p.lastAt) >= d.cooldown {
			d.sendAggregated(p)
			delete(d.pending, camID)
		}
	}
}

// sendAggregated отправляет одно агрегированное уведомление.
func (d *Deduplicator) sendAggregated(p *pendingEvent) {
	// Создаём агрегированное событие
	agg := &events.Event{
		CameraID:  p.cameraID,
		Type:      events.EventTypeMotion,
		Source:    events.EventSourceSystem,
		StartedAt: p.firstAt,
		Metadata: map[string]interface{}{
			"aggregated":   true,
			"motion_count": p.count,
			"first_at":     p.firstAt.Format(time.RFC3339),
			"last_at":      p.lastAt.Format(time.RFC3339),
		},
	}

	_ = d.notifier.Notify(ctxForDedup, agg)
	d.cooldowns[p.cameraID] = time.Now()
}

// ctxForDedup — минимальный контекст для вызовов notifier внутри deduplicator.
var ctxForDedup = func() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cancel()
	return ctx
}()
