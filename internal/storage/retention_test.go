package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRetentionWorkerStartStop проверяет запуск и остановку worker.
func TestRetentionWorkerStartStop(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	config := RetentionConfig{
		Days:                      7,
		EmergencyThresholdPercent: 10,
		CheckInterval:             1 * time.Hour,
	}

	worker := NewRetentionWorker(st, config)
	if worker == nil {
		t.Fatal("ожидался worker, получено nil")
	}

	// Остановка без запуска должна быть безопасной
	worker.Stop()
}

// TestRetentionWorkerCleanup проверяет очистку старых файлов.
func TestRetentionWorkerCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	config := RetentionConfig{
		Days:                      1,
		EmergencyThresholdPercent: 10,
		CheckInterval:             1 * time.Hour,
	}

	worker := NewRetentionWorker(st, config)

	// Создать старую директорию события (2 дня назад)
	oldDate := time.Now().AddDate(0, 0, -2)
	oldEventDir := filepath.Join(st.Root, "events", "camera-1",
		oldDate.Format("2006"),
		oldDate.Format("01"),
		oldDate.Format("02"),
		"old-event")

	if err := os.MkdirAll(oldEventDir, 0755); err != nil {
		t.Fatalf("не удалось создать старую директорию: %v", err)
	}

	// Создать файл в старой директории
	if err := os.WriteFile(filepath.Join(oldEventDir, "snapshot.jpg"), []byte("test"), 0644); err != nil {
		t.Fatalf("не удалось создать файл: %v", err)
	}

	// Установить старый modTime — это ключевой момент для retention logic
	if err := os.Chtimes(oldEventDir, oldDate.Add(-24*time.Hour), oldDate.Add(-24*time.Hour)); err != nil {
		t.Fatalf("не удалось установить modTime: %v", err)
	}

	// Создать новую директорию события (сегодня)
	newDate := time.Now()
	newEventDir := filepath.Join(st.Root, "events", "camera-1",
		newDate.Format("2006"),
		newDate.Format("01"),
		newDate.Format("02"),
		"new-event")

	if err := os.MkdirAll(newEventDir, 0755); err != nil {
		t.Fatalf("не удалось создать новую директорию: %v", err)
	}

	// Запустить очистку
	ctx := context.Background()
	worker.Start(ctx)
	defer worker.Stop()

	// Подождать первую очистку
	time.Sleep(150 * time.Millisecond)

	// Проверить, что старая директория удалена
	if _, err := os.Stat(oldEventDir); !os.IsNotExist(err) {
		t.Error("ожидалось удаление старой директории")
	}

	// Проверить, что новая директория осталась
	if _, err := os.Stat(newEventDir); err != nil {
		t.Error("новая директория должна остаться")
	}
}

// TestRetentionConfigDefault проверяет конфигурацию по умолчанию.
func TestRetentionConfigDefault(t *testing.T) {
	config := DefaultRetentionConfig()

	if config.Days != 7 {
		t.Errorf("ожидался 7 дней, получено %d", config.Days)
	}

	if config.EmergencyThresholdPercent != 10 {
		t.Errorf("ожидался 10%%, получено %d", config.EmergencyThresholdPercent)
	}

	if config.AggressiveThresholdPercent != 5 {
		t.Errorf("ожидался 5%%, получено %d", config.AggressiveThresholdPercent)
	}

	if config.CheckInterval != 1*time.Hour {
		t.Errorf("ожидался интервал 1 час, получено %v", config.CheckInterval)
	}
}

// TestRetentionWorkerEmergencyCleanup проверяет emergency cleanup.
func TestRetentionWorkerEmergencyCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	// Создать старую директорию записей (5 дней назад)
	oldDate := time.Now().AddDate(0, 0, -5)
	oldRecordingDir := filepath.Join(st.Root, "recordings", "camera-1",
		oldDate.Format("2006"),
		oldDate.Format("01"),
		oldDate.Format("02"))

	if err := os.MkdirAll(oldRecordingDir, 0755); err != nil {
		t.Fatalf("не удалось создать старую директорию: %v", err)
	}

	// Создать тестовый файл
	if err := os.WriteFile(filepath.Join(oldRecordingDir, "test.mp4"), []byte("test"), 0644); err != nil {
		t.Fatalf("не удалось создать файл: %v", err)
	}

	// Установить старый modTime
	if err := os.Chtimes(oldRecordingDir, oldDate.Add(-24*time.Hour), oldDate.Add(-24*time.Hour)); err != nil {
		t.Fatalf("не удалось установить modTime: %v", err)
	}

	config := RetentionConfig{
		Days:                      1,
		EmergencyThresholdPercent: 10,
		CheckInterval:             1 * time.Hour,
	}

	worker := NewRetentionWorker(st, config)
	ctx := context.Background()
	worker.Start(ctx)
	defer worker.Stop()

	// Подождать
	time.Sleep(150 * time.Millisecond)

	// Проверить, что старая директория удалена
	if _, err := os.Stat(oldRecordingDir); !os.IsNotExist(err) {
		t.Error("ожидалось удаление старой директории записей")
	}
}

// TestRetentionWorkerMultipleCleanupCycles проверяет несколько циклов очистки.
func TestRetentionWorkerMultipleCleanupCycles(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	config := RetentionConfig{
		Days:                      1,
		EmergencyThresholdPercent: 10,
		CheckInterval:             50 * time.Millisecond, // Быстрый интервал для теста
	}

	worker := NewRetentionWorker(st, config)
	ctx := context.Background()
	worker.Start(ctx)
	defer worker.Stop()

	// Создаём файл с old modTime
	oldDir := filepath.Join(st.Root, "snapshots")
	if err := os.MkdirAll(oldDir, 0755); err != nil {
		t.Fatalf("не удалось создать директорию: %v", err)
	}

	oldFile := filepath.Join(oldDir, "old-file.jpg")
	if err := os.WriteFile(oldFile, []byte("test"), 0644); err != nil {
		t.Fatalf("не удалось создать файл: %v", err)
	}

	// Установить старый modTime (3 дня назад)
	oldTime := time.Now().AddDate(0, 0, -3)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatalf("не удалось установить modTime: %v", err)
	}

	// Подождать несколько циклов очистки
	time.Sleep(200 * time.Millisecond)

	// Проверить, что файл удалён
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("ожидалось удаление старого файла")
	}
}

// TestRetentionWorkerSafeShutdown проверяет безопасное завершение работы.
func TestRetentionWorkerSafeShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	config := DefaultRetentionConfig()
	worker := NewRetentionWorker(st, config)

	ctx := context.Background()
	worker.Start(ctx)

	// Остановить несколько раз — должно быть безопасно
	worker.Stop()
	worker.Stop()
	worker.Stop()
}

// TestRetentionWorkerPathTraversalProtection проверяет защиту от path traversal.
func TestRetentionWorkerPathTraversalProtection(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	config := DefaultRetentionConfig()
	worker := NewRetentionWorker(st, config)

	// Создать "опасную" директорию (с traversal в имени)
	// Это не должно вызвать проблем
	unsafeDir := filepath.Join(tmpDir, "recordings", "..", "escape-test")
	if err := os.MkdirAll(unsafeDir, 0755); err != nil {
		t.Fatalf("не удалось создать директорию: %v", err)
	}

	ctx := context.Background()
	worker.Start(ctx)
	defer worker.Stop()

	// Очистка должна пройти без паники
	time.Sleep(150 * time.Millisecond)
}

// TestRetentionWorkerEmptyStorage проверяет очистку пустого хранилища.
func TestRetentionWorkerEmptyStorage(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	config := DefaultRetentionConfig()
	worker := NewRetentionWorker(st, config)

	ctx := context.Background()
	worker.Start(ctx)
	defer worker.Stop()

	// Очистка пустого хранилища должна пройти без ошибок
	time.Sleep(150 * time.Millisecond)
}
