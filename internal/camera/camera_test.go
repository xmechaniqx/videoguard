package camera

import (
	"context"
	"testing"

	"videoguard/internal/config"
)

// TestNewCamera проверяет создание камеры.
func TestNewCamera(t *testing.T) {
	cfg := config.CameraConfig{
		ID:       "test-camera",
		Name:     "Тестовая камера",
		RTSPURL:  "rtsp://test:pass@192.168.1.100:554/stream",
		Username: "test",
		Password: "pass",
	}

	cam := NewCamera(cfg)

	if cam == nil {
		t.Fatal("ожидалась камера, получено nil")
	}

	if cam.ID() != "test-camera" {
		t.Errorf("ожидался ID 'test-camera', получено '%s'", cam.ID())
	}

	if cam.Name() != "Тестовая камера" {
		t.Errorf("ожидалось имя 'Тестовая камера', получено '%s'", cam.Name())
	}
}

// TestCameraState проверяет состояния камеры.
func TestCameraState(t *testing.T) {
	cfg := config.CameraConfig{
		ID:      "state-test",
		Name:    "State Test",
		RTSPURL: "rtsp://test:554/stream",
	}

	cam := NewCamera(cfg)

	// Начальное состояние — offline
	if cam.State() != StateOffline {
		t.Errorf("ожидалось состояние offline, получено %v", cam.State())
	}

	// Проверить строковые представления состояний
	if StateUnknown.String() != "unknown" {
		t.Errorf("ожидалось 'unknown', получено '%s'", StateUnknown.String())
	}
	if StateOnline.String() != "online" {
		t.Errorf("ожидалось 'online', получено '%s'", StateOnline.String())
	}
	if StateOffline.String() != "offline" {
		t.Errorf("ожидалось 'offline', получено '%s'", StateOffline.String())
	}
	if StateConnecting.String() != "connecting" {
		t.Errorf("ожидалось 'connecting', получено '%s'", StateConnecting.String())
	}
}

// TestCameraRTSPURLSanitization проверяет санитизацию RTSP URL.
func TestCameraRTSPURLSanitization(t *testing.T) {
	cfg := config.CameraConfig{
		ID:       "sanitize-test",
		Name:     "Sanitize Test",
		RTSPURL:  "rtsp://admin:secret123@192.168.1.100:554/stream1",
		Username: "admin",
		Password: "secret123",
	}

	cam := NewCamera(cfg)

	rtspURL := cam.RTSPURL()

	// Проверить, что учётные данные убраны
	if rtspURL == cfg.RTSPURL {
		t.Error("ожидалась санитизация RTSP URL")
	}

	// Проверить, что пароль не в URL
	if containsSubstring(rtspURL, "secret123") {
		t.Error("пароль найден в санитизированном URL")
	}

	// Проверить, что хост остался
	if !containsSubstring(rtspURL, "192.168.1.100") {
		t.Error("хост потерян после санитизации")
	}
}

// TestCameraManager проверяет менеджер камер.
func TestCameraManager(t *testing.T) {
	cm := NewCameraManager()

	// Добавить камеры
	cm.AddCamera(config.CameraConfig{
		ID:      "cam-1",
		Name:    "Camera 1",
		RTSPURL: "rtsp://test1:554/stream",
	})
	cm.AddCamera(config.CameraConfig{
		ID:      "cam-2",
		Name:    "Camera 2",
		RTSPURL: "rtsp://test2:554/stream",
	})

	// Проверить GetCamera
	gotCam1, ok := cm.GetCamera("cam-1")
	if !ok {
		t.Error("ожидалось получение камеры cam-1")
	}
	if gotCam1 == nil || gotCam1.ID() != "cam-1" {
		t.Errorf("ожидалась камера cam-1, получено %+v", gotCam1)
	}

	// Проверить несуществующую камеру
	_, ok = cm.GetCamera("nonexistent")
	if ok {
		t.Error("ожидалось false для несуществующей камеры")
	}

	// Проверить ListCameras
	allCams := cm.ListCameras()
	if len(allCams) != 2 {
		t.Errorf("ожидалось 2 камеры, получено %d", len(allCams))
	}
}

// TestCameraManagerStartAll проверяет что StartAll не паникует при пустом списке.
func TestCameraManagerStartAll(t *testing.T) {
	cm := NewCameraManager()

	// Без камер StartAll должен вернуть ошибку
	ctx := context.Background()
	err := cm.StartAll(ctx)
	if err == nil {
		t.Error("ожидалась ошибка при отсутствии камер")
	}
}

// TestCameraStopSafety проверяет безопасность остановки.
func TestCameraStopSafety(t *testing.T) {
	cfg := config.CameraConfig{
		ID:      "stop-safety",
		Name:    "Stop Safety",
		RTSPURL: "rtsp://test:554/stream",
	}

	cam := NewCamera(cfg)

	// Остановка без запуска должна быть безопасной
	err := cam.Stop()
	if err != nil {
		t.Errorf("остановка без запуска не должна вызывать ошибку: %v", err)
	}

	// Проверить, что состояние — offline
	if cam.State() != StateOffline {
		t.Errorf("ожидалось состояние offline, получено %v", cam.State())
	}
}

// TestCameraReconnectAttempts проверяет счётчик переподключений.
func TestCameraReconnectAttempts(t *testing.T) {
	cfg := config.CameraConfig{
		ID:      "reconnect-test",
		Name:    "Reconnect Test",
		RTSPURL: "rtsp://test:554/stream",
	}

	cam := NewCamera(cfg)

	// Начальное значение — 0
	if cam.ReconnectAttempts() != 0 {
		t.Errorf("ожидалось 0 попыток, получено %d", cam.ReconnectAttempts())
	}

	// Проверить, что метод не паникует
	// reconnect — unexported, проверяем через публичный API
	_ = cam.ReconnectAttempts
	// Метод должен завершиться без паники
}

// TestCameraUptime проверяет время работы.
func TestCameraUptime(t *testing.T) {
	cfg := config.CameraConfig{
		ID:      "uptime-test",
		Name:    "Uptime Test",
		RTSPURL: "rtsp://test:554/stream",
	}

	cam := NewCamera(cfg)

	// До запуска uptime должен быть 0
	if cam.Uptime() != 0 {
		t.Errorf("ожидался uptime 0 до запуска, получено %v", cam.Uptime())
	}
}

// TestCameraIsRunning проверяет статус запущенности.
func TestCameraIsRunning(t *testing.T) {
	cfg := config.CameraConfig{
		ID:      "running-test",
		Name:    "Running Test",
		RTSPURL: "rtsp://test:554/stream",
	}

	cam := NewCamera(cfg)

	// До запуска должен возвращать false
	if cam.IsRunning() {
		t.Error("ожидалось false до запуска")
	}

	// После остановки (без запуска) должен возвращать false
	cam.Stop()
	if cam.IsRunning() {
		t.Error("ожидалось false после остановки без запуска")
	}
}

// containsSubstring проверяет, содержит ли строка подстроку.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

// searchSubstring — внутренняя реализация поиска подстроки.
func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
