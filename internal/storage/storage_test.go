package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNewStorage проверяет создание хранилища.
func TestNewStorage(t *testing.T) {
	st := NewStorage("/tmp/test-storage")

	if st.Root != "/tmp/test-storage" {
		t.Errorf("ожидался root '/tmp/test-storage', получено '%s'", st.Root)
	}
}

// TestStorageInit проверяет инициализацию хранилища.
func TestStorageInit(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	// Проверить создание директорий
	expectedDirs := []string{"recordings", "events", "snapshots", "tmp"}
	for _, dir := range expectedDirs {
		path := filepath.Join(tmpDir, dir)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("директория '%s' не создана: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("'%s' не является директорией", dir)
		}
	}
}

// TestPathRecording проверяет построение путей к записям.
func TestPathRecording(t *testing.T) {
	st := NewStorage("/srv/videoguard-data")

	path := st.PathRecording("camera-1")
	expected := "/srv/videoguard-data/recordings/camera-1"

	if path != expected {
		t.Errorf("ожидался '%s', получено '%s'", expected, path)
	}
}

// TestPathRecordingByDate проверяет построение путей по дате.
func TestPathRecordingByDate(t *testing.T) {
	st := NewStorage("/srv/videoguard-data")
	date := time.Date(2026, 9, 20, 18, 30, 0, 0, time.UTC)

	path := st.PathRecordingByDate("camera-1", date)
	expected := "/srv/videoguard-data/recordings/camera-1/2026/09/20"

	if path != expected {
		t.Errorf("ожидался '%s', получено '%s'", expected, path)
	}
}

// TestPathSegment проверяет построение путей к сегментам.
func TestPathSegment(t *testing.T) {
	st := NewStorage("/srv/videoguard-data")
	date := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	segmentTime := time.Date(2026, 9, 20, 18, 5, 0, 0, time.UTC)

	path := st.PathSegment("camera-1", date, segmentTime)
	expected := "/srv/videoguard-data/recordings/camera-1/2026/09/20/18-05-00.mp4"

	if path != expected {
		t.Errorf("ожидался '%s', получено '%s'", expected, path)
	}
}

// TestPathEvents проверяет построение путей к событиям.
func TestPathEvents(t *testing.T) {
	st := NewStorage("/srv/videoguard-data")

	path := st.PathEvents("camera-1")
	expected := "/srv/videoguard-data/events/camera-1"

	if path != expected {
		t.Errorf("ожидался '%s', получено '%s'", expected, path)
	}
}

// TestPathEvent проверяет построение путей к конкретному событию.
func TestPathEvent(t *testing.T) {
	st := NewStorage("/srv/videoguard-data")
	date := time.Date(2026, 9, 20, 18, 30, 0, 0, time.UTC)

	path := st.PathEvent("camera-1", "evt-123", date)
	expected := "/srv/videoguard-data/events/camera-1/2026/09/20/evt-123"

	if path != expected {
		t.Errorf("ожидался '%s', получено '%s'", expected, path)
	}
}

// TestPathSnapshot проверяет построение путей к снимкам.
func TestPathSnapshot(t *testing.T) {
	st := NewStorage("/srv/videoguard-data")
	date := time.Date(2026, 9, 20, 18, 30, 0, 0, time.UTC)

	path := st.PathSnapshot("camera-1", "evt-123", date)
	expected := "/srv/videoguard-data/events/camera-1/2026/09/20/evt-123/snapshot.jpg"

	if path != expected {
		t.Errorf("ожидался '%s', получено '%s'", expected, path)
	}
}

// TestPathEventVideo проверяет построение путей к видеоклипам событий.
func TestPathEventVideo(t *testing.T) {
	st := NewStorage("/srv/videoguard-data")
	date := time.Date(2026, 9, 20, 18, 30, 0, 0, time.UTC)

	path := st.PathEventVideo("camera-1", "evt-123", date)
	expected := "/srv/videoguard-data/events/camera-1/2026/09/20/evt-123/event.mp4"

	if path != expected {
		t.Errorf("ожидался '%s', получено '%s'", expected, path)
	}
}

// TestSafePath проверяет защиту от path traversal.
func TestSafePath(t *testing.T) {
	st := NewStorage("/srv/videoguard-data")

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "валидный путь",
			path:    "/srv/videoguard-data/recordings/camera-1",
			wantErr: false,
		},
		{
			name:    "путь внутри хранилища",
			path:    "/srv/videoguard-data/events/camera-1/2026/09/20",
			wantErr: false,
		},
		{
			name:    "path traversal попытка",
			path:    "/srv/videoguard-data/../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "путь за пределами хранилища",
			path:    "/etc/passwd",
			wantErr: true,
		},
		{
			name:    "относительный путь с traversal",
			path:    "recordings/../../etc/shadow",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Создать абсолютный путь для тестов
			var testPath string
			if tt.path[0] == '/' {
				testPath = tt.path
			} else {
				testPath = filepath.Join(st.Root, tt.path)
			}

			_, err := st.SafePath(testPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("SafePath('%s') ошибка = %v, wantErr %v", testPath, err, tt.wantErr)
			}
		})
	}
}

// TestEnsureDir проверяет создание директорий.
func TestEnsureDir(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	testDir := filepath.Join(tmpDir, "a", "b", "c")

	if err := st.EnsureDir(testDir); err != nil {
		t.Fatalf("не удалось создать директорию: %v", err)
	}

	info, err := os.Stat(testDir)
	if err != nil {
		t.Fatalf("директория не существует: %v", err)
	}

	if !info.IsDir() {
		t.Error("ожидалась директория")
	}
}

// TestListSegmentsByDate проверяет поиск сегментов.
func TestListSegmentsByDate(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	// Создать тестовую структуру
	cameraID := "camera-1"
	date := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	dateDir := st.PathRecordingByDate(cameraID, date)

	if err := os.MkdirAll(dateDir, 0755); err != nil {
		t.Fatalf("не удалось создать директорию: %v", err)
	}

	// Создать тестовые файлы
	files := []string{
		"18-00-00.mp4",
		"18-05-00.mp4",
		"18-10-00.mp4",
		"readme.txt", // Не должен быть включён
	}

	for _, f := range files {
		filePath := filepath.Join(dateDir, f)
		if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
			t.Fatalf("не удалось создать файл: %v", err)
		}
	}

	segments, err := st.ListSegmentsByDate(cameraID, date)
	if err != nil {
		t.Fatalf("не удалось получить сегменты: %v", err)
	}

	if len(segments) != 3 {
		t.Errorf("ожидалось 3 сегмента, получено %d", len(segments))
	}

	// Проверить, что txt файл не включён
	for _, seg := range segments {
		if filepath.Ext(seg) != ".mp4" {
			t.Errorf("неожиданное расширение: %s", seg)
		}
	}
}

// TestListEventsByDate проверяет поиск событий.
func TestListEventsByDate(t *testing.T) {
	tmpDir := t.TempDir()
	st := NewStorage(tmpDir)

	cameraID := "camera-1"
	date := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	dateDir := st.PathEventsByDate(cameraID, date)

	if err := os.MkdirAll(dateDir, 0755); err != nil {
		t.Fatalf("не удалось создать директорию: %v", err)
	}

	// Создать тестовые поддиректории событий
	eventDirs := []string{"evt-1", "evt-2", "evt-3"}
	for _, evt := range eventDirs {
		if err := os.MkdirAll(filepath.Join(dateDir, evt), 0755); err != nil {
			t.Fatalf("не удалось создать директорию события: %v", err)
		}
	}

	// Создать файл (не должен быть включён)
	if err := os.WriteFile(filepath.Join(dateDir, "note.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("не удалось создать файл: %v", err)
	}

	events, err := st.ListEventsByDate(cameraID, date)
	if err != nil {
		t.Fatalf("не удалось получить события: %v", err)
	}

	if len(events) != 3 {
		t.Errorf("ожидалось 3 события, получено %d", len(events))
	}
}

// TestDiskUsage проверяет получение информации о диске.
func TestDiskUsage(t *testing.T) {
	// Использовать временную директорию
	tmpDir := t.TempDir()

	usage, err := DiskUsage(tmpDir)
	if err != nil {
		t.Fatalf("не удалось получить информацию о диске: %v", err)
	}

	if usage.Total == 0 {
		t.Error("ожидалось total > 0")
	}

	if usage.Used > usage.Total {
		t.Error("ожидалось used <= total")
	}

	if usage.PercentUsed < 0 || usage.PercentUsed > 100 {
		t.Errorf("ожидался percent_used от 0 до 100, получено %f", usage.PercentUsed)
	}
}

// TestFormatBytes проверяет форматирование размера в байтах.
func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes  uint64
		expect string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{2147483648, "2.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expect, func(t *testing.T) {
			got := FormatBytes(tt.bytes)
			if got != tt.expect {
				t.Errorf("FormatBytes(%d) = '%s', want '%s'", tt.bytes, got, tt.expect)
			}
		})
	}
}

// TestStoragePathsUniqueness проверяет уникальность путей.
func TestStoragePathsUniqueness(t *testing.T) {
	st := NewStorage("/srv/videoguard-data")

	// Проверить, что разные типы файлов имеют разные пути
	recordings := st.PathRecording("camera-1")
	events := st.PathEvents("camera-1")
	snapshots := st.PathSnapshotShared("test")
	tmp := st.PathTmp("file")

	paths := []string{recordings, events, snapshots, tmp}

	for i, p1 := range paths {
		for j, p2 := range paths {
			if i < j && p1 == p2 {
				t.Errorf("пути совпадают: [%d]='%s' и [%d]='%s'", i, p1, j, p2)
			}
		}
	}
}
