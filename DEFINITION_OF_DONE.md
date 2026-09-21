# VideoGuard — Definition of Done

Каждый компонент считается готовым **только если** выполнены ВСЕ пункты.

---

## Recorder

- [ ] Записал RTSP → появились сегменты в `recordings/camera-id/YYYY/MM/DD/`
- [ ] Сегменты создаются каждые 5 минут (`-segment_time 300`)
- [ ] Пережил обрыв RTSP — камера перешла в `offline`, затем восстановилась
- [ ] Пережил рестарт сервера — запись продолжилась автоматически
- [ ] FFmpeg процесс корректно завершается при `rec.Stop()`
- [ ] В логах есть `[Recorder]` сообщения о запуске/остановке

---

## CameraManager

- [ ] Камера отключилась → статус изменился на `offline`
- [ ] Камера восстановилась → статус изменился обратно на `online`
- [ ] Автоматический reconnect с экспоненциальной задержкой работает
- [ ] `GetCamera(id)` возвращает камеру и `ok=true`
- [ ] `ListCameras()` возвращает все камеры
- [ ] `StartAll()` запускает все камеры параллельно
- [ ] `StopAll()` останавливает все камеры

---

## MAX Notifier

- [ ] Создано событие → сообщение реально ушло в Telegram MAX
- [ ] Статус в SQLite `notifications` стал `sent`
- [ ] При ошибке — статус `pending`, retries увеличен
- [ ] Очередь не блокирует основной pipeline (async)
- [ ] Очередь не падает при переполнении (возвращает error)
- [ ] Поддерживает retry logic с настраиваемой задержкой

---

## Live Stream (MJPEG)

- [ ] Открыл `/live` → FFmpeg стартовал, поток пошёл
- [ ] Первый клиент запускает FFmpeg, последующие подключаются к тому же процессу
- [ ] Закрыл страницу → FFmpeg завершился (последний клиент отключился)
- [ ] `/api/v1/stream/mjpeg/{camera-id}` возвращает `multipart/x-mixed-replace`
- [ ] Заголовки `Cache-Control: no-cache` и `Connection: keep-alive`
- [ ] Нет data race при concurrent доступе к stream

---

## Snapshot

- [ ] `/api/v1/snapshot/{camera-id}` возвращает JPEG
- [ ] Главная страница показывает snapshot для каждой камеры
- [ ] Snapshot обновляется раз в 5 секунд (JS polling)
- [ ] Камера `offline` → показывает заглушку "OFFLINE"
- [ ] Камера `online` → показывает "LIVE" badge

---

## Watchdog

### CameraWatchdog
- [ ] Проверяет камеры каждые 15 секунд через ffprobe
- [ ] Генерирует `camera_offline` событие при недоступности
- [ ] Генерирует `camera_online` событие при восстановлении
- [ ] Корректно останавливается при shutdown

### RecorderWatchdog
- [ ] Проверяет что сегменты записываются (каждые 60с)
- [ ] Генерирует `recording_stopped` если нет свежих сегментов
- [ ] Не генерирует ложные срабатывания

### StorageWatchdog
- [ ] Проверяет диск каждые 30 секунд
- [ ] Генерирует `storage_warning` при >75%
- [ ] Генерирует `storage_full` при >90%
- [ ] Корректно считает FreeBytes

---

## Network/Modem

- [ ] Видит, есть интернет или нет (ping + DNS + TCP)
- [ ] Генерирует `network_lost` при потере
- [ ] Генерирует `network_restored` при восстановлении
- [ ] `/api/v1/status` возвращает network status
- [ ] Поддерживает ModemManager (если доступен)
- [ ] Поддерживает nmcli (если доступен)
- [ ] Не управляет модемом — только мониторинг

---

## Web UI

- [ ] Главная страница: минималистичный "апплаенс" дизайн
- [ ] Статистика: uptime, total_events, cameras count
- [ ] Сетка камер с snapshot для каждой
- [ ] Страница `/live` с MJPEG потоками
- [ ] Страница `/events` с фильтрацией
- [ ] Страница `/archive` с сегментами
- [ ] Тёмная тема, responsive дизайн
- [ ] Без ощущения Frigate/HomeAssistant — чисто, быстро

---

## Database (SQLite)

- [ ] `events` таблица с UUID v4 ID
- [ ] `notifications` таблица с status/retries
- [ ] `camera_state` таблица с last_seen/uptime
- [ ] WAL режим для производительности
- [ ] Индексы на camera_id, type, started_at
- [ ] Metadata хранится как JSON TEXT
- [ ] Миграции применяются при старте

---

## General

- [ ] `go build ./...` проходит без ошибок
- [ ] `go vet ./...` проходит без предупреждений
- [ ] `go test ./...` зелёный (все пакеты)
- [ ] `go test -race ./...` без data race
- [ ] `GOOS=linux GOARCH=386 CGO_ENABLED=0 go build` — кросс-компиляция
- [ ] Graceful shutdown: HTTP сервер останавливается за ≤30с
- [ ] Логирование: `[Component]` префикс для всех компонентов
- [ ] Конфигурация: YAML + secrets.env поддержка

---

## Definition of Ready (перед началом работы)

- [ ] Понятно что делать (требования описаны)
- [ ] Понятно как проверять (есть критерии)
- [ ] Понятно что НЕ делать (ограничения)
- [ ] Нет blockers (зависимостей от других фич)
