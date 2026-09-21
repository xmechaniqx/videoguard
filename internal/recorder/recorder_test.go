package recorder

import (
	"testing"
	"time"

	"videoguard/internal/camera"
	"videoguard/internal/config"
	"videoguard/internal/storage"
)

// TestNewRecorder проверяет создание рекордера.
func TestNewRecorder(t *testing.T) {
	cfg := config.RecordingConfig{
		Enabled:         true,
		SegmentDuration: 300 * time.Second,
		CodecCopy:       true,
	}

	camMgr := camera.NewCameraManager()
	tmpDir := t.TempDir()
	st := storage.NewStorage(tmpDir)

	rec := NewRecorder(cfg, camMgr, st)

	if rec == nil {
		t.Fatal("ожидался рекордер, получено nil")
	}

	// Проверить что IsRunning возвращает false по умолчанию
	if rec.IsRunning() {
		t.Error("ожидалось что IsRunning вернёт false по умолчанию")
	}
}

// TestRecorderStartStop проверяет запуск и остановку записи.
func TestRecorderStartStop(t *testing.T) {
	cfg := config.RecordingConfig{
		Enabled:         true,
		SegmentDuration: 300 * time.Second,
		CodecCopy:       true,
	}

	camMgr := camera.NewCameraManager()
	tmpDir := t.TempDir()
	st := storage.NewStorage(tmpDir)

	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	rec := NewRecorder(cfg, camMgr, st)

	// Проверить, что запись не запущена
	if rec.IsRunning() {
		t.Error("ожидалось, что запись не запущена")
	}

	// Запустить запись (без камер — должен вернуть ошибку)
	err := rec.Start()
	if err == nil {
		t.Error("ожидалась ошибка при отсутствии камер")
	}

	// Остановка должна быть безопасной
	if err := rec.Stop(); err != nil {
		t.Errorf("остановка без запуска не должна вызывать ошибку: %v", err)
	}
}

// TestRecorderConfigValidation проверяет валидацию конфигурации рекордера.
func TestRecorderConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		cfg       config.RecordingConfig
		wantError bool
	}{
		{
			name: "валидная конфигурация",
			cfg: config.RecordingConfig{
				Enabled:         true,
				SegmentDuration: 300 * time.Second,
				CodecCopy:       true,
			},
			wantError: false,
		},
		{
			name: "отключённая запись",
			cfg: config.RecordingConfig{
				Enabled:         false,
				SegmentDuration: 300 * time.Second,
				CodecCopy:       true,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			camMgr := camera.NewCameraManager()
			tmpDir := t.TempDir()
			st := storage.NewStorage(tmpDir)

			rec := NewRecorder(tt.cfg, camMgr, st)
			if rec == nil {
				t.Fatal("ожидался рекордер, получено nil")
			}

			// Проверить duration
			if tt.cfg.SegmentDuration < 60*time.Second && tt.cfg.Enabled {
				// Ожидаем, что короткая длительность будет проблемой
			}
		})
	}
}

// TestRecorderGracefulShutdown проверяет корректное завершение работы.
func TestRecorderGracefulShutdown(t *testing.T) {
	cfg := config.RecordingConfig{
		Enabled:         true,
		SegmentDuration: 300 * time.Second,
		CodecCopy:       true,
	}

	camMgr := camera.NewCameraManager()
	tmpDir := t.TempDir()
	st := storage.NewStorage(tmpDir)

	rec := NewRecorder(cfg, camMgr, st)

	// Остановка без запуска должна быть безопасной
	if err := rec.Stop(); err != nil {
		t.Errorf("остановка без запуска не должна вызывать ошибку: %v", err)
	}

	// Проверить, что IsRunning возвращает false
	if rec.IsRunning() {
		t.Error("ожидалось, что запись не запущена после остановки")
	}

	// Повторная остановка должна быть безопасной
	if err := rec.Stop(); err != nil {
		t.Errorf("повторная остановка не должна вызывать ошибку: %v", err)
	}
}
