#!/bin/bash
# ============================================================
# VideoGuard — Скрипт развёртывания (hardened)
# ============================================================
# Порядок:
#   1. git pull
#   2. validate config
#   3. run tests
#   4. build binary
#   5. сохранить предыдущий бинарник
#   6. заменить бинарник только после успешной сборки
#   7. restart service
#   8. дождаться /api/v1/health
#   9. rollback при ошибке
#
# Использование:
#   ./scripts/deploy.sh [branch]
#   ./scripts/deploy.sh main
#   ./scripts/deploy.sh (текущая ветка)
# ============================================================

set -euo pipefail

# Переменные
APP_NAME="videoguard"
BINARY_DIR="/opt/videoguard"
CONFIG_DIR="/etc/videoguard"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
SERVICE_NAME="videoguard"
TARGET_ARCH="386"
TARGET_OS="linux"
HEALTH_URL="http://localhost:8080/api/v1/health"
HEALTH_MAX_RETRIES=30
HEALTH_RETRY_INTERVAL=2
DEPLOY_LOG="/var/log/videoguard-deploy.log"

# Цвета для вывода
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Функции логирования
log_info() {
    local msg="[$(date '+%Y-%m-%d %H:%M:%S')] [INFO] $1"
    echo -e "${GREEN}${msg}${NC}"
    echo "$msg" >> "$DEPLOY_LOG" 2>/dev/null || true
}

log_warn() {
    local msg="[$(date '+%Y-%m-%d %H:%M:%S')] [WARN] $1"
    echo -e "${YELLOW}${msg}${NC}"
    echo "$msg" >> "$DEPLOY_LOG" 2>/dev/null || true
}

log_error() {
    local msg="[$(date '+%Y-%m-%d %H:%M:%S')] [ERROR] $1"
    echo -e "${RED}${msg}${NC}"
    echo "$msg" >> "$DEPLOY_LOG" 2>/dev/null || true
}

log_step() {
    local msg="[$(date '+%Y-%m-%d %H:%M:%S')] === $1 ==="
    echo -e "${BLUE}${msg}${NC}"
    echo "$msg" >> "$DEPLOY_LOG" 2>/dev/null || true
}

# ============================================================
# Rollback function
# ============================================================
rollback() {
    log_error "Развёртывание не удалось. Выполняется откат..."

    if [ -f "${BINARY_DIR}/${APP_NAME}.backup" ]; then
        log_info "Восстановление предыдущей версии..."
        sudo cp "${BINARY_DIR}/${APP_NAME}.backup" "${BINARY_DIR}/${APP_NAME}"
        sudo chmod +x "${BINARY_DIR}/${APP_NAME}"
        sudo systemctl restart "$SERVICE_NAME"
        log_info "Откат завершён. Сервис запущен с предыдущей версией."

        # Проверить что откатился
        sleep 5
        if sudo systemctl is-active --quiet "$SERVICE_NAME"; then
            log_info "Сервис после отката работает"
        else
            log_error "Сервис НЕ запустился после отката! Проверьте вручную."
        fi
    else
        log_error "Нет резервной копии для отката!"
    fi

    exit 1
}

# ============================================================
# Health check function
# ============================================================
wait_for_health() {
    log_info "Ожидание health endpoint (${HEALTH_MAX_RETRIES} попыток, интервал ${HEALTH_RETRY_INTERVAL}с)..."

    for i in $(seq 1 $HEALTH_MAX_RETRIES); do
        if curl -sf "$HEALTH_URL" | grep -q '"status"'; then
            log_info "Health check пройден (попытка $i)"
            return 0
        fi
        sleep $HEALTH_RETRY_INTERVAL
    done

    log_error "Health check не прошёл после $HEALTH_MAX_RETRIES попыток"
    return 1
}

# ============================================================
# Pre-flight checks
# ============================================================
log_step "PRE-FLIGHT CHECKS"

# Проверка прав
if [ "$EUID" -ne 0 ]; then
    log_error "Скрипт должен запускаться от root (sudo)"
    exit 1
fi

# Проверка что мы в репозитории
if [ ! -d ".git" ]; then
    log_error "Текущая директория не является git-репозиторием"
    exit 1
fi

# ============================================================
# 1. Git pull
# ============================================================
log_step "1. GIT PULL"

BRANCH="${1:-$(git branch --show-current)}"
if [ -z "$BRANCH" ]; then
    log_error "Не удалось определить ветку"
    exit 1
fi

log_info "Обновление ветки '$BRANCH'..."
if ! git pull origin "$BRANCH" 2>&1 | tee -a "$DEPLOY_LOG"; then
    log_error "git pull не удался"
    exit 1
fi

CURRENT_HASH=$(git rev-parse --short HEAD)
log_info "Текущий commit: $CURRENT_HASH"

# ============================================================
# 2. Validate config
# ============================================================
log_step "2. VALIDATE CONFIG"

# Проверить что конфиг существует
if [ ! -f "$CONFIG_FILE" ]; then
    log_error "Конфигурация не найдена: $CONFIG_FILE"
    exit 1
fi
log_info "Конфигурация найдена: $CONFIG_FILE"

# Проверить что бинарник существует (для --validate-config)
if [ ! -f "${BINARY_DIR}/${APP_NAME}" ]; then
    log_error "Бинарник не найден: ${BINARY_DIR}/${APP_NAME}. Сначала выполните сборку."
    exit 1
fi

# Валидация конфигурации
log_info "Валидация конфигурации..."
if ! "${BINARY_DIR}/${APP_NAME}" --config "$CONFIG_FILE" --validate-config 2>&1 | tee -a "$DEPLOY_LOG"; then
    log_error "Валидация конфигурации не пройдена"
    exit 1
fi
log_info "Конфигурация валидна"

# ============================================================
# 3. Run tests
# ============================================================
log_step "3. RUN TESTS"

log_info "Запуск тестов..."
if ! go test -v -race ./... 2>&1 | tee -a "$DEPLOY_LOG"; then
    log_error "Тесты не пройдены"
    exit 1
fi
log_info "Тесты пройдены"

# ============================================================
# 4. Build binary
# ============================================================
log_step "4. BUILD BINARY"

log_info "Кросс-компиляция для linux/386..."
if ! GOOS=linux GOARCH=386 CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.appVersion=${CURRENT_HASH}" \
    -o "${BINARY_DIR}/${APP_NAME}.new" \
    ./cmd/videoguard 2>&1 | tee -a "$DEPLOY_LOG"; then
    log_error "Сборка не удалась"
    exit 1
fi
log_info "Бинарник собран: ${BINARY_DIR}/${APP_NAME}.new"

# Проверить новый бинарник
log_info "Проверка нового бинарника..."
if ! "${BINARY_DIR}/${APP_NAME}.new" --version >/dev/null 2>&1; then
    log_error "Новый бинарник не запускается"
    rm -f "${BINARY_DIR}/${APP_NAME}.new"
    exit 1
fi

# ============================================================
# 5. Save previous binary
# ============================================================
log_step "5. SAVE BACKUP"

if [ -f "${BINARY_DIR}/${APP_NAME}" ]; then
    log_info "Сохранение текущей версии..."
    cp "${BINARY_DIR}/${APP_NAME}" "${BINARY_DIR}/${APP_NAME}.backup"
    log_info "Бэкап создан: ${BINARY_DIR}/${APP_NAME}.backup"
else
    log_warn "Текущий бинарник не найден (первый деплой)"
fi

# ============================================================
# 6. Replace binary
# ============================================================
log_step "6. REPLACE BINARY"

mv "${BINARY_DIR}/${APP_NAME}.new" "${BINARY_DIR}/${APP_NAME}"
chmod +x "${BINARY_DIR}/${APP_NAME}"
log_info "Бинарник заменён"

# ============================================================
# 7. Restart service
# ============================================================
log_step "7. RESTART SERVICE"

sudo systemctl restart "$SERVICE_NAME"
log_info "Сервис перезапущен"

# ============================================================
# 8. Wait for health
# ============================================================
log_step "8. HEALTH CHECK"

if ! wait_for_health; then
    log_error "Health check не прошёл"
    rollback
fi

# ============================================================
# 9. Final verification
# ============================================================
log_step "FINAL VERIFICATION"

# Проверить что сервис работает
if ! sudo systemctl is-active --quiet "$SERVICE_NAME"; then
    log_error "Сервис не работает!"
    rollback
fi

# Показать информацию о развёртывании
log_info "============================================================"
log_info "Развёртывание завершено успешно!"
log_info "============================================================"
log_info "Версия:   $CURRENT_HASH"
log_info "Бинарник: ${BINARY_DIR}/${APP_NAME}"
log_info "Веб-интерфейс: http://<server-ip>:8080"
log_info "Логи:     journalctl -u ${SERVICE_NAME} -f"
log_info "Статус:   sudo systemctl status ${SERVICE_NAME}"
log_info "Деплой-лог: $DEPLOY_LOG"
log_info "============================================================"

# Удалить бэкап через 10 минут (время на проверку)
log_info "Бэкап будет удалён через 10 минут..."
(sleep 600 && rm -f "${BINARY_DIR}/${APP_NAME}.backup") &
