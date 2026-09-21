// Package config предоставляет функции для загрузки и валидации конфигурации приложения.
//
// Конфигурация хранится в YAML-файле, чувствительные данные могут быть
// загружены из separate .env-файла с использованием плейсхолдеров ${VAR_NAME}.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config представляет полную конфигурацию приложения.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Storage   StorageConfig   `yaml:"storage"`
	Cameras   []CameraConfig  `yaml:"cameras"`
	Recording RecordingConfig `yaml:"recording"`
	Events    EventsConfig    `yaml:"events"`
	Notifier  NotifierConfig  `yaml:"notifier"`
	Motion    MotionConfig    `yaml:"motion"`
	Logging   LoggingConfig   `yaml:"logging"`
}

// ServerConfig содержит настройки HTTP-сервера.
type ServerConfig struct {
	Listen       string        `yaml:"listen"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

// StorageConfig содержит настройки хранилища и_periods очистки.
type StorageConfig struct {
	Root                       string `yaml:"root"`
	RetentionDays              int    `yaml:"retention_days"`
	EmergencyThresholdPercent  int    `yaml:"emergency_threshold_percent"`
	AggressiveThresholdPercent int    `yaml:"aggressive_threshold_percent"`
}

// CameraConfig содержит конфигурацию одной камеры.
type CameraConfig struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	RTSPURL  string `yaml:"rtsp_url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// RecordingConfig содержит настройки записи.
type RecordingConfig struct {
	Enabled         bool          `yaml:"enabled"`
	SegmentDuration time.Duration `yaml:"segment_duration"`
	CodecCopy       bool          `yaml:"codec_copy"`
}

// EventsConfig содержит настройки обработки событий.
type EventsConfig struct {
	Enabled           bool `yaml:"enabled"`
	PreRecordSeconds  int  `yaml:"pre_record_seconds"`
	PostRecordSeconds int  `yaml:"post_record_seconds"`
}

// NotifierConfig содержит настройки уведомителей.
type NotifierConfig struct {
	MAX            MAXNotifierConfig        `yaml:"max"`
	Cooldown       time.Duration            `yaml:"cooldown"`
	CooldownPerCam map[string]time.Duration `yaml:"cooldown_per_camera"`
}

// MAXNotifierConfig содержит настройки бота MAX.
type MAXNotifierConfig struct {
	Enabled    bool          `yaml:"enabled"`
	BotToken   string        `yaml:"bot_token"`
	ChatID     string        `yaml:"chat_id"`
	RetryMax   int           `yaml:"retry_max"`
	RetryDelay time.Duration `yaml:"retry_delay"`
}

// MotionConfig содержит настройки детектора движения.
type MotionConfig struct {
	Enabled    bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"`
}

// LoggingConfig содержит настройки логирования.
type LoggingConfig struct {
	Level string `yaml:"level"`
}

// DefaultConfig возвращает конфигурацию с разумными значениями по умолчанию.
func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Listen:       "0.0.0.0:8080",
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
		},
		Storage: StorageConfig{
			Root:                       "/srv/videoguard-data",
			RetentionDays:              7,
			EmergencyThresholdPercent:  10,
			AggressiveThresholdPercent: 5,
		},
		Cameras: []CameraConfig{
			{
				ID:       "camera-1",
				Name:     "Главная камера",
				RTSPURL:  "rtsp://localhost:554/stream1",
				Username: "",
				Password: "",
			},
		},
		Recording: RecordingConfig{
			Enabled:         true,
			SegmentDuration: 300 * time.Second,
			CodecCopy:       true,
		},
		Events: EventsConfig{
			Enabled:           true,
			PreRecordSeconds:  10,
			PostRecordSeconds: 20,
		},
		Notifier: NotifierConfig{
			MAX: MAXNotifierConfig{
				Enabled:    false,
				BotToken:   "",
				ChatID:     "",
				RetryMax:   3,
				RetryDelay: 5 * time.Second,
			},
			Cooldown:       5 * time.Minute, // default cooldown для motion
			CooldownPerCam: make(map[string]time.Duration),
		},
		Motion: MotionConfig{
			Enabled:    false,
			WebhookURL: "http://127.0.0.1:8080/api/v1/events",
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

// SanitizeRTSPURL убирает учётные данные из RTSP-URL для безопасного логирования.
func SanitizeRTSPURL(url string) string {
	// Найдем символ @, который отделяет учётные данные от хоста
	atIdx := strings.LastIndex(url, "@")
	if atIdx == -1 {
		return url
	}
	// Найдем :// чтобы найти начало учётных данных
	schemeIdx := strings.Index(url, "://")
	if schemeIdx == -1 {
		return url
	}
	credentials := url[schemeIdx+3 : atIdx]
	return strings.Replace(url, credentials, "***:***", 1)
}

// LoadConfig загружает YAML-конфигурацию и применяет переопределения из переменных окружения.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("прочитать файл конфигурации: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("разобрать файл конфигурации: %w", err)
	}

	// Загрузить секреты из .env-файла, если он существует
	secretsPath := filepath.Join(filepath.Dir(path), "secrets.env")
	secrets := loadSecrets(secretsPath)

	// Применить секреты к камерам
	for i := range cfg.Cameras {
		cfg.Cameras[i].Username = resolveSecret(cfg.Cameras[i].Username, secrets)
		cfg.Cameras[i].Password = resolveSecret(cfg.Cameras[i].Password, secrets)
	}

	// Применить секреты к уведомителю
	cfg.Notifier.MAX.BotToken = resolveSecret(cfg.Notifier.MAX.BotToken, secrets)
	cfg.Notifier.MAX.ChatID = resolveSecret(cfg.Notifier.MAX.ChatID, secrets)

	// Проверить валидность
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("проверить конфигурацию: %w", err)
	}

	return cfg, nil
}

// resolveSecret заменяет плейсхолдеры ${VAR} значениями из карты секретов.
func resolveSecret(value string, secrets map[string]string) string {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return value
	}
	varName := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	if val, ok := secrets[varName]; ok {
		return val
	}
	return value // Оставить оригинал, если не найдено
}

// loadSecrets читает файл в формате .env и возвращает карту ключ-значение.
func loadSecrets(path string) map[string]string {
	secrets := make(map[string]string)

	f, err := os.Open(path)
	if err != nil {
		return secrets // Отсутствие файла — это нормально
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return secrets
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Убрать кавычки, если есть
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		secrets[key] = val
	}

	return secrets
}

// ValidationErrors собирает все ошибки валидации конфигурации.
type ValidationErrors []ValidationError

// ValidationError описывает одну ошибку валидации.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error реализует интерфейс error для ValidationErrors.
func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return ""
	}
	msgs := make([]string, len(ve))
	for i, e := range ve {
		msgs[i] = fmt.Sprintf("  %s: %s", e.Field, e.Message)
	}
	return fmt.Sprintf("ошибки конфигурации (%d):\n%s", len(ve), strings.Join(msgs, "\n"))
}

// Validate проверяет конфигурацию на наличие обязательных полей и корректных значений.
// Собирает ВСЕ ошибки, а не останавливается на первой.
func (c *Config) Validate() error {
	var errs ValidationErrors

	// --- Server ---
	if c.Server.Listen == "" {
		errs = append(errs, ValidationError{"server.listen", "обязателен"})
	}

	// --- Storage ---
	if c.Storage.Root == "" {
		errs = append(errs, ValidationError{"storage.root", "обязателен"})
	}

	if c.Storage.RetentionDays < 1 {
		errs = append(errs, ValidationError{"storage.retention_days", "должен быть >= 1"})
	}

	if c.Storage.EmergencyThresholdPercent < 1 || c.Storage.EmergencyThresholdPercent > 50 {
		errs = append(errs, ValidationError{"storage.emergency_threshold_percent", "должен быть от 1 до 50"})
	}

	if c.Storage.AggressiveThresholdPercent < 1 || c.Storage.AggressiveThresholdPercent > 50 {
		errs = append(errs, ValidationError{"storage.aggressive_threshold_percent", "должен быть от 1 до 50"})
	}

	// --- Cameras ---
	if len(c.Cameras) == 0 {
		errs = append(errs, ValidationError{"cameras", "требуется хотя бы одна камера"})
	} else {
		// Проверить обязательные поля камер
		seenIDs := make(map[string]int) // ID -> индекс
		for i, cam := range c.Cameras {
			if cam.ID == "" {
				errs = append(errs, ValidationError{fmt.Sprintf("cameras[%d].id", i), "обязателен"})
			} else {
				// Проверить дубликаты ID
				if dupIdx, exists := seenIDs[cam.ID]; exists {
					errs = append(errs, ValidationError{
						fmt.Sprintf("cameras[%d].id", i),
						fmt.Sprintf("дублирует cameras[%d].id '%s'", dupIdx, cam.ID),
					})
				} else {
					seenIDs[cam.ID] = i
				}
			}
			if cam.RTSPURL == "" {
				camID := cam.ID
				if camID == "" {
					camID = fmt.Sprintf("[%d]", i)
				}
				errs = append(errs, ValidationError{
					fmt.Sprintf("cameras[%d].rtsp_url", i),
					fmt.Sprintf("обязателен для камеры %s", camID),
				})
			} else if !strings.HasPrefix(cam.RTSPURL, "rtsp://") {
				camID := cam.ID
				if camID == "" {
					camID = fmt.Sprintf("[%d]", i)
				}
				errs = append(errs, ValidationError{
					fmt.Sprintf("cameras[%d].rtsp_url", i),
					fmt.Sprintf("должен начинаться с rtsp:// (камера %s)", camID),
				})
			}
		}
	}

	// --- Recording ---
	if c.Recording.SegmentDuration <= 0 {
		errs = append(errs, ValidationError{"recording.segment_duration", "должен быть > 0"})
	} else if c.Recording.SegmentDuration < 60*time.Second {
		errs = append(errs, ValidationError{"recording.segment_duration", "должен быть >= 60s"})
	}

	// --- Notifier ---
	if c.Notifier.Cooldown < 0 {
		errs = append(errs, ValidationError{"notifier.cooldown", "должен быть >= 0"})
	}

	if c.Notifier.MAX.RetryMax < 0 {
		errs = append(errs, ValidationError{"notifier.max.retry_max", "должен быть >= 0"})
	}

	// --- Logging ---
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		errs = append(errs, ValidationError{"logging.level", "должен быть одним из: debug, info, warn, error"})
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// ParseLogLevel преобразует строковый уровень в целое число.
func (c *Config) ParseLogLevel() int {
	switch c.Logging.Level {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn":
		return 2
	case "error":
		return 3
	default:
		return 1 // по умолчанию info
	}
}

// CameraByID возвращает конфигурацию камеры по ID.
func (c *Config) CameraByID(id string) *CameraConfig {
	for i := range c.Cameras {
		if c.Cameras[i].ID == id {
			return &c.Cameras[i]
		}
	}
	return nil
}

// ParseDuration разбирает строку длительности, возвращает значение по умолчанию, если пусто.
func ParseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

// ParseInt разбирает строку в целое число, возвращает значение по умолчанию, если пусто.
func ParseInt(s string, def int) int {
	if s == "" {
		return def
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return i
}
