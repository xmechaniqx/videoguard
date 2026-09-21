// Package stream предоставляет MJPEG live stream и snapshot функциональность.
//
// MJPEG stream запускается "по требованию":
// - Первый клиент запускает FFmpeg процесс
// - Все последующие клиенты подключаются к тому же процессу
// - Когда последний клиент отключается — FFmpeg останавливается
//
// Snapshot — однократный снимок с камеры через FFmpeg.
package stream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"videoguard/internal/config"
)

// StreamManager управляет MJPEG потоками для камер.
type StreamManager struct {
	mu         sync.RWMutex
	streams    map[string]*Stream // key = cameraID
	cfg        config.CameraConfig
	ffmpegPath string
}

// Stream представляет активный MJPEG поток.
type Stream struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	ctx        context.Context
	cancel     context.CancelFunc
	viewers    int64 // atomic counter
	outputPipe io.ReadCloser
	stderr     io.ReadCloser
}

// NewStreamManager создаёт новый менеджер потоков.
func NewStreamManager() *StreamManager {
	return &StreamManager{
		streams:    make(map[string]*Stream),
		ffmpegPath: "ffmpeg",
	}
}

// GetStream возвращает или создаёт MJPEG поток для камеры.
func (sm *StreamManager) GetStream(cameraID string, rtspURL string) *Stream {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if stream, exists := sm.streams[cameraID]; exists {
		// Увеличить счётчик зрителей
		atomic.AddInt64(&stream.viewers, 1)
		log.Printf("[Stream] Камера %s: зритель #%d", cameraID, atomic.LoadInt64(&stream.viewers))
		return stream
	}

	// Создать новый поток
	stream := &Stream{
		ctx:    context.Background(),
		cancel: func() {},
	}

	// Запустить FFmpeg
	if err := stream.start(rtspURL); err != nil {
		log.Printf("[Stream] Ошибка запуска FFmpeg для %s: %v", cameraID, err)
		return nil
	}

	sm.streams[cameraID] = stream
	log.Printf("[Stream] MJPEG поток запущен для камеры %s", cameraID)
	return stream
}

// ReleaseStream уменьшает счётчик зрителей.
// Когда счётчик достигает 0 — поток останавливается.
func (sm *StreamManager) ReleaseStream(cameraID string) {
	sm.mu.Lock()
	stream, exists := sm.streams[cameraID]
	sm.mu.Unlock()

	if !exists {
		return
	}

	count := atomic.AddInt64(&stream.viewers, -1)
	log.Printf("[Stream] Камера %s: зрителей осталось %d", cameraID, count)

	if count <= 0 {
		log.Printf("[Stream] Последний зритель отключился, останавливаем FFmpeg для %s", cameraID)
		stream.stop()

		sm.mu.Lock()
		delete(sm.streams, cameraID)
		sm.mu.Unlock()
	}
}

// TakeSnapshot делает однократный снимок с камеры.
func TakeSnapshot(rtspURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-frames:v", "1", // один кадр
		"-f", "mjpeg",
		"-", // stdout
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("сделать снимок: %w", err)
	}

	return output, nil
}

// start запускает FFmpeg процесс для MJPEG stream.
func (s *Stream) start(rtspURL string) error {
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	s.cancel = cancel

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-c:v", "mjpeg",
		"-q:v", "5", // качество 1-31 (5 — среднее)
		"-f", "multipartmux",
		"-boundary", "123456789009876543210",
		"-", // stdout
	)

	var errOut bytes.Buffer
	cmd.Stderr = &errOut

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("создать pipe: %w", err)
	}
	s.outputPipe = pipe

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запустить FFmpeg: %w", err)
	}

	s.cmd = cmd

	// Запустить мониторинг процесса
	go s.monitor()

	log.Printf("[Stream] FFmpeg запущен для MJPEG потока")
	return nil
}

// stop останавливает FFmpeg процесс.
func (s *Stream) stop() {
	if s.cmd == nil {
		return
	}

	s.cancel()

	if s.outputPipe != nil {
		s.outputPipe.Close()
	}

	// Дать процессу время на graceful shutdown
	time.Sleep(500 * time.Millisecond)

	// Если процесс ещё жив — kill
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}

	log.Printf("[Stream] FFmpeg остановлен для MJPEG потока")
}

// Reader возвращает io.ReadCloser для MJPEG данных.
func (s *Stream) Reader() io.ReadCloser {
	return s.outputPipe
}

// monitor отслеживает процесс FFmpeg.
func (s *Stream) monitor() {
	if s.cmd == nil {
		return
	}

	err := s.cmd.Wait()
	if err != nil {
		log.Printf("[Stream] FFmpeg завершил работу с ошибкой: %v", err)
	}
}
