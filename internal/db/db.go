// Package db предоставляет слой работы с SQLite базой данных.
//
// Использует чистый Go driver (modernc.org/sqlite) без CGO.
// Поддерживает WAL режим для лучшей производительности.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB обёртка над sql.DB с дополнительными методами.
type DB struct {
	db *sql.DB
}

// Open открывает SQLite базу данных с оптимизированными PRAGMA.
func Open(path string) (*DB, error) {
	d, err := sql.Open("sqlite", path+"?_journal=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("открыть базу данных: %w", err)
	}

	db := &DB{db: d}

	// Настроить пул соединений
	db.db.SetMaxOpenConns(1) // SQLite не поддерживает конкурентные записи
	db.db.SetMaxIdleConns(1)
	db.db.SetConnMaxLifetime(0) // соединения не закрываются

	// Проверить соединение
	if err := db.db.Ping(); err != nil {
		return nil, fmt.Errorf("проверить соединение: %w", err)
	}

	// ============================================================
	// SQLite PRAGMA — hardening для круглосуточной работы
	// ============================================================

	// journal_mode=WAL — write-ahead logging (лучшая производительность, нет блокировок на чтение)
	if res := applyPragma(db.db, `PRAGMA journal_mode=WAL`); res != "" && res != "wal" {
		return nil, fmt.Errorf("journal_mode=WAL failed: %s", res)
	}

	// synchronous=NORMAL — баланс между безопасностью и скоростью
	// NORMAL достаточно для видеоархива (данные можно восстановить из FFmpeg сегментов)
	if res := applyPragma(db.db, `PRAGMA synchronous=NORMAL`); res != "ok" && res != "" {
		return nil, fmt.Errorf("synchronous=NORMAL failed: %s", res)
	}

	// foreign_keys=ON — целостность связей (notifications -> events)
	if res := applyPragma(db.db, `PRAGMA foreign_keys=ON`); res != "ok" && res != "" {
		return nil, fmt.Errorf("foreign_keys=ON failed: %s", res)
	}

	// busy_timeout=5000 — ждать 5 секунд при конкурентном доступе вместо ошибки
	applyPragma(db.db, `PRAGMA busy_timeout=5000`)

	// optimize — рекомендация SQLite для長期 работы
	applyPragma(db.db, `PRAGMA optimize`)

	return db, nil
}

// applyPragma выполняет PRAGMA и возвращает результат (для PRAGMA которые возвращают значение).
func applyPragma(d *sql.DB, pragma string) string {
	var result string
	err := d.QueryRow(pragma).Scan(&result)
	if err != nil {
		// Некоторые PRAGMA не возвращают значение через QueryRow
		d.Exec(pragma)
		return ""
	}
	return result
}

// Migrate применяет миграции схемы.
func (d *DB) Migrate() error {
	schema := `
	-- ============================================================
	-- Таблица событий (история motion, camera, system events)
	-- ============================================================
	CREATE TABLE IF NOT EXISTS events (
		id TEXT PRIMARY KEY,
		camera_id TEXT NOT NULL,
		type TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT 'system',
		started_at TEXT NOT NULL,
		ended_at TEXT,
		snapshot_path TEXT DEFAULT '',
		video_path TEXT DEFAULT '',
		metadata TEXT DEFAULT '{}'
	);

	CREATE INDEX IF NOT EXISTS idx_events_camera_id ON events(camera_id);
	CREATE INDEX IF NOT EXISTS idx_events_type ON events(type);
	CREATE INDEX IF NOT EXISTS idx_events_started_at ON events(started_at);
	CREATE INDEX IF NOT EXISTS idx_events_camera_type ON events(camera_id, type);

	-- ============================================================
	-- Таблица уведомлений (очередь отправки в MAX)
	-- ============================================================
	CREATE TABLE IF NOT EXISTS notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id TEXT NOT NULL,
		notifier TEXT NOT NULL DEFAULT 'max',
		status TEXT NOT NULL DEFAULT 'pending',
		created_at TEXT NOT NULL,
		sent_at TEXT,
		error TEXT DEFAULT '',
		retries INTEGER DEFAULT 0,
		FOREIGN KEY (event_id) REFERENCES events(id)
	);

	CREATE INDEX IF NOT EXISTS idx_notifications_status ON notifications(status);
	CREATE INDEX IF NOT EXISTS idx_notifications_event_id ON notifications(event_id);
	CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications(created_at);

	-- ============================================================
	-- Таблица состояния камер (последний статус, uptime, last_seen)
	-- ============================================================
	CREATE TABLE IF NOT EXISTS camera_state (
		camera_id TEXT PRIMARY KEY,
		name TEXT DEFAULT '',
		status TEXT NOT NULL DEFAULT 'unknown',
		uptime_seconds INTEGER DEFAULT 0,
		last_seen TEXT,
		reconnect_attempts INTEGER DEFAULT 0,
		last_error TEXT DEFAULT '',
		updated_at TEXT NOT NULL
	);
	`

	_, err := d.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("применить миграции: %w", err)
	}

	return nil
}

// Close закрывает соединение с базой данных.
func (d *DB) Close() error {
	return d.db.Close()
}

// DB возвращает underlying sql.DB для прямых запросов.
func (d *DB) DB() *sql.DB {
	return d.db
}

// Health проверяет работоспособность базы данных.
func (d *DB) Health() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.db.PingContext(ctx)
}

// AutoVacuum выполняет VACUUM только если удалено много записей.
// Вызывается из StorageWatchdog или при старте, если прошло > 24 часа.
func (d *DB) AutoVacuum(minRemoved int) (removed int, vacuumed bool, err error) {
	// Подсчитать удалённые события (ended_at не NULL)
	var eventCount, removedCount int
	err = d.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&eventCount)
	if err != nil {
		return 0, false, err
	}

	err = d.db.QueryRow(`SELECT COUNT(*) FROM events WHERE ended_at IS NOT NULL`).Scan(&removedCount)
	if err != nil {
		return 0, false, err
	}

	// VACUUM только если удалено больше minRemoved записей
	if removedCount < minRemoved {
		return removedCount, false, nil
	}

	// Выполнить VACUUM (может быть долгим, но только в редких случаях)
	_, err = d.db.Exec(`VACUUM`)
	if err != nil {
		return removedCount, false, err
	}

	return removedCount, true, nil
}
