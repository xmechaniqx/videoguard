# ============================================================
# VideoGuard — Makefile
# ============================================================
# Команды для сборки, тестирования и развёртывания.
# ============================================================

APP_NAME := videoguard
APP_DIR  := cmd/videoguard
CONFIG   := configs/config.example.yaml
BUILD_DIR := build

# Целевая платформа
GOOS   ?= linux
GOARCH ?= 386
CGO    ?= 0

# Версия
VERSION := 1.0.0
LDFLAGS := -ldflags "-s -w -X main.appVersion=$(VERSION)"

.PHONY: all build test clean run help \
        test-coverage test-race \
        cross-compile deploy \
        lint vet fmt \
        validate release

# Сборка по умолчанию
all: build

# ============================================================
# release — полный цикл проверки и сборки для production
# ============================================================
# Выполняет: go fmt, go vet, go test, GOARCH=386 build
# Падает при любой ошибке.
# ============================================================
release: fmt vet test cross-compile
	@echo "============================================================"
	@echo "Release build completed successfully"
	@echo "Binary: $(BUILD_DIR)/$(APP_NAME)-linux-386"
	@echo "============================================================"

# ============================================================
# validate — проверка конфигурации и кода
# ============================================================
validate: vet fmt-check
	@echo "Все проверки пройдены"

# Проверка форматирования (не исправляет, а проверяет)
fmt-check:
	@echo "Проверка форматирования..."
	@UNFORMATTED=$$(go fmt ./...); \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "Файлы не отформатированы:"; \
		echo "$$UNFORMATTED"; \
		echo "Запустите: make fmt"; \
		exit 1; \
	fi
	@echo "Форматирование OK"

# ============================================================
# build — сборка для текущей платформы
# ============================================================
build:
	@echo "Сборка $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO) go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME) ./$(APP_DIR)
	@echo "Готово: $(BUILD_DIR)/$(APP_NAME)"

# ============================================================
# cross-compile — кросс-компиляция для linux/386 (целевая платформа)
# ============================================================
cross-compile:
	@echo "Кросс-компиляция для linux/386..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=386 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-linux-386 ./$(APP_DIR)
	@echo "Готово: $(BUILD_DIR)/$(APP_NAME)-linux-386"

# Кросс-компиляция для linux/amd64 (для разработки)
cross-compile-amd64:
	@echo "Кросс-компиляция для linux/amd64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64 ./$(APP_DIR)
	@echo "Готово: $(BUILD_DIR)/$(APP_NAME)-linux-amd64"

# ============================================================
# test — запуск тестов
# ============================================================
test:
	@echo "Запуск тестов..."
	go test -v -race ./...

# Запустить тесты с покрытием
test-coverage:
	@echo "Запуск тестов с покрытием..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Отчёт о покрытии: coverage.html"

# Запустить тесты с детектором гонок
test-race:
	@echo "Запуск тестов с детектором гонок..."
	go test -race ./...

# ============================================================
# code quality
# ============================================================
vet:
	@echo "Проверка кода (go vet)..."
	go vet ./...

lint:
	@echo "Проверка форматирования..."
	@gofmt -l . | grep -v "^vendor/" || true

fmt:
	@echo "Форматирование кода..."
	go fmt ./...

# ============================================================
# run
# ============================================================
run:
	@echo "Запуск $(APP_NAME)..."
	go run ./$(APP_DIR) --config $(CONFIG) --dev

# Запустить с отладкой
run-debug:
	@echo "Запуск $(APP_NAME) в режиме отладки..."
	go run ./$(APP_DIR) --config $(CONFIG) --dev --log-level debug

# ============================================================
# clean
# ============================================================
clean:
	@echo "Очистка..."
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# ============================================================
# deploy
# ============================================================
deploy: build
	@echo "Развёртывание на сервере..."
	@./scripts/deploy.sh $(BUILD_DIR)/$(APP_NAME)-linux-386

# ============================================================
# help
# ============================================================
help:
	@echo "VideoGuard Makefile"
	@echo ""
	@echo "Доступные команды:"
	@echo "  make build          - Собрать бинарник"
	@echo "  make cross-compile  - Кросс-компиляция для linux/386"
	@echo "  make test           - Запустить тесты"
	@echo "  make vet            - Проверить код (go vet)"
	@echo "  make fmt            - Исправить форматирование"
	@echo "  make validate       - Проверить код и форматирование"
	@echo "  make release        - Полный цикл: fmt + vet + test + build (linux/386)"
	@echo "  make clean          - Очистить build artifacts"
	@echo "  make deploy         - Развернуть на сервере"
	@echo "  make help           - Показать эту справку"
