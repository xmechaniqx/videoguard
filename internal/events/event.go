// Package events предоставляет модель событий и менеджер событий.
//
// Событие — центральная сущность приложения. Оно создаётся при обнаружении
// движения, изменении состояния камеры или других значимых событиях.
//
// Данные хранятся в SQLite с WAL режимом для производительности.
package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"videoguard/internal/db"
)

// EventType определяет тип события.
type EventType string

const (
	EventTypeMotion             EventType = "motion"              // Обнаружено движение
	EventTypeCameraOnline       EventType = "camera_online"       // Камера доступна
	EventTypeCameraOffline      EventType = "camera_offline"      // Камера недоступна
	EventTypeCameraReconnecting EventType = "camera_reconnecting" // Камера переподключается
	EventTypeRecordingStarted   EventType = "recording_started"   // Начата запись
	EventTypeRecordingStopped   EventType = "recording_stopped"   // Запись остановлена
	EventTypeStorageWarning     EventType = "storage_warning"     // Предупреждение о хранилище
	EventTypeStorageFull        EventType = "storage_full"        // Хранилище заполнено
	EventTypeSystemError        EventType = "system_error"        // Ошибка системы
)

// EventSource определяет источник события.
type EventSource string

const (
	EventSourceMotion EventSource = "motion" // Детектор движения
	EventSourceHTTP   EventSource = "http"   // HTTP API
	EventSourceSystem EventSource = "system" // Внутренняя система
	EventSourceCamera EventSource = "camera" // Камера
	EventSourceDev    EventSource = "dev"    // Режим разработки
)

// Event представляет событие в системе.
type Event struct {
	ID           string                 `json:"id"`
	CameraID     string                 `json:"camera_id"`
	Type         EventType              `json:"type"`
	Source       EventSource            `json:"source"`
	StartedAt    time.Time              `json:"started_at"`
	EndedAt      time.Time              `json:"ended_at,omitempty"`
	SnapshotPath string                 `json:"snapshot_path,omitempty"`
	VideoPath    string                 `json:"video_path,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// EventManager управляет созданием, хранением и поиском событий.
// Использует SQLite для постоянного хранения.
type EventManager struct {
	db      *db.DB
	mu      sync.RWMutex
	cache   map[string]*Event
	ordered []string // IDs в порядке добавления
	maxAge  time.Duration
}

// NewEventManager создаёт новый менеджер событий с SQLite.
func NewEventManager(d *db.DB, maxAge time.Duration) *EventManager {
	return &EventManager{
		db:      d,
		cache:   make(map[string]*Event),
		ordered: make([]string, 0),
		maxAge:  maxAge,
	}
}

// Create создаёт новое событие и добавляет его в хранилище.
func (em *EventManager) Create(event *Event) error {
	if event.ID == "" {
		event.ID = generateEventID()
	}
	if event.StartedAt.IsZero() {
		event.StartedAt = time.Now()
	}
	if event.Metadata == nil {
		event.Metadata = make(map[string]interface{})
	}

	if err := ValidateEvent(event); err != nil {
		return err
	}

	// Сохранить в SQLite
	metaJSON, _ := json.Marshal(event.Metadata)

	var endedAt interface{}
	if event.EndedAt.IsZero() {
		endedAt = nil
	} else {
		endedAt = event.EndedAt.Format(time.RFC3339Nano)
	}

	tx, err := em.db.DB().Begin()
	if err != nil {
		return fmt.Errorf("начать транзакцию: %w", err)
	}

	_, err = tx.Exec(
		`INSERT OR REPLACE INTO events (id, camera_id, type, source, started_at, ended_at, snapshot_path, video_path, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.CameraID,
		string(event.Type),
		string(event.Source),
		event.StartedAt.Format(time.RFC3339Nano),
		endedAt,
		event.SnapshotPath,
		event.VideoPath,
		string(metaJSON),
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("сохранить событие в БД: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("закоммитить транзакцию: %w", err)
	}

	// Обновить кэш
	em.cache[event.ID] = event
	em.ordered = append(em.ordered, event.ID)

	return nil
}

// Cleanup удаляет старые события из БД и кэша.
func (em *EventManager) Cleanup() {
	if em.maxAge <= 0 {
		return
	}

	em.mu.Lock()
	defer em.mu.Unlock()

	cutoff := time.Now().Add(-em.maxAge).Format(time.RFC3339Nano)
	_, err := em.db.DB().Exec(`DELETE FROM events WHERE started_at < ?`, cutoff)
	if err != nil {
		return
	}

	em.cleanupLocked()
}

// GetByID возвращает событие по ID.
func (em *EventManager) GetByID(id string) (*Event, bool) {
	em.mu.RLock()
	event, ok := em.cache[id]
	em.mu.RUnlock()

	if ok {
		copy := *event
		return &copy, true
	}

	// Загрузить из БД
	var e Event
	var metaJSON, startedAt string
	var endedAt *string
	err := em.db.DB().QueryRow(
		`SELECT id, camera_id, type, source, started_at, ended_at, snapshot_path, video_path, metadata
		 FROM events WHERE id = ?`, id,
	).Scan(
		&e.ID, &e.CameraID, (*string)(&e.Type), (*string)(&e.Source),
		&startedAt, &endedAt, &e.SnapshotPath, &e.VideoPath, &metaJSON,
	)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		return nil, false
	}

	// Парсим время из строки
	if startedAt != "" {
		e.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
	}
	if endedAt != nil {
		e.EndedAt, _ = time.Parse(time.RFC3339Nano, *endedAt)
	}

	if metaJSON != "" {
		json.Unmarshal([]byte(metaJSON), &e.Metadata)
	}

	copy := e
	return &copy, true
}

// List возвращает список событий с фильтрацией.
func (em *EventManager) List(cameraID string, eventType EventType, from, to time.Time, limit int) []*Event {
	query := `SELECT id, camera_id, type, source, started_at, ended_at, snapshot_path, video_path, metadata
			  FROM events WHERE 1=1`
	args := []interface{}{}

	if cameraID != "" {
		query += " AND camera_id = ?"
		args = append(args, cameraID)
	}
	if eventType != "" {
		query += " AND type = ?"
		args = append(args, string(eventType))
	}
	if !from.IsZero() {
		query += " AND started_at >= ?"
		args = append(args, from.Format(time.RFC3339Nano))
	}
	if !to.IsZero() {
		query += " AND started_at <= ?"
		args = append(args, to.Format(time.RFC3339Nano))
	}

	query += " ORDER BY started_at DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := em.db.DB().Query(query, args...)
	if err != nil {
		return []*Event{}
	}
	defer rows.Close()

	result := make([]*Event, 0)
	for rows.Next() {
		var e Event
		var metaJSON string
		var startedAt string
		var endedAt *string

		err := rows.Scan(
			&e.ID, &e.CameraID, (*string)(&e.Type), (*string)(&e.Source),
			&startedAt, &endedAt, &e.SnapshotPath, &e.VideoPath, &metaJSON,
		)
		if err != nil {
			fmt.Printf("[EVENTS] ERROR scan error: %v\n", err)
			continue
		}

		// Парсим время из строки
		if startedAt != "" {
			e.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
		}
		if endedAt != nil {
			e.EndedAt, _ = time.Parse(time.RFC3339Nano, *endedAt)
		}

		if metaJSON != "" {
			json.Unmarshal([]byte(metaJSON), &e.Metadata)
		}

		copy := e
		result = append(result, &copy)
	}

	return result
}

// CountByType возвращает количество событий по типу.
func (em *EventManager) CountByType(eventType EventType) int {
	var count int
	err := em.db.DB().QueryRow(
		`SELECT COUNT(*) FROM events WHERE type = ?`, string(eventType),
	).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// CountByCamera возвращает количество событий по камере.
func (em *EventManager) CountByCamera(cameraID string) int {
	var count int
	err := em.db.DB().QueryRow(
		`SELECT COUNT(*) FROM events WHERE camera_id = ?`, cameraID,
	).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// TotalCount возвращает общее количество событий.
func (em *EventManager) TotalCount() int {
	var count int
	err := em.db.DB().QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// cleanup удаляет события, превышающие максимальный возраст.
func (em *EventManager) cleanup() {
	if em.maxAge <= 0 {
		return
	}

	em.mu.Lock()
	defer em.mu.Unlock()

	cutoff := time.Now().Add(-em.maxAge).Format(time.RFC3339Nano)
	_, err := em.db.DB().Exec(`DELETE FROM events WHERE started_at < ?`, cutoff)
	if err != nil {
		return
	}

	em.cleanupLocked()
}

func (em *EventManager) cleanupLocked() {
	if em.maxAge <= 0 {
		return
	}

	cutoff := time.Now().Add(-em.maxAge)
	valid := make([]string, 0)

	for _, id := range em.ordered {
		if event, ok := em.cache[id]; ok && event.StartedAt.After(cutoff) {
			valid = append(valid, id)
		} else {
			delete(em.cache, id)
		}
	}

	em.ordered = valid
}

// generateEventID создаёт UUID v4 для события.
func generateEventID() string {
	return uuid.New().String()
}

// ValidateEvent проверяет валидность события.
func ValidateEvent(event *Event) error {
	if event.Type == "" {
		return fmt.Errorf("тип события обязателен")
	}

	validTypes := map[EventType]bool{
		EventTypeMotion:             true,
		EventTypeCameraOnline:       true,
		EventTypeCameraOffline:      true,
		EventTypeCameraReconnecting: true,
		EventTypeRecordingStarted:   true,
		EventTypeRecordingStopped:   true,
		EventTypeStorageWarning:     true,
		EventTypeStorageFull:        true,
		EventTypeSystemError:        true,
	}

	if !validTypes[event.Type] {
		return fmt.Errorf("недопустимый тип события: %s", event.Type)
	}

	if event.Source == "" {
		event.Source = EventSourceSystem
	}

	validSources := map[EventSource]bool{
		EventSourceMotion: true,
		EventSourceHTTP:   true,
		EventSourceSystem: true,
		EventSourceCamera: true,
		EventSourceDev:    true,
	}

	if !validSources[event.Source] {
		return fmt.Errorf("недопустимый источник события: %s", event.Source)
	}

	if event.CameraID == "" {
		return fmt.Errorf("camera_id обязателен")
	}

	return nil
}
