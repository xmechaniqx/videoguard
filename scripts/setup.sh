#!/bin/bash
# ============================================================
# VideoGuard — Скрипт начальной установки (Zero to Running)
# ============================================================
# Подготавливает сервер к запуску VideoGuard:
#   1. Установка зависимостей (ffmpeg, motion, Go)
#   2. Создание всех директорий
#   3. Копирование бинарника, конфига, systemd-сервиса
#   4. Настройка прав и secrets.env
#   5. Запуск systemd-сервиса
#
# Использование:
#   sudo ./scripts/setup.sh [путь/к/бинарнику]
#
# Если путь к бинарнику не указан — ищет:
#   ./build/videoguard-linux-386  (результат make release)
#   ./build/videoguard            (результат make build)
#
# После установки сервис доступен:
#   http://<server-ip>:8080
# ============================================================

set -euo pipefail

# ============================================================
# Константы
# ============================================================
APP_NAME="videoguard"
BINARY_DIR="/opt/videoguard"
DATA_DIR="/srv/videoguard-data"
CONFIG_DIR="/etc/videoguard"
LOG_DIR="/var/log/videoguard"
SERVICE_NAME="videoguard"
SERVICE_FILE="deployments/systemd/videoguard.service"
CONFIG_TEMPLATE="configs/config.example.yaml"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
SECRETS_FILE="${CONFIG_DIR}/secrets.env"
HEALTH_URL="http://localhost:8080/api/v1/health"
INSTALL_LOG="/var/log/videoguard-install.log"

# Пользователь сервиса
SERVICE_USER="username"
SERVICE_GROUP="username"

# Цвета
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ============================================================
# Утилиты
# ============================================================
log_info()  { echo -e "${GREEN}[INSTALL]${NC} $1" | tee -a "$INSTALL_LOG"; }
log_warn()  { echo -e "${YELLOW}[INSTALL]${NC} $1" | tee -a "$INSTALL_LOG"; }
log_error() { echo -e "${RED}[INSTALL]${NC} $1" | tee -a "$INSTALL_LOG"; }
log_step()  { echo -e "\n${BLUE}[INSTALL]${NC} === $1 ===" | tee -a "$INSTALL_LOG"; }
log_ask()   { echo -e "${CYAN}[INSTALL]${NC} $1" | tee -a "$INSTALL_LOG"; }

prompt() {
    local question="$1"
    local default="$2"
    local value

    if [ -n "$default" ]; then
        read -rp "$question [$default]: " value
        echo "${value:-$default}"
    else
        read -rp "$question: " value
        echo "$value"
    fi
}

ask_confirm() {
    local question="$1"
    local response

    read -rp "$question [Y/n]: " response
    [[ "$response" =~ ^[Nn]$ ]] && return 1
    return 0
}

# ============================================================
# Проверка прав
# ============================================================
if [ "$EUID" -ne 0 ]; then
    log_error "Скрипт должен запускаться от root (sudo)"
    exit 1
fi

# ============================================================
# Шаг 1: Установка зависимостей системы
# ============================================================
log_step "1. ЗАВИСИМОСТИ СИСТЕМЫ"

log_info "Обновление списков пакетов..."
apt update -y >> "$INSTALL_LOG" 2>&1

log_info "Обновление установленных пакетов..."
apt upgrade -y >> "$INSTALL_LOG" 2>&1

log_info "Установка ffmpeg..."
apt install -y ffmpeg >> "$INSTALL_LOG" 2>&1
log_info "ffmpeg: $(ffmpeg -version 2>/dev/null | head -1)"

log_info "Установка motion (детекция движения)..."
apt install -y motion >> "$INSTALL_LOG" 2>&1
log_info "motion установлен"

# Дополнительные утилиты
log_info "Установка дополнительных утилит..."
apt install -y curl jq netcat-openbsd >> "$INSTALL_LOG" 2>&1

log_info "Зависимости установлены"

# ============================================================
# Шаг 2: Установка Go
# ============================================================
log_step "2. GO"

if command -v go &>/dev/null; then
    log_info "Go уже установлен: $(go version)"
else
    log_info "Установка Go 1.22..."
    wget -q https://go.dev/dl/go1.22.5.linux-386.tar.gz
    tar -C /usr/local -xzf go1.22.5.linux-386.tar.gz
    rm go1.22.5.linux-386.tar.gz

    # Добавить в PATH
    echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
    echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/bash.bashrc
    export PATH=$PATH:/usr/local/go/bin

    log_info "Go установлен: $(go version)"
fi

# ============================================================
# Шаг 3: Поиск бинарника
# ============================================================
log_step "3. БИНАРНИК"

BINARY_SOURCE="${1:-}"

if [ -z "$BINARY_SOURCE" ]; then
    log_info "Бинарник не указан, ищем..."

    # Приоритет: кросс-компиляция → обычная сборка → локальный
    if [ -f "./build/${APP_NAME}-linux-386" ]; then
        BINARY_SOURCE="./build/${APP_NAME}-linux-386"
        log_info "Найден: $BINARY_SOURCE (кросс-компиляция)"
    elif [ -f "./build/${APP_NAME}" ]; then
        BINARY_SOURCE="./build/${APP_NAME}"
        log_info "Найден: $BINARY_SOURCE (локальная сборка)"
    elif [ -f "./${APP_NAME}" ]; then
        BINARY_SOURCE="./${APP_NAME}"
        log_info "Найден: $BINARY_SOURCE (корень проекта)"
    else
        log_error "Бинарник не найден!"
        log_info "Можно:"
        log_info "  1. Собрать: make release"
        log_info "  2. Передать путь: sudo $0 ./build/videoguard-linux-386"
        exit 1
    fi
fi

# ============================================================
# Шаг 4: Создание директорий
# ============================================================
log_step "4. ДИРЕКТОРИИ"

# Проверить что пользователь существует
if ! id -u "$SERVICE_USER" &>/dev/null; then
    log_warn "Пользователь '$SERVICE_USER' не найден"
    if ask_confirm "Создать пользователя '$SERVICE_USER'?"; then
        useradd -m -s /bin/bash "$SERVICE_USER" 2>> "$INSTALL_LOG" || true
        log_info "Пользователь '$SERVICE_USER' создан"
    else
        log_error "Невозможно продолжить без пользователя"
        exit 1
    fi
fi

log_info "Создание директорий..."
mkdir -p "$BINARY_DIR"
mkdir -p "$DATA_DIR"
mkdir -p "$CONFIG_DIR"
mkdir -p "$LOG_DIR"

# Структура данных
mkdir -p "$DATA_DIR/recordings"
mkdir -p "$DATA_DIR/events"
mkdir -p "$DATA_DIR/snapshots"
mkdir -p "$DATA_DIR/tmp"

log_info "Директории созданы:"
log_info "  Бинарник:   $BINARY_DIR"
log_info "  Данные:     $DATA_DIR"
log_info "  Конфиг:     $CONFIG_DIR"
log_info "  Логи:       $LOG_DIR"

# ============================================================
# Шаг 5: Копирование файлов
# ============================================================
log_step "5. КОПИРОВАНИЕ ФАЙЛОВ"

# Бинарник
log_info "Копирование бинарника..."
cp "$BINARY_SOURCE" "${BINARY_DIR}/${APP_NAME}"
chmod +x "${BINARY_DIR}/${APP_NAME}"
log_info "Бинарник установлен: ${BINARY_DIR}/${APP_NAME}"
log_info "Версия: $(${BINARY_DIR}/${APP_NAME} --version 2>/dev/null || echo 'N/A')"

# Конфигурация
if [ -f "$CONFIG_TEMPLATE" ]; then
    log_info "Копирование конфигурации..."
    cp "$CONFIG_TEMPLATE" "$CONFIG_FILE"
    log_info "Конфиг: $CONFIG_FILE"
else
    log_warn "Шаблон конфига не найден: $CONFIG_TEMPLATE"
fi

# Systemd сервис
if [ -f "$SERVICE_FILE" ]; then
    log_info "Установка systemd-сервиса..."
    cp "$SERVICE_FILE" "/etc/systemd/system/${SERVICE_NAME}.service"
    systemctl daemon-reload
    log_info "Сервис зарегистрирован"
else
    log_warn "Файл сервиса не найден: $SERVICE_FILE"
fi

# ============================================================
# Шаг 6: Настройка прав
# ============================================================
log_step "6. ПРАВА"

log_info "Настройка прав для пользователя '$SERVICE_USER'..."
chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "$BINARY_DIR"
chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "$DATA_DIR"
chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "$CONFIG_DIR"
chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "$LOG_DIR"

log_info "Права настроены"

# ============================================================
# Шаг 7: Настройка конфигурации (интерактивная)
# ============================================================
log_step "7. КОНФИГУРАЦИЯ"

log_info "Конфиг: $CONFIG_FILE"
log_info "Редактируйте вручную: sudo $EDITOR $CONFIG_FILE\n"

# Настройка secrets.env
if [ ! -f "$SECRETS_FILE" ]; then
    log_info "Создание secrets.env..."

    BOT_TOKEN=$(prompt "Telegram Bot Token (оставьте пустым для отключения)" "")
    CHAT_ID=$(prompt "Telegram Chat ID" "")

    if [ -n "$BOT_TOKEN" ] || [ -n "$CHAT_ID" ]; then
        cat > "$SECRETS_FILE" <<EOF
# VideoGuard — секреты
# Заменяйте значения на реальные
MAX_BOT_TOKEN=${BOT_TOKEN}
MAX_CHAT_ID=${CHAT_ID}
EOF
        chmod 600 "$SECRETS_FILE"
        log_info "secrets.env создан"
    else
        log_info "Telegram уведомления отключены"
    fi
else
    log_info "secrets.env уже существует"
fi

# Настройка камеры (если конфиг пустой)
if grep -q 'rtsp://USER:PASS@' "$CONFIG_FILE" 2>/dev/null; then
    log_warn "Конфигурация камеры не настроена!"

    CAMERA_RTSP=$(prompt "RTSP URL камеры (rtsp://user:pass@ip:554/stream)" "rtsp://admin:admin@192.168.1.100:554/stream1")
    CAMERA_NAME=$(prompt "Имя камеры" "camera-1")

    # Заменить в конфиге
    if command -v sed &>/dev/null; then
        sed -i "s|rtsp://USER:PASS@[^[:space:]]*|${CAMERA_RTSP}|g" "$CONFIG_FILE"
        sed -i "s|\"Main Camera\"|\"${CAMERA_NAME}\"|g" "$CONFIG_FILE"
        log_info "RTSP URL обновлён в конфиге"
    fi
else
    log_info "Камера уже настроена"
fi

# ============================================================
# Шаг 8: Systemd
# ============================================================
log_step "8. SYSTEMD"

if [ -f "/etc/systemd/system/${SERVICE_NAME}.service" ]; then
    log_info "Активация сервиса..."
    systemctl enable "$SERVICE_NAME"
    log_info "Автозапуск включён"

    if ask_confirm "Запустить сервис сейчас?"; then
        systemctl start "$SERVICE_NAME"
        log_info "Сервис запущен"
    else
        log_info "Запустите вручную: sudo systemctl start $SERVICE_NAME"
    fi
else
    log_warn "Systemd-сервис не установлен"
    log_info "Скопируйте вручную:"
    log_info "  sudo cp $SERVICE_FILE /etc/systemd/system/${SERVICE_NAME}.service"
    log_info "  sudo systemctl daemon-reload"
    log_info "  sudo systemctl enable $SERVICE_NAME"
    log_info "  sudo systemctl start $SERVICE_NAME"
fi

# ============================================================
# Шаг 9: Проверка
# ============================================================
log_step "9. ПРОВЕРКА"

sleep 3

# Проверить сервис
if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
    log_info "Сервис $SERVICE_NAME: ${GREEN}работает${NC}"
else
    log_warn "Сервис $SERVICE_NAME не запущен"
    log_info "Логи: journalctl -u $SERVICE_NAME -n 50 --no-pager"
fi

# Проверить health endpoint
if command -v curl &>/dev/null; then
    log_info "Проверка health endpoint..."
    if curl -sf "$HEALTH_URL" 2>/dev/null | jq . &>/dev/null; then
        log_info "Health endpoint доступен: ${GREEN}${HEALTH_URL}${NC}"
        curl -sf "$HEALTH_URL" 2>/dev/null | jq . | tee -a "$INSTALL_LOG"
    else
        log_warn "Health endpoint недоступен (ожидается через ~10с)"
    fi
fi

# Проверить диски
log_info "Использование диска:"
df -h "$DATA_DIR" | tee -a "$INSTALL_LOG"

# ============================================================
# Готово
# ============================================================
log_step "УСТАНОВКА ЗАВЕРШЕНА"

echo ""
echo -e "${GREEN}============================================================${NC}"
echo -e "${GREEN}VideoGuard установлен успешно!${NC}"
echo -e "${GREEN}============================================================${NC}"
echo ""
echo -e "  ${CYAN}Веб-интерфейс:${NC}  http://<server-ip>:8080"
echo -e "  ${CYAN}Бинарник:${NC}      ${BINARY_DIR}/${APP_NAME}"
echo -e "  ${CYAN}Конфиг:${NC}        ${CONFIG_FILE}"
echo -e "  ${CYAN}Секреты:${NC}       ${SECRETS_FILE}"
echo -e "  ${CYAN}Данные:${NC}        ${DATA_DIR}"
echo -e "  ${CYAN}Логи:${NC}          journalctl -u ${SERVICE_NAME} -f"
echo -e "  ${CYAN}Статус:${NC}        sudo systemctl status ${SERVICE_NAME}"
echo -e "  ${CYAN}Логи установки:${NC}  $INSTALL_LOG"
echo ""
echo -e "  ${YELLOW}Не забудьте настроить:${NC}"
echo -e "    1. $CONFIG_FILE  — RTSP URL камеры"
echo -e "    2. $SECRETS_FILE — Telegram Bot Token (опционально)"
echo ""
echo -e "  ${BLUE}Дальнейшие обновления:${NC}"
echo -e "    cd /path/to/videoguard && sudo ./scripts/deploy.sh"
echo ""
