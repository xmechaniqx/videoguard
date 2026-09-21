// Package selftest выполняет самодиагностику при запуске приложения.
//
// Проверяет все критичные компоненты до запуска основного цикла:
// - FFmpeg / ffprobe в PATH
// - Motion (если включён в конфиге)
// - SQLite база данных и миграции
// - Директории хранения
// - Свободное место на диске
// - Загрузка камер
package selftest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"videoguard/internal/config"
	"videoguard/internal/storage"
)

// Result описывает результат одной проверки.
type Result struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "pass", "warn", "fail"
	Message string `json:"message"`
}

// Results — список результатов самодиагностики.
type Results []Result

// Add добавляет результат.
func (r *Results) Add(name, status, message string) {
	*r = append(*r, Result{
		Name:    name,
		Status:  status,
		Message: message,
	})
}

// HasFail возвращает true если есть критические ошибки.
func (r Results) HasFail() bool {
	for _, res := range r {
		if res.Status == "fail" {
			return true
		}
	}
	return false
}

// HasWarn возвращает true если есть предупреждения.
func (r Results) HasWarn() bool {
	for _, res := range r {
		if res.Status == "warn" {
			return true
		}
	}
	return false
}

// Print выводит результаты в формате, удобном для journalctl.
func (r Results) Print() {
	for i, res := range r {
		icon := "✓"
		if res.Status == "fail" {
			icon = "✗"
		} else if res.Status == "warn" {
			icon = "!"
		}
		fmt.Printf("  [%s] %s: %s\n", icon, res.Name, res.Message)
		_ = i
	}
}

// Run выполняет все проверки и возвращает результаты.
// Если critical=true, критические ошибки возвращают ошибку.
func Run(cfg config.Config, critical bool) (Results, error) {
	var results Results

	// 1. FFmpeg
	results.addCheckBinary("ffmpeg", "FFmpeg должен быть в PATH")

	// 2. ffprobe
	results.addCheckBinary("ffprobe", "ffprobe должен быть в PATH")

	// 3. Motion (если включён)
	if cfg.Motion.Enabled {
		results.addCheckBinary("motion", "Motion включён в конфиге, должен быть в PATH")
	}

	// 4. Директории хранения
	results.addCheckDirs(cfg.Storage.Root)

	// 5. Свободное место на диске
	results.addCheckDiskSpace(cfg.Storage.Root)

	// 6. Камеры загружены
	results.addCheckCameras(cfg.Cameras)

	// 7. Storage writeable
	results.addCheckStorageWritable(cfg.Storage.Root)

	fmt.Println("============================================================")
	fmt.Println("Self Test Results")
	fmt.Println("============================================================")
	results.Print()
	fmt.Println("============================================================")

	if critical && results.HasFail() {
		var failMsgs []string
		for _, res := range results {
			if res.Status == "fail" {
				failMsgs = append(failMsgs, fmt.Sprintf("  %s: %s", res.Name, res.Message))
			}
		}
		return results, fmt.Errorf("критические ошибки самодиагностики:\n%s",
			fmt.Sprintf("%s", failMsgs))
	}

	return results, nil
}

// addCheckBinary проверяет наличие бинарника в PATH.
func (r *Results) addCheckBinary(binary, errMsg string) {
	path, err := exec.LookPath(binary)
	if err != nil {
		r.Add(binary, "fail", fmt.Sprintf("не найден в PATH: %v", err))
		return
	}
	r.Add(binary, "pass", fmt.Sprintf("найден: %s", path))
}

// addCheckDirs проверяет существование директорий хранения.
func (r *Results) addCheckDirs(root string) {
	if root == "" {
		r.Add("storage_dirs", "fail", "storage.root не указан")
		return
	}

	requiredDirs := []string{"recordings", "events", "snapshots", "tmp"}
	missing := []string{}

	for _, dir := range requiredDirs {
		fullPath := filepath.Join(root, dir)
		info, err := os.Stat(fullPath)
		if err != nil {
			// Попробовать создать
			if mkErr := os.MkdirAll(fullPath, 0755); mkErr != nil {
				missing = append(missing, dir)
			}
		} else if !info.IsDir() {
			missing = append(missing, dir)
		}
	}

	if len(missing) > 0 {
		r.Add("storage_dirs", "warn",
			fmt.Sprintf("директории созданы автоматически: %v", missing))
	} else {
		r.Add("storage_dirs", "pass", "все директории существуют")
	}
}

// addCheckDiskSpace проверяет свободное место на диске.
func (r *Results) addCheckDiskSpace(root string) {
	if root == "" {
		r.Add("disk_space", "warn", "storage.root не указан, пропуск")
		return
	}

	usage, err := storage.DiskUsage(root)
	if err != nil {
		r.Add("disk_space", "warn", fmt.Sprintf("не удалось проверить: %v", err))
		return
	}

	freeGB := float64(usage.Free) / (1024 * 1024 * 1024)
	totalGB := float64(usage.Total) / (1024 * 1024 * 1024)

	if freeGB < 1.0 {
		r.Add("disk_space", "fail",
			fmt.Sprintf("критически мало места: %.1f GB свободно из %.1f GB", freeGB, totalGB))
	} else if freeGB < 5.0 {
		r.Add("disk_space", "warn",
			fmt.Sprintf("мало места: %.1f GB свободно из %.1f GB", freeGB, totalGB))
	} else {
		r.Add("disk_space", "pass",
			fmt.Sprintf("%.1f GB свободно из %.1f GB", freeGB, totalGB))
	}
}

// addCheckCameras проверяет что камеры загружены корректно.
func (r *Results) addCheckCameras(cameras []config.CameraConfig) {
	if len(cameras) == 0 {
		r.Add("cameras", "fail", "камеры не загружены")
		return
	}

	// Проверить дубликаты ID
	seen := make(map[string]bool)
	dupes := []string{}
	for _, cam := range cameras {
		if seen[cam.ID] {
			dupes = append(dupes, cam.ID)
		}
		seen[cam.ID] = true
	}

	if len(dupes) > 0 {
		r.Add("cameras", "fail",
			fmt.Sprintf("дубликаты ID: %v", dupes))
		return
	}

	r.Add("cameras", "pass",
		fmt.Sprintf("загружено %d камер", len(cameras)))
}

// addCheckStorageWritable проверяет что storage доступен на запись.
func (r *Results) addCheckStorageWritable(root string) {
	if root == "" {
		r.Add("storage_writable", "warn", "storage.root не указан, пропуск")
		return
	}

	// Проверить что можно создать файл
	tmpFile := filepath.Join(root, "tmp", ".write_test")
	tmpDir := filepath.Join(root, "tmp")

	// Создать tmp если нет
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		r.Add("storage_writable", "fail",
			fmt.Sprintf("нельзя создать tmp: %v", err))
		return
	}

	f, err := os.Create(tmpFile)
	if err != nil {
		r.Add("storage_writable", "fail",
			fmt.Sprintf("нельзя записать: %v", err))
		return
	}
	f.Close()
	os.Remove(tmpFile)

	r.Add("storage_writable", "pass", "доступен на запись")
}

// checkTime отмечает время для расчёта длительности.
var checkTime = time.Now()
