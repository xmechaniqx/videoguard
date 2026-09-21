// Package storage предоставляет worker для автоматической очистки старых файлов.
//
// Retention worker запускается periodically и удаляет:
// - Записи старше retention_days
// - События старше retention_days
// - Снимки старше retention_days
//
// Также реализует emergency cleanup при нехватке места.
package storage

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RetentionConfig содержит настройки очистки.
type RetentionConfig struct {
	Days                       int
	EmergencyThresholdPercent  int
	AggressiveThresholdPercent int
	CheckInterval              time.Duration
}

// DefaultRetentionConfig возвращает конфигурацию очистки по умолчанию.
func DefaultRetentionConfig() RetentionConfig {
	return RetentionConfig{
		Days:                       7,
		EmergencyThresholdPercent:  10,
		AggressiveThresholdPercent: 5,
		CheckInterval:              1 * time.Hour,
	}
}

// RetentionWorker управляет периодической очисткой старых файлов.
type RetentionWorker struct {
	storage *Storage
	config  RetentionConfig
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
	logger  *logger
}

// NewRetentionWorker создаёт новый worker очистки.
func NewRetentionWorker(storage *Storage, config RetentionConfig) *RetentionWorker {
	return &RetentionWorker{
		storage: storage,
		config:  config,
		logger:  newLogger(),
	}
}

// Start запускает worker очистки.
func (rw *RetentionWorker) Start(ctx context.Context) {
	rw.ctx, rw.cancel = context.WithCancel(ctx)
	rw.running = true

	// Сразу выполнить первую очистку
	rw.cleanup()

	// Запустить периодическую очистку
	go rw.run()
}

// Stop останавливает worker очистки.
func (rw *RetentionWorker) Stop() {
	if rw.cancel != nil {
		rw.cancel()
	}
	rw.running = false
}

// run выполняет периодическую очистку.
func (rw *RetentionWorker) run() {
	ticker := time.NewTicker(rw.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rw.ctx.Done():
			return
		case <-ticker.C:
			rw.cleanup()
		}
	}
}

// cleanup выполняет очистку старых файлов и проверку места.
func (rw *RetentionWorker) cleanup() {
	// Проверить использование диска
	usage, err := DiskUsage(rw.storage.Root)
	if err != nil {
		rw.logger.Error("не удалось проверить диск", "error", err)
		return
	}

	// Проверить emergency threshold
	if usage.PercentUsed > float64(rw.config.EmergencyThresholdPercent) {
		rw.logger.Warn("нехватка места, запускается emergency cleanup",
			"percent_used", usage.PercentUsed,
			"threshold", rw.config.EmergencyThresholdPercent)
		rw.emergencyCleanup()
		return
	}

	// Проверить aggressive threshold
	if usage.PercentUsed > float64(rw.config.AggressiveThresholdPercent) {
		rw.logger.Warn("низкий порог свободного места",
			"percent_used", usage.PercentUsed,
			"threshold", rw.config.AggressiveThresholdPercent)
	}

	// Очистить старые файлы
	cutoff := time.Now().AddDate(0, 0, -rw.config.Days)
	removed := rw.removeOlderThan(cutoff)

	if removed > 0 {
		rw.logger.Info("очистка завершена",
			"удалено_файлов", removed,
			"дата_обрезки", cutoff.Format("2006-01-02"))
	}
}

// emergencyCleanup выполняет агрессивную очистку при нехватке места.
func (rw *RetentionWorker) emergencyCleanup() {
	// Удалить всё, что старше 3 дней
	cutoff := time.Now().AddDate(0, 0, -3)
	removed := rw.removeOlderThan(cutoff)

	rw.logger.Info("emergency cleanup завершён",
		"удалено_файлов", removed,
		"дата_обрезки", cutoff.Format("2006-01-02"))
}

// removeOlderThan удаляет файлы старше заданной даты.
func (rw *RetentionWorker) removeOlderThan(cutoff time.Time) int {
	removed := 0

	// Очистить записи
	removed += rw.cleanDirectory("recordings", cutoff)

	// Очистить события
	removed += rw.cleanDirectory("events", cutoff)

	// Очистить снимки
	removed += rw.cleanDirectory("snapshots", cutoff)

	return removed
}

// cleanDirectory удаляет файлы старше cutoff в заданной директории.
func (rw *RetentionWorker) cleanDirectory(dirName string, cutoff time.Time) int {
	dir := filepath.Join(rw.storage.Root, dirName)
	removed := 0

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		rw.logger.Error("не удалось прочитать директорию", "dir", dir, "error", err)
		return 0
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			// Рекурсивно очистить поддиректории
			removed += rw.cleanSubdir(fullPath, cutoff)
		} else {
			// Удалить старые файлы в корне директории
			fileInfo, err := entry.Info()
			if err == nil && fileInfo.ModTime().Before(cutoff) {
				if err := os.Remove(fullPath); err == nil {
					removed++
				}
			}
		}
	}

	return removed
}

// cleanSubdir рекурсивно очищает поддиректории.
func (rw *RetentionWorker) cleanSubdir(path string, cutoff time.Time) int {
	removed := 0

	info, err := os.Stat(path)
	if err != nil {
		return 0
	}

	// Если директория старше cutoff — удалить
	if info.ModTime().Before(cutoff) {
		if err := os.RemoveAll(path); err != nil {
			rw.logger.Error("не удалось удалить директорию", "path", path, "error", err)
		} else {
			removed++
			rw.logger.Info("удалена старая директория", "path", path)
		}
		return removed
	}

	// Иначе проверить содержимое
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			removed += rw.cleanSubdir(fullPath, cutoff)
		} else {
			fileInfo, err := entry.Info()
			if err == nil && fileInfo.ModTime().Before(cutoff) {
				if err := os.Remove(fullPath); err == nil {
					removed++
				}
			}
		}
	}

	return removed
}
