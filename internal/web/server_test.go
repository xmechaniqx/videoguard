package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"videoguard/internal/camera"
	"videoguard/internal/config"
	"videoguard/internal/db"
	"videoguard/internal/events"
	"videoguard/internal/recorder"
	"videoguard/internal/storage"
)

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

// setupTestServer создаёт тестовый HTTP сервер с необходимыми зависимостями.
func setupTestServer(t *testing.T) (*Server, *events.EventManager) {
	t.Helper()

	cfg := config.DefaultConfig()

	d, path := openTestDB(t)
	t.Cleanup(func() { closeTestDB(t, d); os.Remove(path) })

	eventMgr := events.NewEventManager(d, 0)

	tmpDir := t.TempDir()
	st := storage.NewStorage(tmpDir)
	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	camMgr := camera.NewCameraManager()
	rec := recorder.NewRecorder(cfg.Recording, camMgr, st)

	server := NewServer(&cfg, eventMgr, st, false, camMgr, rec)

	return server, eventMgr
}

// TestHandleHealth проверяет обработчик health check.
func TestHandleHealth(t *testing.T) {
	server, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ожидался статус 200, получено %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("не удалось разобрать ответ: %v", err)
	}

	if response["status"] != "ok" {
		t.Errorf("ожидался статус 'ok', получено '%v'", response["status"])
	}
}

// TestHandleStatus проверяет обработчик статуса системы.
func TestHandleStatus(t *testing.T) {
	server, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ожидался статус 200, получено %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("не удалось разобрать ответ: %v", err)
	}

	if response["cameras"] == nil {
		t.Error("ожидалось поле 'cameras' в ответе")
	}
}

// TestHandleCameras проверяет обработчик списка камер.
func TestHandleCameras(t *testing.T) {
	server, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cameras", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ожидался статус 200, получено %d", w.Code)
	}

	var response []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("не удалось разобрать ответ: %v", err)
	}

	cfg := config.DefaultConfig()
	if len(response) != len(cfg.Cameras) {
		t.Errorf("ожидалось %d камер, получено %d", len(cfg.Cameras), len(response))
	}

	// Проверить, что учётные данные не в ответе
	for _, cam := range response {
		if rtspURL, ok := cam["rtsp_url"].(string); ok {
			if containsSubstring(rtspURL, "PASS") || containsSubstring(rtspURL, "secret") {
				t.Error("учётные данные найдены в ответе API")
			}
		}
	}
}

// TestHandleEventsAPIPost проверяет POST /api/v1/events.
func TestHandleEventsAPIPost(t *testing.T) {
	server, _ := setupTestServer(t)

	// Валидный запрос
	validBody := `{"type": "motion", "camera_id": "camera-1", "source": "motion"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", jsonReader(validBody))
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("ожидался статус 201, получено %d", w.Code)
	}

	var response events.Event
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("не удалось разобрать ответ: %v", err)
	}

	if response.Type != events.EventTypeMotion {
		t.Errorf("ожидался тип 'motion', получено '%s'", response.Type)
	}

	if response.CameraID != "camera-1" {
		t.Errorf("ожидался camera_id 'camera-1', получено '%s'", response.CameraID)
	}
}

// TestHandleEventsAPIPostValidation проверяет валидацию POST запроса.
func TestHandleEventsAPIPostValidation(t *testing.T) {
	server, _ := setupTestServer(t)

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "пустой тип",
			body:       `{"camera_id": "camera-1"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "пустой camera_id",
			body:       `{"type": "motion"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "неверный JSON",
			body:       `not json`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "пустое тело",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/events", jsonReader(tt.body))
			w := httptest.NewRecorder()

			server.mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("ожидался статус %d, получено %d", tt.wantStatus, w.Code)
			}
		})
	}
}

// TestHandleEventsAPIGet проверяет GET /api/v1/events.
func TestHandleEventsAPIGet(t *testing.T) {
	server, eventMgr := setupTestServer(t)

	// Добавить тестовое событие
	eventMgr.Create(&events.Event{
		ID:        "test-event",
		Type:      events.EventTypeMotion,
		CameraID:  "camera-1",
		Source:    events.EventSourceMotion,
		StartedAt: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events?camera_id=camera-1&limit=10", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ожидался статус 200, получено %d", w.Code)
	}

	var response []*events.Event
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("не удалось разобрать ответ: %v", err)
	}

	if len(response) != 1 {
		t.Errorf("ожидалось 1 событие, получено %d", len(response))
	}
}

// TestHandleStorageAPI проверяет обработчик информации о хранилище.
func TestHandleStorageAPI(t *testing.T) {
	server, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/storage", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ожидался статус 200, получено %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("не удалось разобрать ответ: %v", err)
	}

	if response["root"] == nil {
		t.Error("ожидалось поле 'root' в ответе")
	}
}

// TestHandleAPINotFound проверяет обработчик несуществующих API.
func TestHandleAPINotFound(t *testing.T) {
	server, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("ожидался статус 404, получено %d", w.Code)
	}

	var response map[string]string
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("не удалось разобрать ответ: %v", err)
	}

	if response["error"] == "" {
		t.Error("ожидалось сообщение об ошибке")
	}
}

// TestHandleStatic проверяет обработку статических файлов.
func TestHandleStatic(t *testing.T) {
	server, _ := setupTestServer(t)

	// Запрос CSS файла
	req := httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	// Статический файл должен быть найден (200) или возвращена ошибка embed (зависит от реализации)
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Errorf("ожидался статус 200 или 404, получено %d", w.Code)
	}
}

// TestHandleStaticPathTraversal проверяет защиту от path traversal в статике.
func TestHandleStaticPathTraversal(t *testing.T) {
	server, _ := setupTestServer(t)

	// Попытка path traversal
	req := httptest.NewRequest(http.MethodGet, "/static/../../etc/passwd", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	// Ожидаем 301/307 (redirect от FileServer) или 400 (path traversal blocked)
	if w.Code != http.StatusMovedPermanently && w.Code != http.StatusTemporaryRedirect && w.Code != http.StatusBadRequest {
		t.Errorf("ожидался статус 301, 307 или 400, получено %d", w.Code)
	}
}

// TestHandleEventsAPIMethodNotAllowed проверяет запрет неподдерживаемых методов.
func TestHandleEventsAPIMethodNotAllowed(t *testing.T) {
	server, _ := setupTestServer(t)

	// PUT метод не должен поддерживаться
	req := httptest.NewRequest(http.MethodPut, "/api/v1/events", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("ожидался статус 405, получено %d", w.Code)
	}
}

// jsonReader создаёт io.Reader из JSON-строки.
func jsonReader(s string) io.Reader {
	return &stringReaderWrapper{s: s}
}

type stringReaderWrapper struct {
	s string
	i int
}

func (r *stringReaderWrapper) Read(p []byte) (n int, err error) {
	if r.i >= len(r.s) {
		return 0, nil
	}
	n = copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}

// containsSubstring проверяет, содержит ли строка подстроку.
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
