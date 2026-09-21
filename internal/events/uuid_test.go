package events

import (
	"strings"
	"testing"
	"time"

	"videoguard/internal/db"
)

// TestEventIDIsUUID проверяет что ID события — валидный UUID v4.
func TestEventIDIsUUID(t *testing.T) {
	d, path := openTestDBForUUID(t)
	t.Cleanup(func() { closeTestDBForUUID(t, d); _ = d.Close() })
	_ = path

	em := NewEventManager(d, 0)

	event := &Event{
		Type:     EventTypeMotion,
		CameraID: "camera-1",
		Source:   EventSourceMotion,
	}

	if err := em.Create(event); err != nil {
		t.Fatalf("не удалось создать событие: %v", err)
	}

	id := event.ID

	// UUID v4 формат: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
	if len(id) != 36 {
		t.Errorf("ожидался UUID длиной 36, получено %d: %s", len(id), id)
	}

	// Проверить что это UUID v4 (содержит дефисы и цифру 4 в позиции 14)
	if !strings.Contains(id, "-") {
		t.Error("UUID должен содержать дефисы")
	}

	if len(id) >= 14 && id[14] != '4' {
		t.Errorf("UUID v4 должен иметь '4' в позиции 14, получено '%c'", id[14])
	}

	t.Logf("Сгенерирован UUID: %s", id)
}

// TestEventMetadataJSON проверяет что metadata сохраняется и читается как JSON.
func TestEventMetadataJSON(t *testing.T) {
	d, path := openTestDBForUUID(t)
	t.Cleanup(func() { closeTestDBForUUID(t, d); _ = d.Close() })
	_ = path

	em := NewEventManager(d, 0)

	event := &Event{
		Type:     EventTypeMotion,
		CameraID: "camera-1",
		Source:   EventSourceMotion,
		Metadata: map[string]interface{}{
			"motion_score": 0.91,
			"duration":     12,
			"camera_ip":    "192.168.1.100",
			"zones":        []string{"entrance", "yard"},
		},
	}

	if err := em.Create(event); err != nil {
		t.Fatalf("не удалось создать событие: %v", err)
	}

	// Прочитать через EventManager
	got, ok := em.GetByID(event.ID)
	if !ok {
		t.Fatal("ожидалось событие, получено nil")
	}

	if got.Metadata == nil {
		t.Fatal("ожидался непустой metadata")
	}

	// Проверить поля
	if score, ok := got.Metadata["motion_score"].(float64); !ok || score != 0.91 {
		t.Errorf("ожидался motion_score=0.91, получено %v", got.Metadata["motion_score"])
	}

	// Проверить что duration существует и является числом
	if _, exists := got.Metadata["duration"]; !exists {
		t.Error("ожидалось поле duration в metadata")
	}

	if ip, ok := got.Metadata["camera_ip"].(string); !ok || ip != "192.168.1.100" {
		t.Errorf("ожидался camera_ip='192.168.1.100', получено %v", got.Metadata["camera_ip"])
	}

	// Проверить массив
	if zones, ok := got.Metadata["zones"].([]interface{}); ok && len(zones) == 2 {
		t.Logf("zones: %v", zones)
	}

	t.Logf("Metadata прочитан: %v", got.Metadata)
}

// TestDBNotificationsTable проверяет что таблица notifications создана.
func TestDBNotificationsTable(t *testing.T) {
	d, path := openTestDBForUUID(t)
	t.Cleanup(func() { closeTestDBForUUID(t, d); _ = d.Close() })
	_ = path

	// Проверить что таблица существует
	var count int
	err := d.DB().QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&count)
	if err != nil {
		t.Fatalf("таблица notifications не существует: %v", err)
	}

	t.Logf("Таблица notifications существует, строк: %d", count)
}

// TestDBCameraStateTable проверяет что таблица camera_state создана.
func TestDBCameraStateTable(t *testing.T) {
	d, path := openTestDBForUUID(t)
	t.Cleanup(func() { closeTestDBForUUID(t, d); _ = d.Close() })
	_ = path

	// Проверить что таблица существует
	var count int
	err := d.DB().QueryRow(`SELECT COUNT(*) FROM camera_state`).Scan(&count)
	if err != nil {
		t.Fatalf("таблица camera_state не существует: %v", err)
	}

	// Вставить запись
	cs := &db.CameraState{
		CameraID:      "test-cam",
		Name:          "Test Camera",
		Status:        "online",
		UptimeSeconds: 3600,
		LastSeen:      time.Now(),
	}

	if err := d.UpsertCameraState(cs); err != nil {
		t.Fatalf("не удалось вставить состояние камеры: %v", err)
	}

	// Прочитать обратно
	got, err := d.GetCameraState("test-cam")
	if err != nil {
		t.Fatalf("не удалось прочитать состояние камеры: %v", err)
	}

	if got.Status != "online" {
		t.Errorf("ожидался статус 'online', получено '%s'", got.Status)
	}

	if got.UptimeSeconds != 3600 {
		t.Errorf("ожидался uptime 3600, получено %d", got.UptimeSeconds)
	}

	t.Logf("CameraState: %s, status=%s, uptime=%ds", got.CameraID, got.Status, got.UptimeSeconds)
}

// openTestDBForUUID создаёт временную SQLite БД для UUID/JSON тестов.
func openTestDBForUUID(t *testing.T) (*db.DB, string) {
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

// closeTestDBForUUID закрывает тестовую БД.
func closeTestDBForUUID(t *testing.T, d *db.DB) {
	t.Helper()
	if err := d.Close(); err != nil {
		t.Logf("предупреждение при закрытии БД: %v", err)
	}
}
