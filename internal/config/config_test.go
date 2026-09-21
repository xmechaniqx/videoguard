package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDefaultConfig проверяет значения конфигурации по умолчанию.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Errorf("ожидался listen '0.0.0.0:8080', получено '%s'", cfg.Server.Listen)
	}

	if cfg.Storage.RetentionDays != 7 {
		t.Errorf("ожидался retention_days 7, получено %d", cfg.Storage.RetentionDays)
	}

	if len(cfg.Cameras) != 1 {
		t.Errorf("ожидалась 1 камера, получено %d", len(cfg.Cameras))
	}

	if cfg.Recording.SegmentDuration != 300*time.Second {
		t.Errorf("ожидался segment_duration 300s, получено %v", cfg.Recording.SegmentDuration)
	}
}

// TestLoadConfig проверяет загрузку конфигурации из файла.
func TestLoadConfig(t *testing.T) {
	// Создать временный файл конфигурации
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
server:
  listen: "127.0.0.1:9090"
  
storage:
  root: "/tmp/test-storage"
  retention_days: 14

cameras:
  - id: "test-camera"
    name: "Test"
    rtsp_url: "rtsp://test:pass@192.168.1.1:554/stream"
    username: "test"
    password: "pass"

recording:
  enabled: true
  segment_duration: "600s"
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("не удалось создать тестовый файл: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("не удалось загрузить конфигурацию: %v", err)
	}

	if cfg.Server.Listen != "127.0.0.1:9090" {
		t.Errorf("ожидался listen '127.0.0.1:9090', получено '%s'", cfg.Server.Listen)
	}

	if cfg.Storage.RetentionDays != 14 {
		t.Errorf("ожидался retention_days 14, получено %d", cfg.Storage.RetentionDays)
	}

	if len(cfg.Cameras) != 1 {
		t.Fatalf("ожидалась 1 камера, получено %d", len(cfg.Cameras))
	}

	if cfg.Cameras[0].ID != "test-camera" {
		t.Errorf("ожидался ID 'test-camera', получено '%s'", cfg.Cameras[0].ID)
	}
}

// TestLoadConfigWithSecrets проверяет загрузку секретов из .env файла.
func TestLoadConfigWithSecrets(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	secretsPath := filepath.Join(tmpDir, "secrets.env")

	configContent := `
server:
  listen: "0.0.0.0:8080"
  
storage:
  root: "/tmp/test-storage"
  retention_days: 7

cameras:
  - id: "test-camera"
    name: "Test"
    rtsp_url: "rtsp://localhost:554/stream"
    username: "${CAM_USERNAME}"
    password: "${CAM_PASSWORD}"
`

	secretsContent := `
CAM_USERNAME=secretuser
CAM_PASSWORD=secretpass
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("не удалось создать конфиг: %v", err)
	}

	if err := os.WriteFile(secretsPath, []byte(secretsContent), 0644); err != nil {
		t.Fatalf("не удалось создать секреты: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("не удалось загрузить конфигурацию: %v", err)
	}

	if cfg.Cameras[0].Username != "secretuser" {
		t.Errorf("ожидался username 'secretuser', получено '%s'", cfg.Cameras[0].Username)
	}

	if cfg.Cameras[0].Password != "secretpass" {
		t.Errorf("ожидался password 'secretpass', получено '%s'", cfg.Cameras[0].Password)
	}
}

// TestValidateConfig проверяет валидацию конфигурации.
func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "валидная конфигурация",
			cfg:     DefaultConfig(),
			wantErr: false,
		},
		{
			name: "пустой listen",
			cfg: Config{
				Server:  ServerConfig{Listen: ""},
				Storage: StorageConfig{Root: "/tmp/test"},
				Cameras: []CameraConfig{{ID: "cam1", RTSPURL: "rtsp://test"}},
			},
			wantErr: true,
		},
		{
			name: "пустой root",
			cfg: Config{
				Server:  ServerConfig{Listen: "0.0.0.0:8080"},
				Storage: StorageConfig{Root: ""},
				Cameras: []CameraConfig{{ID: "cam1", RTSPURL: "rtsp://test"}},
			},
			wantErr: true,
		},
		{
			name: "retention_days < 1",
			cfg: Config{
				Server:  ServerConfig{Listen: "0.0.0.0:8080"},
				Storage: StorageConfig{Root: "/tmp/test", RetentionDays: 0},
				Cameras: []CameraConfig{{ID: "cam1", RTSPURL: "rtsp://test"}},
			},
			wantErr: true,
		},
		{
			name: "пустой список камер",
			cfg: Config{
				Server:  ServerConfig{Listen: "0.0.0.0:8080"},
				Storage: StorageConfig{Root: "/tmp/test"},
				Cameras: []CameraConfig{},
			},
			wantErr: true,
		},
		{
			name: "камера без ID",
			cfg: Config{
				Server:  ServerConfig{Listen: "0.0.0.0:8080"},
				Storage: StorageConfig{Root: "/tmp/test"},
				Cameras: []CameraConfig{{ID: "", RTSPURL: "rtsp://test"}},
			},
			wantErr: true,
		},
		{
			name: "камера без RTSP URL",
			cfg: Config{
				Server:  ServerConfig{Listen: "0.0.0.0:8080"},
				Storage: StorageConfig{Root: "/tmp/test"},
				Cameras: []CameraConfig{{ID: "cam1", RTSPURL: ""}},
			},
			wantErr: true,
		},
		{
			name: "segment_duration < 60s",
			cfg: Config{
				Server:    ServerConfig{Listen: "0.0.0.0:8080"},
				Storage:   StorageConfig{Root: "/tmp/test"},
				Cameras:   []CameraConfig{{ID: "cam1", RTSPURL: "rtsp://test"}},
				Recording: RecordingConfig{SegmentDuration: 30 * time.Second},
			},
			wantErr: true,
		},
		{
			name: "недопустимый уровень логирования",
			cfg: Config{
				Server:  ServerConfig{Listen: "0.0.0.0:8080"},
				Storage: StorageConfig{Root: "/tmp/test"},
				Cameras: []CameraConfig{{ID: "cam1", RTSPURL: "rtsp://test"}},
				Logging: LoggingConfig{Level: "invalid"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() ошибка = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestSanitizeRTSPURL проверяет санитизацию RTSP URL.
func TestSanitizeRTSPURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantSan string
	}{
		{
			name:    "URL с учётными данными",
			input:   "rtsp://admin:password123@192.168.1.100:554/stream",
			wantSan: "rtsp://***:***@192.168.1.100:554/stream",
		},
		{
			name:    "URL без учётных данных",
			input:   "rtsp://192.168.1.100:554/stream",
			wantSan: "rtsp://192.168.1.100:554/stream",
		},
		{
			name:    "пустой URL",
			input:   "",
			wantSan: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeRTSPURL(tt.input)
			if got != tt.wantSan {
				t.Errorf("SanitizeRTSPURL() = '%s', want '%s'", got, tt.wantSan)
			}
		})
	}
}

// TestCameraByID проверяет поиск камеры по ID.
func TestCameraByID(t *testing.T) {
	cfg := DefaultConfig()

	cam := cfg.CameraByID("camera-1")
	if cam == nil {
		t.Fatal("ожидалась камера, получено nil")
	}

	if cam.ID != "camera-1" {
		t.Errorf("ожидался ID 'camera-1', получено '%s'", cam.ID)
	}

	cam = cfg.CameraByID("nonexistent")
	if cam != nil {
		t.Errorf("ожидалось nil для несуществующей камеры, получено %+v", cam)
	}
}

// TestParseLogLevel проверяет преобразование уровня логирования.
func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		level string
		want  int
	}{
		{"debug", 0},
		{"info", 1},
		{"warn", 2},
		{"error", 3},
		{"invalid", 1}, // по умолчанию info
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			cfg := Config{Logging: LoggingConfig{Level: tt.level}}
			got := cfg.ParseLogLevel()
			if got != tt.want {
				t.Errorf("ParseLogLevel() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestResolveSecret проверяет замену плейсхолдеров.
func TestResolveSecret(t *testing.T) {
	secrets := map[string]string{
		"USERNAME": "admin",
		"PASSWORD": "secret123",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"${USERNAME}", "admin"},
		{"${PASSWORD}", "secret123"},
		{"${NONEXISTENT}", "${NONEXISTENT}"},
		{"plain_value", "plain_value"},
		{"${USERNAME}_extra", "${USERNAME}_extra"}, // Только полное совпадение
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := resolveSecret(tt.input, secrets)
			if got != tt.want {
				t.Errorf("resolveSecret() = '%s', want '%s'", got, tt.want)
			}
		})
	}
}

// TestParseDuration проверяет парсинг длительности.
func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		def   time.Duration
		want  time.Duration
	}{
		{"10s", 0, 10 * time.Second},
		{"", 5 * time.Second, 5 * time.Second},
		{"invalid", 10 * time.Second, 10 * time.Second},
		{"300s", 0, 300 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseDuration(tt.input, tt.def)
			if got != tt.want {
				t.Errorf("ParseDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestParseInt проверяет парсинг целых чисел.
func TestParseInt(t *testing.T) {
	tests := []struct {
		input string
		def   int
		want  int
	}{
		{"42", 0, 42},
		{"", 10, 10},
		{"invalid", 5, 5},
		{"0", 10, 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseInt(tt.input, tt.def)
			if got != tt.want {
				t.Errorf("ParseInt() = %d, want %d", got, tt.want)
			}
		})
	}
}
