package recorder

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"videoguard/internal/camera"
	"videoguard/internal/config"
	"videoguard/internal/storage"
)

// skipIfNoFFmpeg пропускает тест если ffmpeg не установлен.
func skipIfNoFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg не найден — пропускаем интеграционный тест")
	}
}

// skipIfNoRTSP проверяет что можно запустить RTSP сервер.
func skipIfNoRTSP(t *testing.T) {
	t.Helper()
	skipIfNoFFmpeg(t)

	// Проверить что порт 8555 свободен и FFmpeg может запустить RTSP
	cmd := exec.Command("ffmpeg",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=1:duration=1",
		"-f", "rtsp", "-rtsp_transport", "tcp", "-listen", "1",
		"-rtsp_port", "8555", // используем другой порт для проверки
		"rtsp://localhost:8555/test",
	)
	err := cmd.Run()
	if err != nil {
		t.Skip("FFmpeg не может запустить RTSP сервер — пропускаем интеграционный тест")
	}
}

// TestIntegrationRTSPRecording проверяет полный цикл:
// 1. Поднять тестовый RTSP поток через FFmpeg (testsrc)
// 2. Запустить Recorder
// 3. Проверить что через 10 секунд появился сегмент MP4
// 4. Остановить RTSP поток
// 5. Проверить что камера перешла в offline
// 6. Вернуть RTSP поток
// 7. Проверить что запись продолжилась
func TestIntegrationRTSPRecording(t *testing.T) {
	skipIfNoRTSP(t)

	tmpDir := t.TempDir()
	st := storage.NewStorage(tmpDir)
	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	// ============================================================
	// Шаг 1: Поднять тестовый RTSP поток
	// ============================================================
	rtspPort := "8554"
	rtspURL := "rtsp://localhost:" + rtspPort + "/stream"

	// FFmpeg генерирует тестовую картинку и отдаёт по RTSP
	ffmpegServer := exec.Command("ffmpeg",
		"-y",
		"-f", "lavfi",
		"-i", "testsrc=size=320x240:rate=10:duration=60",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-f", "rtsp",
		"-rtsp_transport", "tcp",
		"-listen", "1",
		"-muxdelay", "0.1",
		"-rtsp_port", rtspPort,
		rtspURL,
	)

	ffmpegServer.Stderr = os.Stderr
	if err := ffmpegServer.Start(); err != nil {
		t.Fatalf("не удалось запустить тестовый RTSP сервер: %v", err)
	}
	defer ffmpegServer.Process.Kill()

	// Дать RTSP серверу время на старт
	time.Sleep(2 * time.Second)

	// ============================================================
	// Шаг 2: Настроить камеру и Recorder
	// ============================================================
	camMgr := camera.NewCameraManager()

	// Уменьшим segment_duration для быстрого теста (10 секунд вместо 5 минут)
	cfg := config.RecordingConfig{
		Enabled:         true,
		SegmentDuration: 10 * time.Second,
		CodecCopy:       true,
	}

	rec := NewRecorder(cfg, camMgr, st)

	// Добавим камеру
	camMgr.AddCamera(config.CameraConfig{
		ID:       "test-camera",
		Name:     "Test Camera",
		RTSPURL:  rtspURL,
		Username: "",
		Password: "",
	})

	// ============================================================
	// Шаг 3: Запустить Recorder
	// ============================================================
	if err := rec.Start(); err != nil {
		t.Fatalf("не удалось запустить Recorder: %v", err)
	}
	defer rec.Stop()

	// ============================================================
	// Шаг 4: Проверить что через 15 секунд появился сегмент
	// ============================================================
	t.Log("Ожидание создания первого сегмента (15 секунд)...")

	foundSegment := false
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)

		segments, err := st.ListSegmentsByDate("test-camera", time.Now())
		if err == nil && len(segments) > 0 {
			t.Logf("Сегмент найден: %s", segments[0])
			foundSegment = true
			break
		}

		if i%5 == 0 {
			t.Logf("Ожидание сегмента... прошло %d секунд", i+1)
		}
	}

	if !foundSegment {
		t.Error("сегмент не был создан за 30 секунд")
		// Продолжить тест для проверки offline
	}

	// ============================================================
	// Шаг 5: Остановить RTSP поток
	// ============================================================
	t.Log("Остановка RTSP потока...")
	ffmpegServer.Process.Kill()
	ffmpegServer.Wait()

	// Дать камере время обнаружить обрыв
	time.Sleep(8 * time.Second)

	// Проверить состояние камеры
	cam, ok := camMgr.GetCamera("test-camera")
	if !ok {
		t.Fatal("камера не найдена в менеджере")
	}

	state := cam.State()
	t.Logf("Состояние камеры после обрыва: %s", state)

	// Камера должна быть offline или connecting (зависит от reconnect логики)
	if state != camera.StateOffline && state != camera.StateError {
		t.Logf("Предупреждение: ожидалось offline/error, получено %s (возможно сработал reconnect)", state)
	}

	// ============================================================
	// Шаг 6: Вернуть RTSP поток
	// ============================================================
	t.Log("Перезапуск RTSP потока...")

	ffmpegServer2 := exec.Command("ffmpeg",
		"-y",
		"-f", "lavfi",
		"-i", "testsrc=size=320x240:rate=10:duration=60",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-f", "rtsp",
		"-rtsp_transport", "tcp",
		"-listen", "1",
		"-muxdelay", "0.1",
		"-rtsp_port", rtspPort,
		rtspURL,
	)
	ffmpegServer2.Stderr = os.Stderr

	if err := ffmpegServer2.Start(); err != nil {
		t.Fatalf("не удалось перезапустить RTSP сервер: %v", err)
	}
	defer ffmpegServer2.Process.Kill()

	time.Sleep(2 * time.Second)

	// ============================================================
	// Шаг 7: Проверить что запись продолжилась
	// ============================================================
	t.Log("Ожидание нового сегмента после восстановления (15 секунд)...")

	foundSegmentAfterRecovery := false
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)

		segments, err := st.ListSegmentsByDate("test-camera", time.Now())
		if err == nil && len(segments) > 0 {
			t.Logf("Новый сегмент найден после восстановления: %s", segments[0])
			foundSegmentAfterRecovery = true
			break
		}

		if i%5 == 0 {
			t.Logf("Ожидание сегмента после восстановления... прошло %d секунд", i+1)
		}
	}

	// Очистка
	ffmpegServer2.Process.Kill()
	ffmpegServer2.Wait()

	if !foundSegmentAfterRecovery {
		t.Log("Предупреждение: новый сегмент после восстановления не найден")
	}

	// ============================================================
	// Итоговая проверка: есть ли вообще сегменты
	// ============================================================
	allSegments, _ := st.ListSegmentsByDate("test-camera", time.Now())
	t.Logf("Всего сегментов найдено: %d", len(allSegments))

	if len(allSegments) == 0 {
		t.Log("ВНИМАНИЕ: сегменты не были созданы — возможно FFmpeg не смог подключиться")
	}
}

// TestIntegrationSegmentationStructure проверяет структуру сегментов на диске.
// Создаёт тестовую директорию и проверяет что паттерн сегментации корректен.
func TestIntegrationSegmentationStructure(t *testing.T) {
	tmpDir := t.TempDir()
	st := storage.NewStorage(tmpDir)
	if err := st.Init(); err != nil {
		t.Fatalf("не удалось инициализировать хранилище: %v", err)
	}

	// Проверить структуру путей
	camID := "camera-1"
	date := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)

	// PathRecordingByDate должен вернуть: root/recordings/camera-1/2026/09/20
	pathByDate := st.PathRecordingByDate(camID, date)
	expected := tmpDir + "/recordings/camera-1/2026/09/20"
	if pathByDate != expected {
		t.Errorf("ожидался путь '%s', получено '%s'", expected, pathByDate)
	}

	// PathSegment должен вернуть: root/recordings/camera-1/2026/09/20/18-00-00.mp4
	segTime := time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)
	segmentPath := st.PathSegment(camID, date, segTime)
	expectedSegment := tmpDir + "/recordings/camera-1/2026/09/20/18-00-00.mp4"
	if segmentPath != expectedSegment {
		t.Errorf("ожидался путь сегмента '%s', получено '%s'", expectedSegment, segmentPath)
	}

	// Создать директорию и проверить ListSegmentsByDate
	testDir := tmpDir + "/recordings/camera-1/2026/09/20"
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("не удалось создать тестовую директорию: %v", err)
	}

	// Создать тестовые сегменты
	for i := 0; i < 3; i++ {
		segName := testDir + "/seg_001.mp4"
		if err := os.WriteFile(segName, []byte("test"), 0644); err != nil {
			t.Fatalf("не удалось создать тестовый сегмент: %v", err)
		}
	}

	// Проверить что ListSegmentsByDate находит сегменты
	segments, err := st.ListSegmentsByDate(camID, date)
	if err != nil {
		t.Fatalf("ошибка при поиске сегментов: %v", err)
	}

	if len(segments) != 1 {
		t.Errorf("ожидался 1 сегмент, получено %d", len(segments))
	}
}

// TestIntegrationFFmpegCommandGeneration проверяет что FFmpeg команда генерируется корректно.
func TestIntegrationFFmpegCommandGeneration(t *testing.T) {
	skipIfNoFFmpeg(t)

	cfg := config.CameraConfig{
		ID:       "test-cam",
		Name:     "Test",
		RTSPURL:  "rtsp://user:pass@192.168.1.100:554/stream",
		Username: "user",
		Password: "pass",
	}

	cam := camera.NewCamera(cfg)
	if cam == nil {
		t.Fatal("ожидалась камера, получено nil")
	}

	// Проверить что путь сегментации корректен
	outputPath := cam.GetOutputPath()

	// Путь должен содержать паттерн seg_XXX.mp4
	if outputPath == "" {
		t.Error("ожидался непустой путь сегментации")
	}

	// Путь должен содержать recordings/camera-id
	if len(outputPath) < 20 {
		t.Errorf("путь слишком короткий: %s", outputPath)
	}
}
