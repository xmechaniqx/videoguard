# VideoGuard v1.0

Лёгкая система видеонаблюдения для домашнего/дачного сервера на слабом железе.

## Архитектура

```
Камера (RTSP)
    |
  FFmpeg
    |
  Запись (непрерывные сегменты)

Детектор Motion (внешний)
    |
  Менеджер событий
    |
  +----------+-----------+-----------+
  |          |           |           |
Хранилище   Снимок    Уведомитель   API
                         |
                        MAX
```

## Требования

- **Целевая платформа:** Debian 12 i386, Intel Atom N2600
- **CPU:** Intel Atom N2600 (или аналог)
- **RAM:** 2 ГБ
- **Go:** 1.21+ (только для сборки)
- **FFmpeg:** внешняя зависимость (для записи RTSP)
- **Motion:** внешняя зависимость (опционально, для детекции движения)

---

## First install on Debian 12 i386

### 1. Установка зависимостей

```bash
# Обновить систему
sudo apt update && sudo apt upgrade -y

# Установить FFmpeg
sudo apt install -y ffmpeg

# Установить Motion (опционально, для детекции движения)
sudo apt install -y motion

# Проверить
ffmpeg -version
ffprobe -version
```

### 2. Создание каталогов

```bash
# Каталог бинарника
sudo mkdir -p /opt/videoguard

# Каталог данных (записи, события, снимки)
sudo mkdir -p /srv/videoguard-data

# Каталог конфигурации
sudo mkdir -p /etc/videoguard

# Каталог логов
sudo mkdir -p /var/log/videoguard
```

### 3. Копирование файлов

```bash
# Копировать бинарник
sudo cp videoguard /opt/videoguard/
sudo chown -R username:username /opt/videoguard

# Копировать конфигурацию
sudo cp configs/config.example.yaml /etc/videoguard/config.yaml

# Настроить права на данные
sudo chown -R username:username /srv/videoguard-data
```

### 4. Настройка конфигурации

```bash
# Отредактировать конфигурацию
sudo $EDITOR /etc/videoguard/config.yaml
```

**Основные параметры:**

```yaml
server:
  listen: "0.0.0.0:8080"

storage:
  root: "/srv/videoguard-data"
  retention_days: 7

cameras:
  - id: "camera-1"
    name: "Главная камера"
    rtsp_url: "rtsp://admin:password@192.168.1.100:554/stream1"

recording:
  enabled: true
  segment_duration: 300s

notifier:
  max:
    enabled: false  # set true to enable Telegram notifications
    bot_token: "${MAX_BOT_TOKEN}"
    chat_id: "${MAX_CHAT_ID}"
```

**Чувствительные данные** храните в `/etc/videoguard/secrets.env`:

```bash
MAX_BOT_TOKEN=your_bot_token_here
MAX_CHAT_ID=123456789
```

### 5. Настройка systemd

```bash
# Копировать сервис-файл
sudo cp deployments/systemd/videoguard.service /etc/systemd/system/

# Перезагрузить daemon
sudo systemctl daemon-reload

# Включить автозапуск
sudo systemctl enable videoguard

# Запустить
sudo systemctl start videoguard

# Проверить статус
sudo systemctl status videoguard
```

**Файл сервиса** (`videoguard.service`):

```ini
[Unit]
Description=VideoGuard - Lightweight CCTV System
After=network.target

[Service]
Type=simple
User=username
Group=username
ExecStart=/opt/videoguard/videoguard --config /etc/videoguard/config.yaml
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=videoguard

[Install]
WantedBy=multi-user.target
```

### 6. Проверка

```bash
# Проверить сервис
sudo systemctl status videoguard

# Проверить health endpoint
curl http://localhost:8080/api/v1/health

# Проверить логи
journalctl -u videoguard -f

# Проверить валидацию конфигурации
/opt/videoguard/videoguard --config /etc/videoguard/config.yaml --validate-config
```

Ожидаемый ответ health:
```json
{
  "status": "ok",
  "timestamp": "2026-09-21T10:00:00Z",
  "components": {
    "application": {"status": "ok"},
    "camera": {"status": "online", "online": 1, "offline": 0},
    "recorder": {"status": "recording"},
    "storage": {"status": "ok"},
    "notifier": {"status": "disabled"},
    "network": {"status": "online"}
  }
}
```

### 7. Обновление

```bash
# На сервере
cd /path/to/videoguard

# Собрать новую версию
make release

# Развернуть
sudo ./scripts/deploy.sh
```

Скрипт deploy.sh автоматически:
1. Делает `git pull`
2. Валидирует конфигурацию
3. Запускает тесты
4. Собирает бинарник
5. Сохраняет бэкап
6. Заменяет бинарник
7. Перезапускает сервис
8. Проверяет health endpoint
9. Откатывает при ошибке

---

## Локальная разработка

```bash
# Собрать
go build -o videoguard ./cmd/videoguard

# Запустить локально (в режиме разработки, без камеры)
./videoguard --config configs/config.example.yaml --dev

# Валидация конфигурации
./videoguard --config configs/config.example.yaml --validate-config

# Показать версию
./videoguard --version
```

---

## Конфигурация

Полный пример: `configs/config.example.yaml`

### Структура конфига

| Секция | Параметры | Описание |
|--------|-----------|----------|
| `server` | listen, read_timeout, write_timeout | HTTP сервер |
| `storage` | root, retention_days, emergency_threshold_percent | Хранилище |
| `cameras[]` | id, name, rtsp_url, username, password | Камеры |
| `recording` | enabled, segment_duration, codec_copy | Запись |
| `events` | enabled, pre_record_seconds, post_record_seconds | События |
| `notifier.max` | enabled, bot_token, chat_id, retry_max, retry_delay | Telegram |
| `motion` | enabled, webhook_url | Motion детектор |
| `logging` | level | Уровень логов |

---

## Веб-интерфейс

Доступен по адресу: `http://<ip-сервера>:8080`

| Страница | Описание |
|----------|----------|
| `/` | Главная (статус системы) |
| `/live` | Просмотр камеры в реальном времени |
| `/events` | Список событий |
| `/archive` | Архив записей |
| `/settings` | Настройки (только чтение) |

---

## API Endpoints

| Метод | Путь | Описание |
|--------|------|----------|
| GET | `/api/v1/health` | Проверка работоспособности |
| GET | `/api/v1/diagnostics` | Полная диагностика системы |
| GET | `/api/v1/status` | Статус системы |
| GET | `/api/v1/cameras` | Список камер |
| GET | `/api/v1/events` | Список событий |
| POST | `/api/v1/events` | Создать событие |
| GET | `/api/v1/storage` | Информация о хранилище |

---

## Хранение и очистка

Автоматическая очистка старых записей:

```yaml
storage:
  retention_days: 7
  emergency_threshold_percent: 10  # критично при < 10% свободно
  aggressive_threshold_percent: 5  # агрессивно при < 5% свободно
```

Структура данных:
```
/srv/videoguard-data/
├── recordings/
│   └── camera-1/
│       └── 2026/
│           └── 09/
│               └── 21/
│                   ├── seg_001.mp4
│                   ├── seg_002.mp4
│                   └── ...
├── events/
├── snapshots/
├── tmp/
└── events.db
```

---

## Troubleshooting

### FFmpeg не запускается

```bash
# Проверить что FFmpeg установлен
which ffmpeg
ffmpeg -version

# Проверить доступность RTSP потока
ffmpeg -rtsp_transport tcp -i "rtsp://camera-ip:554/stream" -t 5 -f null -

# Проверить логи
journalctl -u videoguard -n 50 --no-pager

# Проверить stderr FFmpeg в логах videoguard
journalctl -u videoguard | grep "FFmpeg stderr"
```

**Частые ошибки:**
- `ffmpeg: command not found` — установить: `sudo apt install ffmpeg`
- `RTSP transport error` — проверить сеть, firewall, URL камеры
- `Permission denied` — проверить права на `/srv/videoguard-data`

### Motion не работает

```bash
# Проверить статус
sudo systemctl status motion

# Проверить конфиг
sudo cat /etc/motion/motion.conf

# Проверить веб-интерфейс Motion
curl http://localhost:8081
```

### RTSP камера недоступна

```bash
# Проверить подключение к камере
ffprobe -timeout 5000000 -i "rtsp://camera-ip:554/stream" -f null -

# Проверить сеть
ping camera-ip
nc -zv camera-ip 554

# Проверить логи камер
journalctl -u videoguard | grep "Camera"
```

### SQLite проблемы

```bash
# Проверить файл БД
ls -la /srv/videoguard-data/events.db

# Проверить целостность
sqlite3 /srv/videoguard-data/events.db "PRAGMA integrity_check;"

# Ручной VACUUM (медленно, только при необходимости)
sqlite3 /srv/videoguard-data/events.db "VACUUM;"
```

### Диск заполнен

```bash
# Проверить использование
df -h /srv/videoguard-data

# Найти большие файлы
du -sh /srv/videoguard-data/*/
du -sh /srv/videoguard-data/recordings/*/*/

# Ручная очистка старых сегментов
find /srv/videoguard-data/recordings -name "*.mp4" -mtime +7 -delete
```

### Сервис не запускается

```bash
# Проверить логи
journalctl -u videoguard -n 100 --no-pager

# Проверить конфигурацию
/opt/videoguard/videoguard --config /etc/videoguard/config.yaml --validate-config

# Проверить health
curl http://localhost:8080/api/v1/health

# Проверить diagnostics
curl http://localhost:8080/api/v1/diagnostics | python3 -m json.tool

# Ручной запуск для диагностики
/opt/videoguard/videoguard --config /etc/videoguard/config.yaml

# Проверить права
ls -la /opt/videoguard/
ls -la /srv/videoguard-data/
ls -la /etc/videoguard/
```

### Health endpoint возвращает degraded

```bash
# Проверить детали
curl http://localhost:8080/api/v1/health | python3 -m json.tool

# Проверить компоненты
curl http://localhost:8080/api/v1/diagnostics | python3 -m json.tool

# Проверить камеры
journalctl -u videoguard | grep -i camera

# Перезапустить сервис
sudo systemctl restart videoguard
```

### Rollback

Если автоматический rollback не сработал:

```bash
# Остановить сервис
sudo systemctl stop videoguard

# Восстановить бинарник
sudo cp /opt/videoguard/videoguard.backup /opt/videoguard/videoguard
sudo chmod +x /opt/videoguard/videoguard

# Запустить
sudo systemctl start videoguard

# Проверить
sudo systemctl status videoguard
curl http://localhost:8080/api/v1/health
```

---

## Безопасность

- Не открывайте порт 8080 в Интернет
- Для удалённого доступа используйте VPN/mesh-сеть (Tailscale, WireGuard)
- Сервер работает через 4G-модем с CGNAT — публичный IP не требуется
- Пароли камер и токены бота хранятся в `secrets.env`

---

## Ограничения

- Целевая платформа: linux/386, слабый CPU, 2 ГБ RAM
- Без GPU-ускорения
- Без ML/детекции (используется Motion)
- Без Docker
- Чистый Go driver для SQLite (без CGO)

---

## Команды Makefile

```bash
make build          # Собрать бинарник
make test           # Запустить тесты
make vet            # Проверить код (go vet)
make fmt            # Исправить форматирование
make validate       # Проверить код и форматирование
make release        # Полный цикл: fmt + vet + test + build (linux/386)
make clean          # Очистить build artifacts
make deploy         # Развернуть на сервере
```
