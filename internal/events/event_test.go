package events

import (
	"os"
	"testing"
	"time"

	"videoguard/internal/db"
)

// TestValidateEvent проверяет валидацию событий.
func TestValidateEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		wantErr bool
	}{
		{
			name: "валидное motion событие",
			event: &Event{
				Type:     EventTypeMotion,
				CameraID: "camera-1",
				Source:   EventSourceMotion,
			},
			wantErr: false,
		},
		{
			name: "валидное HTTP событие",
			event: &Event{
				Type:     EventTypeMotion,
				CameraID: "camera-1",
				Source:   EventSourceHTTP,
			},
			wantErr: false,
		},
		{
			name: "пустой тип",
			event: &Event{
				CameraID: "camera-1",
			},
			wantErr: true,
		},
		{
			name: "недопустимый тип",
			event: &Event{
				Type:     "invalid_type",
				CameraID: "camera-1",
			},
			wantErr: true,
		},
		{
			name: "пустой camera_id",
			event: &Event{
				Type: EventTypeMotion,
			},
			wantErr: true,
		},
		{
			name: "недопустимый источник",
			event: &Event{
				Type:     EventTypeMotion,
				CameraID: "camera-1",
				Source:   "invalid_source",
			},
			wantErr: true,
		},
		{
			name: "событие с системой по умолчанию",
			event: &Event{
				Type:     EventTypeCameraOnline,
				CameraID: "camera-1",
			},
			wantErr: false,
		},
		{
			name: "camera_reconnecting событие",
			event: &Event{
				Type:     EventTypeCameraReconnecting,
				CameraID: "camera-1",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEvent(tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEvent() ошибка = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// openTestDB создаёт временную SQLite БД для тестов.
func openTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	tmpFile := t.TempDir() + "/test.db"
	d, err := db.Open(tmpFile)
	if err != nil {
		t.Fatalf("не удалось открыть тестовую БД: %v", err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatalf("не удалось применить миграции: %v", err)
	}
	return d, tmpFile
}

// closeTestDB закрывает тестовую БД.
func closeTestDB(t *testing.T, d *db.DB) {
	t.Helper()
	if err := d.Close(); err != nil {
		t.Logf("предупреждение при закрытии БД: %v", err)
	}
}

// TestEventManagerCreate проверяет создание событий.
func TestEventManagerCreate(t *testing.T) {
	d, path := openTestDB(t)
	t.Cleanup(func() { closeTestDB(t, d); os.Remove(path) })

	em := NewEventManager(d, 0) // без ограничения по времени

	event := &Event{
		Type:      EventTypeMotion,
		CameraID:  "camera-1",
		Source:    EventSourceMotion,
		StartedAt: time.Now(),
	}

	if err := em.Create(event); err != nil {
		t.Fatalf("не удалось создать событие: %v", err)
	}

	if event.ID == "" {
		t.Error("ожидался ID события, получена пустая строка")
	}

	if em.TotalCount() != 1 {
		t.Errorf("ожидалось 1 событие, получено %d", em.TotalCount())
	}
}

// TestEventManagerGetByID проверяет получение события по ID.
func TestEventManagerGetByID(t *testing.T) {
	d, path := openTestDB(t)
	t.Cleanup(func() { closeTestDB(t, d); os.Remove(path) })

	em := NewEventManager(d, 0)

	original := &Event{
		ID:        "test-event-1",
		Type:      EventTypeMotion,
		CameraID:  "camera-1",
		Source:    EventSourceMotion,
		StartedAt: time.Now(),
	}

	if err := em.Create(original); err != nil {
		t.Fatalf("не удалось создать событие: %v", err)
	}

	got, ok := em.GetByID("test-event-1")
	if !ok {
		t.Fatal("ожидалось событие, получено nil")
	}

	if got.ID != original.ID {
		t.Errorf("ожидался ID '%s', получено '%s'", original.ID, got.ID)
	}

	if got.Type != original.Type {
		t.Errorf("ожидался тип '%s', получено '%s'", original.Type, got.Type)
	}

	// Проверить, что возвращена копия
	got.Type = EventTypeCameraOffline
	if original.Type == EventTypeCameraOffline {
		t.Error("ожидалась копия события, а не оригинал")
	}
}

// TestEventManagerList проверяет список событий с фильтрацией.
func TestEventManagerList(t *testing.T) {
	d, path := openTestDB(t)
	t.Cleanup(func() { closeTestDB(t, d); os.Remove(path) })

	em := NewEventManager(d, 0)

	now := time.Now()

	events := []*Event{
		{
			ID:        "evt-1",
			Type:      EventTypeMotion,
			CameraID:  "camera-1",
			Source:    EventSourceMotion,
			StartedAt: now.Add(-2 * time.Hour),
		},
		{
			ID:        "evt-2",
			Type:      EventTypeMotion,
			CameraID:  "camera-2",
			Source:    EventSourceHTTP,
			StartedAt: now.Add(-1 * time.Hour),
		},
		{
			ID:        "evt-3",
			Type:      EventTypeCameraOnline,
			CameraID:  "camera-1",
			Source:    EventSourceSystem,
			StartedAt: now.Add(-30 * time.Minute),
		},
	}

	for _, evt := range events {
		if err := em.Create(evt); err != nil {
			t.Fatalf("не удалось создать событие: %v", err)
		}
	}

	tests := []struct {
		name      string
		cameraID  string
		eventType EventType
		limit     int
		wantCount int
	}{
		{
			name:      "все события",
			limit:     50,
			wantCount: 3,
		},
		{
			name:      "фильтр по камере",
			cameraID:  "camera-1",
			limit:     50,
			wantCount: 2,
		},
		{
			name:      "фильтр по типу",
			eventType: EventTypeMotion,
			limit:     50,
			wantCount: 2,
		},
		{
			name:      "фильтр по камере и типу",
			cameraID:  "camera-1",
			eventType: EventTypeMotion,
			limit:     50,
			wantCount: 1,
		},
		{
			name:      "лимит 1",
			limit:     1,
			wantCount: 1,
		},
		{
			name:      "фильтр по несуществующей камере",
			cameraID:  "camera-999",
			limit:     50,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := em.List(tt.cameraID, tt.eventType, time.Time{}, time.Time{}, tt.limit)
			if len(result) != tt.wantCount {
				t.Errorf("ожидалось %d событий, получено %d", tt.wantCount, len(result))
			}
		})
	}
}

// TestEventManagerCountByType проверяет подсчёт событий по типу.
func TestEventManagerCountByType(t *testing.T) {
	d, path := openTestDB(t)
	t.Cleanup(func() { closeTestDB(t, d); os.Remove(path) })

	em := NewEventManager(d, 0)

	em.Create(&Event{Type: EventTypeMotion, CameraID: "cam1"})
	em.Create(&Event{Type: EventTypeMotion, CameraID: "cam2"})
	em.Create(&Event{Type: EventTypeCameraOnline, CameraID: "cam1"})
	em.Create(&Event{Type: EventTypeCameraOffline, CameraID: "cam1"})

	if count := em.CountByType(EventTypeMotion); count != 2 {
		t.Errorf("ожидалось 2 motion события, получено %d", count)
	}

	if count := em.CountByType(EventTypeCameraOnline); count != 1 {
		t.Errorf("ожидалось 1 camera_online событие, получено %d", count)
	}

	if count := em.CountByType(EventTypeStorageFull); count != 0 {
		t.Errorf("ожидалось 0 storage_full событий, получено %d", count)
	}
}

// TestEventManagerCountByCamera проверяет подсчёт событий по камере.
func TestEventManagerCountByCamera(t *testing.T) {
	d, path := openTestDB(t)
	t.Cleanup(func() { closeTestDB(t, d); os.Remove(path) })

	em := NewEventManager(d, 0)

	em.Create(&Event{Type: EventTypeMotion, CameraID: "cam1"})
	em.Create(&Event{Type: EventTypeMotion, CameraID: "cam1"})
	em.Create(&Event{Type: EventTypeMotion, CameraID: "cam2"})

	if count := em.CountByCamera("cam1"); count != 2 {
		t.Errorf("ожидалось 2 события для cam1, получено %d", count)
	}

	if count := em.CountByCamera("cam2"); count != 1 {
		t.Errorf("ожидалось 1 событие для cam2, получено %d", count)
	}

	if count := em.CountByCamera("cam999"); count != 0 {
		t.Errorf("ожидалось 0 событий для cam999, получено %d", count)
	}
}

// TestEventManagerCleanup проверяет очистку старых событий.
func TestEventManagerCleanup(t *testing.T) {
	d, path := openTestDB(t)
	t.Cleanup(func() { closeTestDB(t, d); os.Remove(path) })

	em := NewEventManager(d, 1*time.Hour) // 1 час хранения

	// Создать событие "старое" (в прошлом)
	oldEvent := &Event{
		ID:        "old-event",
		Type:      EventTypeMotion,
		CameraID:  "cam1",
		StartedAt: time.Now().Add(-2 * time.Hour), // 2 часа назад
	}

	em.Create(oldEvent)

	// Создать событие "новое"
	newEvent := &Event{
		ID:        "new-event",
		Type:      EventTypeMotion,
		CameraID:  "cam1",
		StartedAt: time.Now(),
	}

	em.Create(newEvent)

	// Явно вызвать cleanup
	em.Cleanup()

	// Проверить, что старое событие удалено
	_, ok := em.GetByID("old-event")
	if ok {
		t.Error("ожидалось удаление старого события")
	}

	// Проверить, что новое событие осталось
	_, ok = em.GetByID("new-event")
	if !ok {
		t.Error("ожидалось сохранение нового события")
	}

	if em.TotalCount() != 1 {
		t.Errorf("ожидалось 1 событие, получено %d", em.TotalCount())
	}
}

// TestEventTypeString проверяет строковые представления типов событий.
func TestEventTypeString(t *testing.T) {
	tests := []struct {
		eventType EventType
		want      string
	}{
		{EventTypeMotion, "motion"},
		{EventTypeCameraOnline, "camera_online"},
		{EventTypeCameraOffline, "camera_offline"},
		{EventTypeCameraReconnecting, "camera_reconnecting"},
		{EventTypeRecordingStarted, "recording_started"},
		{EventTypeRecordingStopped, "recording_stopped"},
		{EventTypeStorageWarning, "storage_warning"},
		{EventTypeStorageFull, "storage_full"},
		{EventTypeSystemError, "system_error"},
	}

	for _, tt := range tests {
		t.Run(string(tt.eventType), func(t *testing.T) {
			if string(tt.eventType) != tt.want {
				t.Errorf("ожидалось '%s', получено '%s'", tt.want, tt.eventType)
			}
		})
	}
}

// TestEventSourceString проверяет строковые представления источников событий.
func TestEventSourceString(t *testing.T) {
	tests := []struct {
		source EventSource
		want   string
	}{
		{EventSourceMotion, "motion"},
		{EventSourceHTTP, "http"},
		{EventSourceSystem, "system"},
		{EventSourceCamera, "camera"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.source) != tt.want {
				t.Errorf("ожидалось '%s', получено '%s'", tt.want, tt.source)
			}
		})
	}
}
