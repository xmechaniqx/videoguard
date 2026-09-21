// Package watchdog предоставляет три watchdog-компонента для мониторинга системы.
//
// Watchdog генерирует события при обнаружении проблем:
// - CameraWatchdog: проверяет RTSP поток через ffprobe каждые 15 секунд
// - RecorderWatchdog: проверяет что FFmpeg пишет сегменты
// - StorageWatchdog: проверяет диск, SMART, свободное место
package watchdog

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"videoguard/internal/config"
	"videoguard/internal/events"
	"videoguard/internal/storage"
)

// EventGenerator — интерфейс для генерации событий.
type EventGenerator interface {
	Create(event *events.Event) error
}

// ============================================================
// CameraWatchdog — проверяет RTSP поток через ffprobe
// ============================================================

// cameraState хранит текущее состояние каждой камеры.
type cameraState struct {
	offlineSince time.Time
	reconnects   int // счётчик переподключений
}

// CameraWatchdog проверяет доступность камер каждые 15 секунд.
type CameraWatchdog struct {
	eventMgr EventGenerator
	cameras  []config.CameraConfig
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	running  bool

	mu       sync.RWMutex
	state    map[string]*cameraState // cameraID -> state
	cooldown time.Duration           // cooldown между событиями одной камеры
}

// NewCameraWatchdog создаёт новый watchdog для камер.
func NewCameraWatchdog(eventMgr EventGenerator, cameras []config.CameraConfig) *CameraWatchdog {
	return &CameraWatchdog{
		eventMgr: eventMgr,
		cameras:  cameras,
		interval: 15 * time.Second,
		state:    make(map[string]*cameraState),
		cooldown: 5 * time.Minute, // минимальный интервал между событиями одной камеры
	}
}

// Start запускает мониторинг камер.
func (w *CameraWatchdog) Start(ctx context.Context) {
	w.ctx, w.cancel = context.WithCancel(ctx)
	w.running = true

	// Сразу проверить все камеры
	w.checkAll()

	// Запустить периодическую проверку
	go w.run()
}

// Stop останавливает мониторинг.
func (w *CameraWatchdog) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.running = false
	log.Printf("[Watchdog] CameraWatchdog остановлен")
}

func (w *CameraWatchdog) run() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.checkAll()
		}
	}
}

func (w *CameraWatchdog) checkAll() {
	for _, cam := range w.cameras {
		w.checkCamera(cam)
	}
}

// canEmit проверяет cooldown — не отправлять события одной камеры чаще cooldown.
func (w *CameraWatchdog) canEmit(cameraID string, eventType events.EventType) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	s, ok := w.state[cameraID]
	if !ok {
		return true
	}
	// Для camera_online всегда разрешаем (камера вернулась)
	if eventType == events.EventTypeCameraOnline {
		return true
	}
	return time.Since(s.offlineSince) > w.cooldown
}

// recordEvent записывает время события для камеры.
func (w *CameraWatchdog) recordEvent(cameraID string, eventType events.EventType) {
	w.mu.Lock()
	defer w.mu.Unlock()

	s, ok := w.state[cameraID]
	if !ok {
		s = &cameraState{}
		w.state[cameraID] = s
	}

	if eventType == events.EventTypeCameraOffline || eventType == events.EventTypeCameraReconnecting {
		s.offlineSince = time.Now()
		s.reconnects++
	}
}

func (w *CameraWatchdog) checkCamera(cam config.CameraConfig) {
	if err := w.ffprobeCheck(cam.RTSPURL); err != nil {
		log.Printf("[Watchdog] Камера %s недоступна: %v", cam.ID, err)

		// Проверяем cooldown
		if !w.canEmit(cam.ID, events.EventTypeCameraReconnecting) {
			log.Printf("[Watchdog] Пропуск события для %s (cooldown)", cam.ID)
			return
		}

		// Генерировать событие camera_reconnecting при потере связи
		w.recordEvent(cam.ID, events.EventTypeCameraReconnecting)
		w.eventMgr.Create(&events.Event{
			Type:     events.EventTypeCameraReconnecting,
			CameraID: cam.ID,
			Source:   events.EventSourceCamera,
			Metadata: map[string]interface{}{
				"error":     err.Error(),
				"rtsp_url":  cam.RTSPURL,
				"reconnect": w.getReconnectCount(cam.ID),
			},
		})
		return
	}

	// Камера доступна — если была offline, генерируем camera_online
	w.mu.RLock()
	s, wasOffline := w.state[cam.ID]
	w.mu.RUnlock()

	if wasOffline && s.offlineSince.IsZero() == false {
		// Камера вернулась — проверяем cooldown
		if w.canEmit(cam.ID, events.EventTypeCameraOnline) {
			w.eventMgr.Create(&events.Event{
				Type:     events.EventTypeCameraOnline,
				CameraID: cam.ID,
				Source:   events.EventSourceCamera,
				Metadata: map[string]interface{}{
					"rtsp_url": cam.RTSPURL,
				},
			})
			log.Printf("[Watchdog] Камера %s доступна (вернулась)", cam.ID)
		} else {
			log.Printf("[Watchdog] Камера %s доступна, но пропуск события (cooldown)", cam.ID)
		}
	} else {
		log.Printf("[Watchdog] Камера %s доступна", cam.ID)
	}
}

func (w *CameraWatchdog) getReconnectCount(cameraID string) int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if s, ok := w.state[cameraID]; ok {
		return s.reconnects
	}
	return 0
}

func (w *CameraWatchdog) ffprobeCheck(rtspURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-timeout", "5000000",
		"-i", rtspURL,
		"-f", "null",
		"-",
	)
	cmd.Stdout = nil
	cmd.Stderr = nil

	return cmd.Run()
}

// ============================================================
// RecorderWatchdog — проверяет что FFmpeg пишет сегменты
// ============================================================

// RecorderWatchdog проверяет что сегменты записываются для всех камер.
type RecorderWatchdog struct {
	eventMgr EventGenerator
	storage  *storage.Storage
	cameras  []config.CameraConfig
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	running  bool

	mu       sync.RWMutex
	prevSize map[string]int64 // cameraID -> размер последнего проверенного сегмента
}

// NewRecorderWatchdog создаёт новый watchdog для рекордера.
func NewRecorderWatchdog(eventMgr EventGenerator, st *storage.Storage, cameras []config.CameraConfig) *RecorderWatchdog {
	return &RecorderWatchdog{
		eventMgr: eventMgr,
		storage:  st,
		cameras:  cameras,
		interval: 60 * time.Second, // Проверять каждые 60 секунд
		prevSize: make(map[string]int64),
	}
}

// Start запускает мониторинг записи.
func (w *RecorderWatchdog) Start(ctx context.Context) {
	w.ctx, w.cancel = context.WithCancel(ctx)
	w.running = true

	// Сразу проверить
	w.checkAll()

	// Запустить периодическую проверку
	go w.run()
}

// Stop останавливает мониторинг.
func (w *RecorderWatchdog) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.running = false
	log.Printf("[Watchdog] RecorderWatchdog остановлен")
}

func (w *RecorderWatchdog) run() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.checkAll()
		}
	}
}

func (w *RecorderWatchdog) checkAll() {
	for _, cam := range w.cameras {
		w.checkRecording(cam)
	}
}

func (w *RecorderWatchdog) checkRecording(cam config.CameraConfig) {
	cameraID := cam.ID
	now := time.Now()

	// Проверить сегменты за последние 10 минут (несколько дат на случай перехода)
	var latestSegPath string
	var latestModTime time.Time
	var latestSize int64

	for d := 0; d < 2; d++ {
		checkDate := now.AddDate(0, 0, -d)
		segments, err := w.storage.ListSegmentsByDate(cameraID, checkDate)
		if err != nil {
			continue
		}
		for _, segName := range segments {
			// Файлы сегментов имеют имена вида 15-04-05.mp4, нужно найти последний по имени
			dir := w.storage.PathRecordingByDate(cameraID, checkDate)
			fullPath := filepath.Join(dir, segName)
			info, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			if info.ModTime().After(latestModTime) || latestSegPath == "" {
				latestSegPath = fullPath
				latestModTime = info.ModTime()
				latestSize = info.Size()
			}
		}
	}

	// Если нет сегментов вообще — запись остановлена
	if latestSegPath == "" {
		log.Printf("[Watchdog] Нет сегментов для камеры %s", cameraID)
		w.eventMgr.Create(&events.Event{
			Type:     events.EventTypeRecordingStopped,
			CameraID: cameraID,
			Source:   events.EventSourceSystem,
			Metadata: map[string]interface{}{
				"reason": "no_segments_found",
			},
		})
		return
	}

	// Проверить модификацию: если последний сегмент не менялся > 2 сегмент-дюрэйнов — стоп
	segmentDuration := 300 * time.Second // default
	w.mu.RLock()
	prevSize, hasPrev := w.prevSize[cameraID]
	w.mu.RUnlock()

	// Check modification time staleness
	staleThreshold := 2 * segmentDuration
	if time.Since(latestModTime) > staleThreshold {
		log.Printf("[Watchdog] Запись остановлена для %s: сегмент не модифицируется %v", cameraID, time.Since(latestModTime).Round(time.Second))
		w.eventMgr.Create(&events.Event{
			Type:     events.EventTypeRecordingStopped,
			CameraID: cameraID,
			Source:   events.EventSourceSystem,
			Metadata: map[string]interface{}{
				"reason":           "stalled_recording",
				"last_modified":    latestModTime.Format(time.RFC3339),
				"stalled_duration": time.Since(latestModTime).String(),
				"segment_path":     latestSegPath,
				"segment_size":     latestSize,
			},
		})
		// Сбросить prevSize чтобы не дублировать событие слишком часто
		w.mu.Lock()
		w.prevSize[cameraID] = -1
		w.mu.Unlock()
		return
	}

	// Check size growth: если размер не изменился с предыдущей проверки — возможно stall
	if hasPrev && prevSize > 0 {
		growth := latestSize - prevSize
		if growth == 0 {
			log.Printf("[Watchdog] Запись не растёт для %s: размер сегмента не изменился (%d байт)", cameraID, latestSize)
			w.eventMgr.Create(&events.Event{
				Type:     events.EventTypeRecordingStopped,
				CameraID: cameraID,
				Source:   events.EventSourceSystem,
				Metadata: map[string]interface{}{
					"reason":       "no_size_growth",
					"segment_path": latestSegPath,
					"segment_size": latestSize,
					"prev_segment": prevSize,
					"growth_bytes": growth,
				},
			})
		}
	}

	// Обновить размер для следующей проверки
	w.mu.Lock()
	w.prevSize[cameraID] = latestSize
	w.mu.Unlock()
}

// ============================================================
// StorageWatchdog — проверяет диск, SMART, свободное место
// ============================================================

// StorageWatchdog проверяет состояние хранилища.
type StorageWatchdog struct {
	eventMgr EventGenerator
	storage  *storage.Storage
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	running  bool
}

// NewStorageWatchdog создаёт новый watchdog для хранилища.
func NewStorageWatchdog(eventMgr EventGenerator, st *storage.Storage) *StorageWatchdog {
	return &StorageWatchdog{
		eventMgr: eventMgr,
		storage:  st,
		interval: 30 * time.Second, // Проверять каждые 30 секунд
	}
}

// Start запускает мониторинг хранилища.
func (w *StorageWatchdog) Start(ctx context.Context) {
	w.ctx, w.cancel = context.WithCancel(ctx)
	w.running = true

	// Сразу проверить
	w.checkAll()

	// Запустить периодическую проверку
	go w.run()
}

// Stop останавливает мониторинг.
func (w *StorageWatchdog) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.running = false
	log.Printf("[Watchdog] StorageWatchdog остановлен")
}

func (w *StorageWatchdog) run() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.checkAll()
		}
	}
}

func (w *StorageWatchdog) checkAll() {
	// Проверить использование диска
	usage, err := storage.DiskUsage(w.storage.Root)
	if err != nil {
		log.Printf("[Watchdog] Ошибка проверки диска: %v", err)
		return
	}

	log.Printf("[Watchdog] Диск: %.1f%% использовано, %.1f GB свободно",
		usage.PercentUsed, float64(usage.Free)/1e9)

	// Проверить emergency threshold
	if usage.PercentUsed > 90 {
		log.Printf("[Watchdog] КРИТИЧНО: диск заполнен на %.1f%%", usage.PercentUsed)
		w.eventMgr.Create(&events.Event{
			Type:     events.EventTypeStorageFull,
			CameraID: "system",
			Source:   events.EventSourceSystem,
			Metadata: map[string]interface{}{
				"percent_used": usage.PercentUsed,
				"free_bytes":   usage.Free,
			},
		})
		return
	}

	// Проверить warning threshold
	if usage.PercentUsed > 75 {
		log.Printf("[Watchdog] ПРЕДУПРЕЖДЕНИЕ: диск заполнен на %.1f%%", usage.PercentUsed)
		w.eventMgr.Create(&events.Event{
			Type:     events.EventTypeStorageWarning,
			CameraID: "system",
			Source:   events.EventSourceSystem,
			Metadata: map[string]interface{}{
				"percent_used": usage.PercentUsed,
				"free_bytes":   usage.Free,
			},
		})
	}
}

// ============================================================
// SystemInfo — системная информация
// ============================================================

// SystemInfo возвращает информацию о системе.
type SystemInfo struct {
	Uptime      time.Duration `json:"uptime"`
	CPUCount    int           `json:"cpu_count"`
	MemoryUsed  uint64        `json:"memory_used"`
	MemoryTotal uint64        `json:"memory_total"`
	MemoryPct   float64       `json:"memory_percent"`
}

// GetSystemInfo возвращает текущую информацию о системе.
func GetSystemInfo() SystemInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return SystemInfo{
		Uptime:      time.Since(startTime),
		CPUCount:    runtime.NumCPU(),
		MemoryUsed:  m.Alloc,
		MemoryTotal: m.TotalAlloc,
		MemoryPct:   float64(m.Alloc) / float64(m.TotalAlloc) * 100,
	}
}

var startTime = time.Now()

// PathExists проверяет существование пути.
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// PathIsDir проверяет является ли путь директорией.
func PathIsDir(path string) bool {
	info, err := exec.Command("stat", "-c", "%F", path).Output()
	return err == nil && string(info) == "directory"
}

// PathSize возвращает размер файла/директории в байтах.
func PathSize(path string) (int64, error) {
	output, err := exec.Command("du", "-sb", path).Output()
	if err != nil {
		return 0, err
	}

	// Парсить вывод: "size\tpath"
	var size int64
	fmt.Sscanf(string(output), "%d", &size)
	return size, nil
}

// FileAge возвращает возраст файла в секундах.
func FileAge(path string) (float64, error) {
	info, err := exec.Command("stat", "-c", "%Y", path).Output()
	if err != nil {
		return 0, err
	}

	var mtime int64
	fmt.Sscanf(string(info), "%d", &mtime)

	age := time.Since(time.Unix(mtime, 0)).Seconds()
	return age, nil
}

// ListFilesByAge возвращает файлы старше указанного возраста.
func ListFilesByAge(dir string, maxAge time.Duration) ([]string, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return nil, err
	}

	oldFiles := []string{}
	cutoff := time.Now().Add(-maxAge)

	for _, entry := range entries {
		info, err := os.Stat(entry)
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			oldFiles = append(oldFiles, entry)
		}
	}

	return oldFiles, nil
}
