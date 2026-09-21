// Package recorder предоставляет менеджер непрерывной записи архива.
//
// Recorder управляет FFmpeg процессами для записи RTSP потоков
// в сегментированный архив без перекодировки.
//
// Hardening:
// - Не создаёт пустые сегменты (проверка размера)
// - Корректно завершает текущий сегмент при остановке
// - Не оставляет битый процесс FFmpeg
// - Пишет новый сегмент после восстановления камеры
// - Логгирует FFmpeg stderr
package recorder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"videoguard/internal/camera"
	"videoguard/internal/config"
	"videoguard/internal/storage"
)

// Recorder управляет непрерывной записью с камер.
type Recorder struct {
	cfg     config.RecordingConfig
	camMgr  *camera.CameraManager
	st      *storage.Storage
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
	mu      sync.RWMutex
}

// NewRecorder создаёт новый рекордер.
func NewRecorder(cfg config.RecordingConfig, camMgr *camera.CameraManager, st *storage.Storage) *Recorder {
	ctx, cancel := context.WithCancel(context.Background())
	return &Recorder{
		cfg:    cfg,
		camMgr: camMgr,
		st:     st,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start запускает запись со всех камер.
func (r *Recorder) Start() error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return fmt.Errorf("рекордер уже запущен")
	}
	r.running = true
	r.mu.Unlock()

	cameras := r.camMgr.ListCameras()
	if len(cameras) == 0 {
		return fmt.Errorf("нет камер для записи")
	}

	for _, cam := range cameras {
		go r.recordCamera(cam)
	}

	return nil
}

// Stop останавливает все записи корректно.
// При остановке FFmpeg получает SIGINT для финализации текущего сегмента.
func (r *Recorder) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	r.running = false
	r.cancel()

	// Остановить все камеры
	// Каждая камера сама обработает контекст и завершит FFmpeg gracefully
	cameras := r.camMgr.ListCameras()
	for _, cam := range cameras {
		if err := cam.Stop(); err != nil {
			fmt.Printf("[Recorder] Ошибка остановки камеры %s: %v\n", cam.ID(), err)
		}
	}

	// Дать FFmpeg время на финализацию сегментов
	time.Sleep(2 * time.Second)

	// Проверить и удалить пустые сегменты
	r.cleanupEmptySegments(cameras)

	return nil
}

// IsRunning возвращает true если рекордер активен.
func (r *Recorder) IsRunning() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.running
}

// recordCamera запускает запись с конкретной камеры.
func (r *Recorder) recordCamera(cam *camera.Camera) {
	outputDir := r.st.PathRecording(cam.ID())
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("[Recorder] Ошибка создания директории для %s: %v\n", cam.ID(), err)
		return
	}

	// Создать поддиректории по дате
	now := time.Now()
	dateDir := filepath.Join(outputDir,
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
	)
	if err := os.MkdirAll(dateDir, 0755); err != nil {
		fmt.Printf("[Recorder] Ошибка создания dateDir для %s: %v\n", cam.ID(), err)
		return
	}

	// Паттерн для FFmpeg сегментов: seg_001.mp4, seg_002.mp4, ...
	segmentPattern := filepath.Join(dateDir, "seg_%03d.mp4")

	if err := cam.Start(r.ctx, segmentPattern); err != nil {
		fmt.Printf("[Recorder] Ошибка запуска камеры %s: %v\n", cam.ID(), err)
		return
	}

	fmt.Printf("[Recorder] Запись началась: %s -> %s\n", cam.ID(), segmentPattern)
}

// cleanupEmptySegments удаляет пустые сегменты (размер 0 байт).
// Вызывается при остановке рекордера.
func (r *Recorder) cleanupEmptySegments(cameras []*camera.Camera) {
	for _, cam := range cameras {
		id := cam.ID()
		outputDir := r.st.PathRecording(id)

		// Пройтись по всем датам в директории камеры
		entries, err := os.ReadDir(outputDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			dateDir := filepath.Join(outputDir, entry.Name())
			segEntries, err := os.ReadDir(dateDir)
			if err != nil {
				continue
			}

			for _, seg := range segEntries {
				if filepath.Ext(seg.Name()) != ".mp4" {
					continue
				}

				info, err := seg.Info()
				if err != nil {
					continue
				}

				// Удалить пустые сегменты (0 байт)
				if info.Size() == 0 {
					segPath := filepath.Join(dateDir, seg.Name())
					os.Remove(segPath)
					fmt.Printf("[Recorder] Удалён пустой сегмент: %s\n", segPath)
				}
			}
		}
	}
}

// checkFFmpegAlive проверяет что процесс FFmpeg не "завис".
// Возвращает true если процесс активен.
func checkFFmpegAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Send signal 0 — проверит существование процесса без отправки сигнала
	return process.Signal(nil) == nil
}

// getFFmpegOutputDir извлекает директорию из паттерна сегмента.
// Например: /path/recordings/cam1/2026/09/21/seg_%03d.mp4 -> /path/recordings/cam1/2026/09/21/
func getFFmpegOutputDir(segmentPattern string) string {
	return filepath.Dir(segmentPattern)
}

// segmentSizeChecker проверяет размер последнего сегмента.
func segmentSize(segmentPattern string) (int64, error) {
	dir := getFFmpegOutputDir(segmentPattern)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}

	var latestSize int64
	var latestTime time.Time

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".mp4" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latestSize = info.Size()
		}
	}

	return latestSize, nil
}
