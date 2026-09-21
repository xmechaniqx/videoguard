// Package camera предоставляет управление камерами и интеграцию с FFmpeg.
//
// CameraManager управляет жизненным циклом камер:
// - Запуск FFmpeg для RTSP потока
// - Проверка состояния камеры
// - Автоматический реконнект
// - Статусы: online, offline, connecting, error
package camera

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"videoguard/internal/config"
)

// State определяет состояние камеры.
type State int

const (
	StateUnknown    State = iota // Неизвестно
	StateOffline                 // Не подключена
	StateConnecting              // Подключение
	StateOnline                  // Подключена и записывает
	StateError                   // Ошибка
)

// ffmpegLogBuffer собирает stderr FFmpeg для логирования при ошибке.
type ffmpegLogBuffer struct {
	camID string
	data  []byte
}

func (b *ffmpegLogBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

// LogOnError записывает stderr в лог если процесс завершился с ошибкой.
func (b *ffmpegLogBuffer) LogOnError(exitError error) {
	if exitError == nil {
		return
	}
	if len(b.data) > 0 {
		// Обрезать последние 1000 символов чтобы не засорять лог
		msg := string(b.data)
		if len(msg) > 1000 {
			msg = "...[truncated] " + msg[len(msg)-1000:]
		}
		fmt.Printf("[Camera %s] FFmpeg stderr: %s\n", b.camID, msg)
	}
}

// String возвращает строковое представление состояния.
func (s State) String() string {
	switch s {
	case StateUnknown:
		return "unknown"
	case StateOffline:
		return "offline"
	case StateConnecting:
		return "connecting"
	case StateOnline:
		return "online"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

// CameraManager управляет множеством камер.
type CameraManager struct {
	cameras map[string]*Camera
	mu      sync.RWMutex
}

// NewCameraManager создаёт новый менеджер камер.
func NewCameraManager() *CameraManager {
	return &CameraManager{
		cameras: make(map[string]*Camera),
	}
}

// AddCamera добавляет камеру в менеджер.
func (cm *CameraManager) AddCamera(cfg config.CameraConfig) *Camera {
	cam := NewCamera(cfg)
	cm.mu.Lock()
	cm.cameras[cfg.ID] = cam
	cm.mu.Unlock()
	return cam
}

// GetCamera возвращает камеру по ID.
func (cm *CameraManager) GetCamera(id string) (*Camera, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cam, ok := cm.cameras[id]
	return cam, ok
}

// ListCameras возвращает список всех камер.
func (cm *CameraManager) ListCameras() []*Camera {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make([]*Camera, 0, len(cm.cameras))
	for _, cam := range cm.cameras {
		result = append(result, cam)
	}
	return result
}

// GetAllStates возвращает map cameraID -> state для health check.
func (cm *CameraManager) GetAllStates() map[string]string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	states := make(map[string]string)
	for id, cam := range cm.cameras {
		states[id] = cam.StateString()
	}
	return states
}

// StartAll запускает запись со всех камер.
func (cm *CameraManager) StartAll(ctx context.Context) error {
	cameras := cm.ListCameras()
	if len(cameras) == 0 {
		return fmt.Errorf("нет камер для запуска")
	}

	for _, cam := range cameras {
		go func(c *Camera) {
			if err := c.Start(ctx, c.getOutputPath()); err != nil {
				fmt.Printf("[CameraManager] Ошибка запуска камеры %s: %v\n", c.ID(), err)
			}
		}(cam)
	}

	return nil
}

// StopAll останавливает запись со всех камер.
func (cm *CameraManager) StopAll() error {
	cameras := cm.ListCameras()
	for _, cam := range cameras {
		if err := cam.Stop(); err != nil {
			fmt.Printf("[CameraManager] Ошибка остановки камеры %s: %v\n", cam.ID(), err)
		}
	}
	return nil
}

// Camera представляет одну камеру наблюдения.
type Camera struct {
	cfg     config.CameraConfig
	state   State
	mu      sync.RWMutex
	started time.Time
	// FFmpeg
	ffmpegCmd *exec.Cmd
	ffmpegCtx context.CancelFunc
	// Reconnect
	reconnectAttempts int
	maxReconnect      int
	reconnectDelay    time.Duration
}

// NewCamera создаёт новую камеру.
func NewCamera(cfg config.CameraConfig) *Camera {
	return &Camera{
		cfg:            cfg,
		state:          StateOffline,
		maxReconnect:   5,
		reconnectDelay: 5 * time.Second,
	}
}

// Start запускает запись с камеры через FFmpeg.
func (c *Camera) Start(ctx context.Context, outputPath string) error {
	c.mu.Lock()
	if c.state == StateOnline {
		c.mu.Unlock()
		return fmt.Errorf("камера уже запущена")
	}
	c.setState(StateConnecting)
	c.mu.Unlock()

	// Сформировать команду FFmpeg
	cmd, cancel, err := c.buildFFmpegCommand(ctx, outputPath)
	if err != nil {
		c.setState(StateError)
		return fmt.Errorf("создать FFmpeg команду: %w", err)
	}

	c.mu.Lock()
	c.ffmpegCmd = cmd
	c.ffmpegCtx = cancel
	c.mu.Unlock()

	// Запустить FFmpeg
	if err := cmd.Start(); err != nil {
		c.setState(StateError)
		return fmt.Errorf("запустить FFmpeg: %w", err)
	}

	c.setState(StateOnline)
	c.started = time.Now()

	// Запустить мониторинг процесса
	go c.monitorProcess(cmd, ctx)

	return nil
}

// Stop останавливает запись с камеры.
func (c *Camera) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ffmpegCmd == nil || c.ffmpegCmd.Process == nil {
		return nil
	}

	if c.ffmpegCtx != nil {
		c.ffmpegCtx()
	}

	// Graceful shutdown
	if err := c.ffmpegCmd.Process.Signal(os.Interrupt); err != nil {
		// Force kill
		c.ffmpegCmd.Process.Kill()
	}

	c.setState(StateOffline)
	c.ffmpegCmd = nil
	c.ffmpegCtx = nil

	return nil
}

// State возвращает текущее состояние камеры.
func (c *Camera) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// StateString возвращает строковое представление состояния.
func (c *Camera) StateString() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.String()
}

// IsRunning возвращает true если камера активна.
func (c *Camera) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state == StateOnline
}

// StartedAt возвращает время начала записи.
func (c *Camera) StartedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.started
}

// Uptime возвращает время работы.
func (c *Camera) Uptime() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.started.IsZero() {
		return 0
	}
	return time.Since(c.started)
}

// ReconnectAttempts возвращает количество попыток переподключения.
func (c *Camera) ReconnectAttempts() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reconnectAttempts
}

// ID возвращает ID камеры.
func (c *Camera) ID() string {
	return c.cfg.ID
}

// Name возвращает имя камеры.
func (c *Camera) Name() string {
	return c.cfg.Name
}

// RTSPURL возвращает RTSP URL (без учётных данных).
func (c *Camera) RTSPURL() string {
	return config.SanitizeRTSPURL(c.cfg.RTSPURL)
}

// setState устанавливает состояние камеры.
func (c *Camera) setState(s State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = s
}

// buildFFmpegCommand создаёт команду FFmpeg для записи.
func (c *Camera) buildFFmpegCommand(ctx context.Context, outputPath string) (*exec.Cmd, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(ctx)

	// FFmpeg команда для непрерывной записи
	// -rtsp_transport tcp — TCP транспорт для RTSP
	// -i — входной поток
	// -c copy — копировать без перекодировки
	// -f mp4 — контейнер MP4
	// -segment_time — длительность сегмента
	// -reset_timestamps 1 — сброс таймстампов для каждого сегмента
	// -fflags +flush_packets — гарантировать запись данных
	args := []string{
		"-rtsp_transport", "tcp",
		"-i", c.cfg.RTSPURL,
		"-c", "copy",
		"-f", "mp4",
		"-segment_time", "300", // 5 минут
		"-reset_timestamps", "1",
		"-avoid_negative_ts", "1",
		"-fflags", "+flush_packets",
		"-max_muxer_queue_size", "1024",
		outputPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// Перенаправить stderr в буфер для логирования при ошибке
	cmd.Stderr = &ffmpegLogBuffer{camID: c.cfg.ID}

	return cmd, cancel, nil
}

// monitorProcess отслеживает процесс FFmpeg и перезапускает при необходимости.
// При неожиданном завершении логирует stderr и пытается переподключиться.
func (c *Camera) monitorProcess(cmd *exec.Cmd, ctx context.Context) {
	err := cmd.Wait()

	c.mu.Lock()
	c.ffmpegCmd = nil
	c.mu.Unlock()

	// Проверить, было ли это запланированное завершение (stop/shutdown)
	select {
	case <-ctx.Done():
		// Завершено по контексту — это нормально
		c.setState(StateOffline)
		return
	default:
		// Процесс завершился сам — ошибка или обрыв RTSP
	}

	// Логировать stderr FFmpeg
	if buf, ok := cmd.Stderr.(*ffmpegLogBuffer); ok {
		buf.LogOnError(err)
	}

	// Записать ошибку в базу
	if err != nil {
		fmt.Printf("[Camera %s] FFmpeg завершился: %v\n", c.cfg.ID, err)
	}

	c.setState(StateOffline)
	c.reconnectAttempts++

	// Попытаться переподключиться с экспоненциальной задержкой
	go c.reconnect(ctx)
}

// reconnect пытается переподключиться к камере.
func (c *Camera) reconnect(ctx context.Context) {
	maxAttempts := c.maxReconnect
	delay := c.reconnectDelay

	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			// Попробовать проверить доступность камеры
			if err := c.checkCamera(ctx); err != nil {
				c.setState(StateError)
				delay = delay * 2 // Экспоненциальная задержка
				continue
			}

			c.setState(StateConnecting)
			// Перезапустить запись
			if err := c.Start(ctx, c.getOutputPath()); err != nil {
				delay = delay * 2
				continue
			}

			// Успешно переподключились
			c.reconnectAttempts = 0
			return
		}
	}

	c.setState(StateError)
}

// checkCamera проверяет доступность камеры через ffprobe.
func (c *Camera) checkCamera(ctx context.Context) error {
	args := []string{
		"-timeout", "5000000", // 5 секунд
		"-i", c.cfg.RTSPURL,
		"-f", "null",
		"-",
	}

	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	err := cmd.Run()
	return err
}

// getOutputPath возвращает паттерн пути для FFmpeg сегментов.
// Формат: recordings/camera-id/YYYY/MM/DD/seg_XXX.mp4
func (c *Camera) getOutputPath() string {
	now := time.Now()
	dateDir := filepath.Join("recordings", c.cfg.ID,
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
	)
	return filepath.Join(dateDir, "seg_%03d.mp4")
}

// GetOutputPath возвращает публичный доступ к паттерну пути сегментации.
func (c *Camera) GetOutputPath() string {
	return c.getOutputPath()
}
