// Package storage предоставляет слой работы с файловой системой.
//
// Отвечает за:
// - Создание директорий
// - Построение путей
// - Сохранение файлов
// - Поиск файлов
// - Удаление файлов
// - Проверку свободного места
// - Защиту от path traversal
package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var errPathOutsideStorage = fmt.Errorf("путь за пределами хранилища")

// Storage управляет файловой структурой хранилища.
type Storage struct {
	Root string
}

// NewStorage создаёт новый менеджер хранилища.
func NewStorage(root string) *Storage {
	return &Storage{Root: root}
}

// Init инициализирует структуру директорий хранилища.
func (s *Storage) Init() error {
	dirs := []string{
		"recordings",
		"events",
		"snapshots",
		"tmp",
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(s.Root, dir), 0755); err != nil {
			return err
		}
	}

	return nil
}

// PathRecording возвращает путь к записи для камеры.
func (s *Storage) PathRecording(cameraID string) string {
	return filepath.Join(s.Root, "recordings", cameraID)
}

// PathRecordingByDate возвращает путь к записи по дате.
func (s *Storage) PathRecordingByDate(cameraID string, date time.Time) string {
	return filepath.Join(s.Root, "recordings", cameraID,
		date.Format("2006"),
		date.Format("01"),
		date.Format("02"))
}

// PathSegment возвращает путь к сегменту записи.
func (s *Storage) PathSegment(cameraID string, date time.Time, segmentTime time.Time) string {
	return filepath.Join(s.Root, "recordings", cameraID,
		date.Format("2006"),
		date.Format("01"),
		date.Format("02"),
		segmentTime.Format("15-04-05")+".mp4")
}

// PathEvents возвращает путь к директории событий.
func (s *Storage) PathEvents(cameraID string) string {
	return filepath.Join(s.Root, "events", cameraID)
}

// PathEventsByDate возвращает путь к событиям по дате.
func (s *Storage) PathEventsByDate(cameraID string, date time.Time) string {
	return filepath.Join(s.Root, "events", cameraID,
		date.Format("2006"),
		date.Format("01"),
		date.Format("02"))
}

// PathEvent возвращает путь к конкретному событию.
func (s *Storage) PathEvent(cameraID, eventID string, date time.Time) string {
	return filepath.Join(s.Root, "events", cameraID,
		date.Format("2006"),
		date.Format("01"),
		date.Format("02"),
		eventID)
}

// PathSnapshot возвращает путь к снимку события.
func (s *Storage) PathSnapshot(cameraID, eventID string, date time.Time) string {
	return filepath.Join(s.PathEvent(cameraID, eventID, date), "snapshot.jpg")
}

// PathEventVideo возвращает путь к видеоклипу события.
func (s *Storage) PathEventVideo(cameraID, eventID string, date time.Time) string {
	return filepath.Join(s.PathEvent(cameraID, eventID, date), "event.mp4")
}

// PathSnapshotShared возвращает путь к общему снимку (без привязки к событию).
func (s *Storage) PathSnapshotShared(name string) string {
	return filepath.Join(s.Root, "snapshots", name+".jpg")
}

// PathTmp возвращает путь к временным файлам.
func (s *Storage) PathTmp(name string) string {
	return filepath.Join(s.Root, "tmp", name)
}

// EnsureDir создаёт директорию и все родительские, если нужно.
func (s *Storage) EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// SafePath проверяет, что путь находится внутри корневого каталога хранилища.
// Это защищает от path traversal атак.
func (s *Storage) SafePath(requestedPath string) (string, error) {
	// Получить абсолютный путь
	abs, err := filepath.Abs(requestedPath)
	if err != nil {
		return "", err
	}

	// Получить абсолютный путь корня
	absRoot, err := filepath.Abs(s.Root)
	if err != nil {
		return "", err
	}

	// Проверить, что путь находится внутри корня
	if abs != absRoot && !hasPrefix(abs, absRoot) {
		return "", errPathOutsideStorage
	}

	return abs, nil
}

// hasPrefix проверяет, что путь начинается с префикса с разделителем.
func hasPrefix(path, prefix string) bool {
	// Убедиться, что это действительно поддиректория
	return len(path) > len(prefix) && path[len(prefix)] == filepath.Separator &&
		path[:len(prefix)] == prefix
}

// ListSegmentsByDate возвращает список сегментов записи за определённую дату.
func (s *Storage) ListSegmentsByDate(cameraID string, date time.Time) ([]string, error) {
	dir := s.PathRecordingByDate(cameraID, date)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	segments := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".mp4" {
			segments = append(segments, entry.Name())
		}
	}

	return segments, nil
}

// ListEventsByDate возвращает список событий за определённую дату.
func (s *Storage) ListEventsByDate(cameraID string, date time.Time) ([]string, error) {
	dir := s.PathEventsByDate(cameraID, date)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	eventIDs := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			eventIDs = append(eventIDs, entry.Name())
		}
	}

	return eventIDs, nil
}

// DiskUsage возвращает использование диска для заданного пути.
func DiskUsage(path string) (*DiskUsageInfo, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		return nil, err
	}

	// Доступные блоки * размер блока = доступное пространство
	available := stat.Bavail * uint64(stat.Bsize)
	// Все блоки - свободные = использованное пространство
	free := stat.Bfree * uint64(stat.Bsize)
	total := stat.Blocks * uint64(stat.Bsize)
	used := total - free

	percentUsed := float64(used) / float64(total) * 100

	return &DiskUsageInfo{
		Total:       total,
		Used:        used,
		Free:        free,
		Available:   available,
		PercentUsed: percentUsed,
	}, nil
}

// DiskUsageInfo содержит информацию об использовании диска.
type DiskUsageInfo struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	Available   uint64  `json:"available"`
	PercentUsed float64 `json:"percent_used"`
}

// FormatBytes возвращает человеко-читаемое представление размера в байтах.
func FormatBytes(bytes uint64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	if bytes < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
}
