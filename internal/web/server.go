// Package web предоставляет HTTP-обработчики и веб-интерфейс.
//
// Использует стандартную библиотеку net/http и html/template.
// Без фреймворков, без Node.js, без сборщиков.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"videoguard/internal/camera"
	"videoguard/internal/config"
	"videoguard/internal/events"
	"videoguard/internal/network"
	"videoguard/internal/recorder"
	"videoguard/internal/storage"
	"videoguard/internal/stream"
)

//go:embed templates/* static/css/* static/js/* static/img/*
var embeddedFiles embed.FS

// startTime отмечает время запуска сервера.
var startTime = time.Now()

// templatesMap хранит распаршенные шаблоны.
var templatesMap map[string]*template.Template

// initTemplates загружает и парсит все HTML-шаблоны.
func initTemplates() {
	templatesMap = make(map[string]*template.Template)

	// Загрузить все шаблоны из templates/
	tmpls, err := template.New("").Funcs(template.FuncMap{
		"eq": func(a, b interface{}) bool {
			return a == b
		},
		"gt": func(a, b int) bool {
			return a > b
		},
		"formatTime": func(t time.Time) string {
			return t.Format("02.01.2006 15:04:05")
		},
		"formatBytes": storage.FormatBytes,
	}).ParseFS(embeddedFiles, "templates/*.html")

	if err != nil {
		log.Printf("Ошибка загрузки шаблонов: %v", err)
		return
	}

	// Разделить шаблоны на отдельные файлы
	for _, tmpl := range tmpls.Templates() {
		name := tmpl.Name()
		// Убрать расширение .html
		base := strings.TrimSuffix(filepath.Base(name), ".html")
		templatesMap[base] = tmpl
	}
}

// Server представляет HTTP-сервер приложения.
type Server struct {
	addr         string
	readTimeout  time.Duration
	writeTimeout time.Duration
	cfg          *config.Config
	eventMgr     *events.EventManager
	storage      *storage.Storage
	camMgr       *camera.CameraManager
	rec          *recorder.Recorder
	streamMgr    *stream.StreamManager // MJPEG streams
	mux          *http.ServeMux
	devMode      bool
	logLevel     int
	httpServer   *http.Server // ссылка для graceful shutdown
}

// NewServer создаёт новый HTTP-сервер.
func NewServer(cfg *config.Config, eventMgr *events.EventManager, st *storage.Storage, devMode bool, camMgr *camera.CameraManager, rec *recorder.Recorder) *Server {
	// Инициализировать шаблоны один раз
	if templatesMap == nil {
		initTemplates()
	}

	s := &Server{
		addr:         cfg.Server.Listen,
		readTimeout:  cfg.Server.ReadTimeout,
		writeTimeout: cfg.Server.WriteTimeout,
		cfg:          cfg,
		eventMgr:     eventMgr,
		storage:      st,
		camMgr:       camMgr,
		rec:          rec,
		streamMgr:    stream.NewStreamManager(),
		mux:          http.NewServeMux(),
		devMode:      devMode,
		logLevel:     cfg.ParseLogLevel(),
	}

	s.registerRoutes()

	return s
}

// registerRoutes регистрирует все маршруты.
func (s *Server) registerRoutes() {
	// Веб-интерфейс
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/live", s.handleLive)
	s.mux.HandleFunc("/events", s.handleEventsPage)
	s.mux.HandleFunc("/archive", s.handleArchive)
	s.mux.HandleFunc("/settings", s.handleSettings)

	// Статические файлы
	s.mux.HandleFunc("/static/", s.handleStatic)

	// API
	s.mux.HandleFunc("/api/v1/health", s.handleHealth)
	s.mux.HandleFunc("/api/v1/status", s.handleStatus)
	s.mux.HandleFunc("/api/v1/diagnostics", s.handleDiagnostics)
	s.mux.HandleFunc("/api/v1/cameras", s.handleCameras)
	s.mux.HandleFunc("/api/v1/events", s.handleEventsAPI)
	s.mux.HandleFunc("/api/v1/storage", s.handleStorageAPI)

	// Live MJPEG stream
	s.mux.HandleFunc("/api/v1/stream/mjpeg/", s.handleMJPEGStream)

	// Snapshot
	s.mux.HandleFunc("/api/v1/snapshot/", s.handleSnapshot)

	// Защита от несуществующих путей
	s.mux.HandleFunc("/api/", s.handleAPINotFound)
}

// Start запускает HTTP-сервер.
func (s *Server) Start() error {
	log.Printf("[HTTP] Запуск HTTP-сервера на %s", s.addr)

	s.httpServer = &http.Server{
		Addr:         s.addr,
		Handler:      s.mux,
		ReadTimeout:  s.readTimeout,
		WriteTimeout: s.writeTimeout,
	}

	return s.httpServer.ListenAndServe()
}

// Shutdown останавливает HTTP-сервер gracefully.
// Ожидает завершения активных запросов (до 30 секунд).
func (s *Server) Shutdown() error {
	log.Printf("[HTTP] Остановка HTTP-сервера")
	if s.httpServer == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := s.httpServer.Shutdown(ctx)
	if err != nil {
		log.Printf("[HTTP] Ошибка при остановке сервера: %v", err)
		return err
	}

	log.Printf("[HTTP] HTTP-сервер остановлен")
	return nil
}

// ============================================================
// Обработчики веб-интерфейса
// ============================================================

// handleIndex обрабатывает главную страницу.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Добавить LastSnapshot timestamp к каждой камере
	now := time.Now().Unix()
	cameras := make([]map[string]interface{}, 0, len(s.cfg.Cameras))
	for _, cam := range s.cfg.Cameras {
		camMap := map[string]interface{}{
			"ID":           cam.ID,
			"Name":         cam.Name,
			"RTSPURL":      cam.RTSPURL,
			"Status":       "offline",
			"LastSnapshot": now,
		}

		// Проверить статус камеры
		if c, ok := s.camMgr.GetCamera(cam.ID); ok {
			camMap["Status"] = c.State().String()
		}

		cameras = append(cameras, camMap)
	}

	data := map[string]interface{}{
		"Title":       "VideoGuard — Главная",
		"Page":        "index",
		"Uptime":      time.Since(startTime).String(),
		"TotalEvents": s.eventMgr.TotalCount(),
		"Cameras":     cameras,
		"DevMode":     s.devMode,
	}

	s.renderHTML(w, "index", data)
}

// handleLive обрабатывает страницу live-просмотра.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title":   "VideoGuard — Live",
		"Page":    "live",
		"Cameras": s.cfg.Cameras,
	}

	s.renderHTML(w, "live", data)
}

// handleEventsPage обрабатывает страницу событий.
func (s *Server) handleEventsPage(w http.ResponseWriter, r *http.Request) {
	cameraID := r.URL.Query().Get("camera_id")
	eventType := events.EventType(r.URL.Query().Get("type"))
	limit := 50

	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	allEvents := s.eventMgr.List(cameraID, eventType, time.Time{}, time.Time{}, limit)

	data := map[string]interface{}{
		"Title":        "VideoGuard — События",
		"Page":         "events",
		"Events":       allEvents,
		"Cameras":      s.cfg.Cameras,
		"FilterCamera": cameraID,
		"FilterType":   string(eventType),
	}

	s.renderHTML(w, "events", data)
}

// handleArchive обрабатывает страницу архива.
func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	cameraID := r.URL.Query().Get("camera_id")
	dateStr := r.URL.Query().Get("date")

	var segments []string
	var selectedDate time.Time

	if cameraID != "" && dateStr != "" {
		selectedDate, _ = time.Parse("2006-01-02", dateStr)
		if !selectedDate.IsZero() {
			segments, _ = s.storage.ListSegmentsByDate(cameraID, selectedDate)
		}
	}

	data := map[string]interface{}{
		"Title":          "VideoGuard — Архив",
		"Page":           "archive",
		"Cameras":        s.cfg.Cameras,
		"Segments":       segments,
		"SelectedDate":   dateStr,
		"SelectedCamera": cameraID,
	}

	s.renderHTML(w, "archive", data)
}

// handleSettings обрабатывает страницу настроек.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	safeCfg := *s.cfg
	for i := range safeCfg.Cameras {
		safeCfg.Cameras[i].Password = "***"
	}

	data := map[string]interface{}{
		"Title":  "VideoGuard — Настройки",
		"Page":   "settings",
		"Config": safeCfg,
	}

	s.renderHTML(w, "settings", data)
}

// handleStatic обрабатывает запросы статических файлов.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	filePath := strings.TrimPrefix(r.URL.Path, "/static/")

	// Защита от path traversal
	if strings.Contains(filePath, "..") {
		http.Error(w, "Недопустимый путь", http.StatusBadRequest)
		return
	}

	// Очистить путь
	filePath = filepath.Clean(filePath)
	if filePath == "" || filePath == "." {
		http.Error(w, "Недопустимый путь", http.StatusBadRequest)
		return
	}

	// Serve content from embedded files
	// Файлы в embedded FS лежат в директории "static/", поэтому добавляем префикс
	handler := http.FileServer(http.FS(embeddedFiles))
	req := r.Clone(r.Context())
	req.URL.Path = "/static/" + filePath
	handler.ServeHTTP(w, req)
}

// ============================================================
// Обработчики API
// ============================================================

// handleHealth обрабатывает проверку работоспособности.
// Возвращает отдельные статусы для каждого компонента.
// application=ok только если все критичные компоненты работают.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	now := time.Now().UTC()

	health := map[string]interface{}{
		"status":    "ok",
		"timestamp": now.Format(time.RFC3339),
		"components": map[string]interface{}{
			"application": map[string]string{"status": "ok"},
			"camera":      map[string]string{"status": "unknown"},
			"recorder":    map[string]string{"status": "unknown"},
			"storage":     map[string]string{"status": "ok"},
			"notifier":    map[string]string{"status": "unknown"},
			"network":     map[string]string{"status": "unknown"},
		},
	}

	components := health["components"].(map[string]interface{})
	overallOK := true

	// --- Camera ---
	if s.camMgr != nil {
		states := s.camMgr.GetAllStates()
		onlineCount := 0
		offlineCount := 0
		for id, state := range states {
			// Добавить статус каждой камеры
			camComp := map[string]interface{}{
				"status": state,
				"id":     id,
			}
			components[id] = camComp

			if state == "online" {
				onlineCount++
			} else {
				offlineCount++
			}
		}

		if len(states) == 0 {
			components["camera"] = map[string]interface{}{
				"status":  "no_cameras",
				"total":   0,
				"online":  0,
				"offline": 0,
			}
		} else {
			components["camera"] = map[string]interface{}{
				"status":  "ok",
				"total":   len(states),
				"online":  onlineCount,
				"offline": offlineCount,
			}
			// Camera component healthy если хотя бы одна онлайн
			if onlineCount > 0 {
				components["camera"].(map[string]interface{})["status"] = "online"
			} else {
				components["camera"].(map[string]interface{})["status"] = "offline"
				overallOK = false
			}
		}
	}

	// --- Recorder ---
	if s.rec != nil && s.rec.IsRunning() {
		components["recorder"] = map[string]interface{}{
			"status": "recording",
		}
	} else if s.rec != nil {
		components["recorder"] = map[string]interface{}{
			"status": "stopped",
		}
		// Recorder не критичен в dev mode
	}

	// --- Storage ---
	_, err := storage.DiskUsage(s.storage.Root)
	if err != nil {
		components["storage"] = map[string]interface{}{
			"status": "error",
			"error":  err.Error(),
		}
		overallOK = false
	}

	// --- Notifier ---
	// MAX offline не делает сервис unhealthy
	if s.cfg.Notifier.MAX.Enabled {
		components["notifier"] = map[string]interface{}{
			"status": "configured",
			"type":   "max",
		}
	} else {
		components["notifier"] = map[string]interface{}{
			"status": "disabled",
		}
	}

	// --- Network ---
	netStatus := network.GetNetworkStatus()
	networkComp := map[string]interface{}{
		"status":    "offline",
		"connected": netStatus.Connected,
	}
	if netStatus.IP != "" {
		networkComp["local_ip"] = netStatus.IP
	}
	if netStatus.Connected {
		networkComp["status"] = "online"
	}
	components["network"] = networkComp

	// --- Overall status ---
	if !overallOK {
		health["status"] = "degraded"
	}

	s.writeJSON(w, http.StatusOK, health)
}

// handleStatus обрабатывает запрос статуса системы.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	diskUsage, _ := storage.DiskUsage(s.storage.Root)
	netStatus := network.GetNetworkStatus()

	status := map[string]interface{}{
		"uptime":       time.Since(startTime).String(),
		"cameras":      len(s.cfg.Cameras),
		"total_events": s.eventMgr.TotalCount(),
		"cpu_count":    runtime.NumCPU(),
		"storage": map[string]interface{}{
			"root":         s.storage.Root,
			"total":        diskUsage,
			"used":         diskUsage,
			"free":         diskUsage,
			"percent_used": diskUsage,
		},
		"network": map[string]interface{}{
			"connected": netStatus.Connected,
			"ip":        netStatus.IP,
			"modem":     netStatus.Modem,
		},
	}

	s.writeJSON(w, http.StatusOK, status)
}

// handleDiagnostics возвращает полную диагностическую информацию.
// GET /api/v1/diagnostics
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	now := time.Now().UTC()

	diag := map[string]interface{}{
		"timestamp": now.Format(time.RFC3339),
		"system":    s.diagSystem(),
		"camera":    s.diagCameras(),
		"recorder":  s.diagRecorder(),
		"storage":   s.diagStorage(),
		"notifier":  s.diagNotifier(),
		"network":   s.diagNetwork(),
	}

	s.writeJSON(w, http.StatusOK, diag)
}

// diagSystem собирает системную информацию.
func (s *Server) diagSystem() map[string]interface{} {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return map[string]interface{}{
		"hostname":       "", // TODO: os.Hostname()
		"os":             runtime.GOOS,
		"architecture":   runtime.GOARCH,
		"go_version":     runtime.Version(),
		"uptime":         time.Since(startTime).String(),
		"num_cpu":        runtime.NumCPU(),
		"num_goroutine":  runtime.NumGoroutine(),
		"memory_alloc":   m.Alloc,
		"memory_sys":     m.Sys,
		"memory_lookups": m.NumGC,
	}
}

// diagCameras собирает информацию о камерах.
func (s *Server) diagCameras() []map[string]interface{} {
	result := make([]map[string]interface{}, 0)

	if s.camMgr == nil {
		return result
	}

	states := s.camMgr.GetAllStates()
	cameras := s.camMgr.ListCameras()

	for _, cam := range cameras {
		state, ok := states[cam.ID()]
		if !ok {
			state = "unknown"
		}

		camInfo := map[string]interface{}{
			"id":              cam.ID(),
			"name":            cam.Name(),
			"status":          state,
			"reconnect_count": cam.ReconnectAttempts(),
			"uptime":          cam.Uptime().String(),
		}

		// Убрать rtsp URL из диагностики (безопасность)
		// camInfo["rtsp_url"] = cam.RTSPURL()

		result = append(result, camInfo)
	}

	return result
}

// diagRecorder собирает информацию о рекордере.
func (s *Server) diagRecorder() map[string]interface{} {
	rec := map[string]interface{}{
		"running": s.rec.IsRunning(),
	}

	if s.rec != nil && s.rec.IsRunning() {
		rec["camera_count"] = len(s.camMgr.ListCameras())
	}

	return rec
}

// diagStorage собирает информацию о хранилище.
func (s *Server) diagStorage() map[string]interface{} {
	usage, err := storage.DiskUsage(s.storage.Root)
	if err != nil {
		return map[string]interface{}{
			"status": "error",
			"error":  err.Error(),
		}
	}

	// Размер SQLite
	dbSize := int64(0)
	dbPath := filepath.Join(s.storage.Root, "events.db")
	if info, err := os.Stat(dbPath); err == nil {
		dbSize = info.Size()
	}

	return map[string]interface{}{
		"root":           s.storage.Root,
		"total":          usage.Total,
		"used":           usage.Used,
		"free":           usage.Free,
		"retention_days": s.cfg.Storage.RetentionDays,
		"sqlite_size":    dbSize,
	}
}

// diagNotifier собирает информацию о уведомителе.
func (s *Server) diagNotifier() map[string]interface{} {
	result := map[string]interface{}{
		"enabled": s.cfg.Notifier.MAX.Enabled,
	}

	if s.cfg.Notifier.MAX.Enabled {
		result["type"] = "max"
		result["retry_max"] = s.cfg.Notifier.MAX.RetryMax
		result["retry_delay"] = s.cfg.Notifier.MAX.RetryDelay.String()
	}

	return result
}

// diagNetwork собирает информацию о сети.
func (s *Server) diagNetwork() map[string]interface{} {
	netStatus := network.GetNetworkStatus()

	result := map[string]interface{}{
		"online":   netStatus.Connected,
		"local_ip": netStatus.IP,
	}

	if netStatus.Connected {
		result["status"] = "online"
	} else {
		result["status"] = "offline"
	}

	return result
}

// handleCameras обрабатывает запрос списка камер.
func (s *Server) handleCameras(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	cameras := make([]map[string]interface{}, 0)
	for _, cam := range s.cfg.Cameras {
		cameras = append(cameras, map[string]interface{}{
			"id":       cam.ID,
			"name":     cam.Name,
			"rtsp_url": config.SanitizeRTSPURL(cam.RTSPURL),
		})
	}

	s.writeJSON(w, http.StatusOK, cameras)
}

// handleEventsAPI обрабатывает запросы к API событий.
func (s *Server) handleEventsAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getEventsAPI(w, r)
	case http.MethodPost:
		s.createEventAPI(w, r)
	default:
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
	}
}

// getEventsAPI обрабатывает GET /api/v1/events.
func (s *Server) getEventsAPI(w http.ResponseWriter, r *http.Request) {
	cameraID := r.URL.Query().Get("camera_id")
	eventType := events.EventType(r.URL.Query().Get("type"))
	limit := 50

	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	eventList := s.eventMgr.List(cameraID, eventType, time.Time{}, time.Time{}, limit)

	s.writeJSON(w, http.StatusOK, eventList)
}

// createEventAPI обрабатывает POST /api/v1/events.
func (s *Server) createEventAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Type     events.EventType   `json:"type"`
		CameraID string             `json:"camera_id"`
		Source   events.EventSource `json:"source"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Неверный формат запроса",
		})
		return
	}

	if input.Type == "" {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "type обязателен",
		})
		return
	}

	if input.CameraID == "" {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "camera_id обязателен",
		})
		return
	}

	event := &events.Event{
		Type:      input.Type,
		CameraID:  input.CameraID,
		Source:    input.Source,
		StartedAt: time.Now(),
	}

	if err := events.ValidateEvent(event); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	if err := s.eventMgr.Create(event); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Не удалось создать событие",
		})
		return
	}

	log.Printf("[API] Событие создано: %s (%s)", event.ID, event.Type)

	s.writeJSON(w, http.StatusCreated, event)
}

// handleStorageAPI обрабатывает запрос информации о хранилище.
func (s *Server) handleStorageAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	diskUsage, err := storage.DiskUsage(s.storage.Root)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Не удалось получить информацию о хранилище",
		})
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"root":         s.storage.Root,
		"total":        diskUsage.Total,
		"used":         diskUsage.Used,
		"free":         diskUsage.Free,
		"available":    diskUsage.Available,
		"percent_used": diskUsage.PercentUsed,
	})
}

// handleAPINotFound обрабатывает несуществующие конечные точки API.
func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusNotFound, map[string]string{
		"error": "Конечная точка API не найдена",
	})
}

// ============================================================
// Live Stream & Snapshot
// ============================================================

// handleMJPEGStream обрабатывает MJPEG поток для камеры.
// URL: /api/v1/stream/mjpeg/{camera-id}
// Запускает FFmpeg "по требованию" — один процесс на всех зрителей.
func (s *Server) handleMJPEGStream(w http.ResponseWriter, r *http.Request) {
	// Извлечь camera-id из URL
	cameraID := strings.TrimPrefix(r.URL.Path, "/api/v1/stream/mjpeg/")
	if cameraID == "" {
		http.Error(w, "camera_id обязателен", http.StatusBadRequest)
		return
	}

	// Найти камеру
	cam, ok := s.camMgr.GetCamera(cameraID)
	if !ok {
		http.Error(w, "камера не найдена", http.StatusNotFound)
		return
	}

	// Получить или создать поток
	stream := s.streamMgr.GetStream(cameraID, cam.RTSPURL())
	if stream == nil {
		http.Error(w, "не удалось запустить поток", http.StatusServiceUnavailable)
		return
	}

	// Установить заголовки для MJPEG
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=123456789009876543210")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Получить reader из потока
	reader := stream.Reader()
	if reader == nil {
		http.Error(w, "нет данных от FFmpeg", http.StatusServiceUnavailable)
		return
	}

	// Копировать данные в response
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(w, reader)
	}()

	// Ждать пока клиент не отключится
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming не поддерживается", http.StatusInternalServerError)
		return
	}

	flusher.Flush()

	// Ждать завершения или отключения клиента
	select {
	case <-done:
		// FFmpeg завершил работу
	case <-r.Context().Done():
		// Клиент отключился
	}

	// Уменьшить счётчик зрителей
	s.streamMgr.ReleaseStream(cameraID)
}

// handleSnapshot обрабатывает запрос на создание снимка.
// URL: /api/v1/snapshot/{camera-id}
// Возвращает JPEG-снимок один раз.
func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	// Извлечь camera-id из URL
	cameraID := strings.TrimPrefix(r.URL.Path, "/api/v1/snapshot/")
	if cameraID == "" {
		http.Error(w, "camera_id обязателен", http.StatusBadRequest)
		return
	}

	// Найти камеру
	cam, ok := s.camMgr.GetCamera(cameraID)
	if !ok {
		http.Error(w, "камера не найдена", http.StatusNotFound)
		return
	}

	// Сделать снимок
	snapshot, err := stream.TakeSnapshot(cam.RTSPURL())
	if err != nil {
		log.Printf("[Snapshot] Ошибка создания снимка для %s: %v", cameraID, err)
		http.Error(w, "не удалось создать снимок", http.StatusServiceUnavailable)
		return
	}

	// Вернуть JPEG
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(snapshot)
}

// ============================================================
// Вспомогательные методы
// ============================================================

// writeJSON отправляет JSON-ответ.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// renderHTML рендерит HTML-шаблон.
func (s *Server) renderHTML(w http.ResponseWriter, templateName string, data map[string]interface{}) {
	tmpl, ok := templatesMap[templateName]
	if !ok {
		http.NotFound(w, nil)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("Ошибка рендеринга шаблона %s: %v", templateName, err)
	}
}
