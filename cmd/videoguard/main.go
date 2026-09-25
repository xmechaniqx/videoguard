// Package main — точка входа приложения VideoGuard.
//
// VideoGuard — лёгкая система видеонаблюдения для домашнего/дачного сервера.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"videoguard/internal/camera"
	"videoguard/internal/config"
	"videoguard/internal/db"
	"videoguard/internal/events"
	"videoguard/internal/notifier"
	"videoguard/internal/notifier/max"
	"videoguard/internal/recorder"
	"videoguard/internal/selftest"
	"videoguard/internal/storage"
	"videoguard/internal/watchdog"
	"videoguard/internal/web"
)

const (
	appName    = "VideoGuard"
	appVersion = "0.2.0"
)

func main() {
	// Канал для graceful shutdown компонентов
	stopComponents := make(chan struct{})

	// ============================================================
	// Обработка сигналов — сразу, чтобы Ctrl+C работал всегда
	// ============================================================
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("============================================================")
		log.Printf("%s — Получен сигнал остановки (Ctrl+C)", appName)
		log.Printf("============================================================")
		close(stopComponents)
	}()

	// Разобрать командную строку
	configPath := flag.String("config", "", "Путь к файлу конфигурации (обязательно)")
	devMode := flag.Bool("dev", false, "Режим разработки (без камеры)")
	showVersion := flag.Bool("version", false, "Показать версию")
	validateOnly := flag.Bool("validate-config", false, "Только валидация конфигурации")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s v%s\n", appName, appVersion)
		os.Exit(0)
	}

	if *configPath == "" {
		fmt.Fprintf(os.Stderr, "Ошибка: указан --config (путь к файлу конфигурации)\n")
		fmt.Fprintf(os.Stderr, "Использование: %s --config /path/to/config.yaml [--dev]\n", appName)
		os.Exit(1)
	}

	// Режим валидации конфигурации
	if *validateOnly {
		if err := validateConfigOnly(*configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Конфигурация невалидна:\n%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Конфигурация валидна: %s\n", *configPath)
		os.Exit(0)
	}

	// Создать логгер
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Printf("============================================================")
	log.Printf("%s v%s — Запуск", appName, appVersion)
	log.Printf("============================================================")

	// Загрузить конфигурацию
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}

	log.Printf("Конфигурация загружена из %s", *configPath)
	log.Printf("Режим разработки: %v", *devMode)

	// ============================================================
	// Startup Self Test
	// ============================================================
	stResults, stErr := selftest.Run(cfg, true)
	if stErr != nil {
		log.Fatalf("Самодиагностика не пройдена: %v", stErr)
	}
	log.Printf("Самодиагностика пройдена: %d проверок", len(stResults))

	// Инициализировать хранилище
	st := storage.NewStorage(cfg.Storage.Root)
	if err := st.Init(); err != nil {
		log.Fatalf("Ошибка инициализации хранилища: %v", err)
	}
	log.Printf("Хранилище инициализировано: %s", cfg.Storage.Root)

	// Инициализировать SQLite базу данных
	dbPath := st.PathTmp("events.db")
	if cfg.Storage.Root != "" {
		dbPath = cfg.Storage.Root + "/events.db"
	}
	d, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("Ошибка открытия БД: %v", err)
	}
	defer d.Close()

	if err := d.Migrate(); err != nil {
		log.Fatalf("Ошибка миграции БД: %v", err)
	}
	log.Printf("База данных инициализирована: %s", dbPath)

	// Инициализировать менеджер событий с SQLite
	eventMgr := events.NewEventManager(d, 0) // 0 = без ограничения по времени
	log.Printf("Менеджер событий инициализирован (SQLite)")

	// Инициализировать уведомитель с deduplication
	if cfg.Notifier.MAX.Enabled {
		maxNotifier := max.NewNotifier(
			cfg.Notifier.MAX.BotToken,
			cfg.Notifier.MAX.ChatID,
			cfg.Notifier.MAX.RetryMax,
			cfg.Notifier.MAX.RetryDelay,
		)
		// Deduplicator оборачивает notifier — motion события агрегируются
		_ = notifier.NewDeduplicator(maxNotifier, cfg.Notifier.Cooldown)
		log.Printf("Уведомитель MAX с deduplication запущен (cooldown: %v)", cfg.Notifier.Cooldown)
	} else {
		log.Printf("Уведомления отключены")
	}

	// Инициализировать менеджер камер
	camMgr := camera.NewCameraManager()
	for _, camCfg := range cfg.Cameras {
		camMgr.AddCamera(camCfg)
	}
	log.Printf("Менеджер камер инициализирован: %d камер", len(camMgr.ListCameras()))

	// Инициализировать рекордер
	rec := recorder.NewRecorder(cfg.Recording, camMgr, st)

	// Инициализировать watchdog-компоненты
	ctx := context.Background()

	// CameraWatchdog — проверка камер каждые 15 секунд
	cameraWD := watchdog.NewCameraWatchdog(eventMgr, cfg.Cameras)

	// RecorderWatchdog — проверка записи каждые 60 секунд
	recorderWD := watchdog.NewRecorderWatchdog(eventMgr, st, cfg.Cameras)

	// StorageWatchdog — проверка диска каждые 30 секунд
	storageWD := watchdog.NewStorageWatchdog(eventMgr, st)

	// Инициализировать HTTP-сервер
	server := web.NewServer(&cfg, eventMgr, st, *devMode, camMgr, rec)

	// Запустить HTTP-сервер ДО всего — чтобы веб-интерфейс работал всегда
	go func() {
		if err := server.Start(); err != nil {
			log.Printf("Ошибка HTTP-сервера: %v", err)
		}
	}()

	log.Printf("HTTP-сервер запущен на %s", cfg.Server.Listen)
	log.Printf("Веб-интерфейс доступен: http://localhost:%s", strings.TrimPrefix(cfg.Server.Listen, "0.0.0.0:"))

	// Показать статус камер
	for _, cam := range cfg.Cameras {
		log.Printf("Камера: %s (%s)", cam.Name, config.SanitizeRTSPURL(cam.RTSPURL))
	}

	// Запустить recorder (если не dev mode или еслиdev mode с камерой)
	if !*devMode {
		// Запустить recorder с таймаутом — если не успевает за 10с, всё равно продолжаем
		recStartDone := make(chan error, 1)
		go func() {
			recStartDone <- rec.Start()
		}()

		select {
		case err := <-recStartDone:
			if err != nil {
				log.Printf("Предупреждение: рекордер не запущен: %v", err)
			} else {
				log.Printf("Recorder запущен")
			}
		case <-time.After(10 * time.Second):
			log.Printf("Предупреждение: рекордер не запустился за 10с — продолжаем без него")
		}

		// Запустить watchdog-компоненты
		cameraWD.Start(ctx)
		log.Printf("CameraWatchdog запущен (проверка каждые 15с)")

		recorderWD.Start(ctx)
		log.Printf("RecorderWatchdog запущен (проверка каждые 60с)")

		storageWD.Start(ctx)
		log.Printf("StorageWatchdog запущен (проверка каждые 30с)")
	} else {
		log.Printf("Recorder пропущен (dev mode)")
	}

	// Ожидать сигнал остановки
	<-stopComponents

	log.Printf("============================================================")
	log.Printf("%s — Остановка", appName)
	log.Printf("============================================================")

	// Запустить graceful shutdown в горутине с таймаутом
	done := make(chan struct{})
	go func() {
		// Остановить HTTP-сервер (прекращает новые запросы)
		if err := server.Shutdown(); err != nil {
			log.Printf("[Shutdown] Ошибка остановки сервера: %v", err)
		}

		// Остановить watchdog-компоненты
		cameraWD.Stop()
		recorderWD.Stop()
		storageWD.Stop()

		// Остановить recorder
		if !*devMode {
			if err := rec.Stop(); err != nil {
				log.Printf("[Shutdown] Ошибка остановки recorder: %v", err)
			} else {
				log.Printf("[Shutdown] Recorder остановлен")
			}
		}

		close(done)
	}()

	// Ждать shutdown с таймаутом 5 секунд
	select {
	case <-done:
		log.Printf("Завершение работы...")
		log.Printf("%s остановлен", appName)
	case <-time.After(5 * time.Second):
		log.Printf("[Shutdown] Таймаут 5с — принудительное завершение")
	}
}

// validateConfigOnly проверяет конфигурацию без запуска приложения.
func validateConfigOnly(path string) error {
	// 1. YAML читается
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("не прочитать файл: %w", err)
	}

	// 2. YAML парсится
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("не разобрать YAML: %w", err)
	}

	// 3. Все обязательные поля заполнены (Validate собирает все ошибки)
	if err := cfg.Validate(); err != nil {
		return err
	}

	// 4. Пути могут быть созданы
	if cfg.Storage.Root != "" {
		if err := os.MkdirAll(cfg.Storage.Root, 0755); err != nil {
			return fmt.Errorf("нельзя создать директорию хранения: %w", err)
		}
	}

	// 5. /srv/videoguard-data доступен на запись (или может быть создан)
	storageRoot := cfg.Storage.Root
	if storageRoot == "" {
		storageRoot = "/srv/videoguard-data"
	}
	if f, err := os.OpenFile(storageRoot, os.O_WRONLY, 0); err != nil {
		// Попробовать создать
		if mkErr := os.MkdirAll(storageRoot, 0755); mkErr != nil {
			return fmt.Errorf("нельзя записать в %s: %v (создать: %v)", storageRoot, err, mkErr)
		}
	} else {
		f.Close()
	}

	// 6. SQLite открывается
	dbPath := filepath.Join(storageRoot, "events.db")
	d, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("нельзя открыть SQLite: %w", err)
	}
	d.Close()
	os.Remove(dbPath) // Удалить тестовый файл

	return nil
}
