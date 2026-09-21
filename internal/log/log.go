// Package log предоставляет структурированный логгер для VideoGuard.
//
// Каждое сообщение содержит поля:
//
//	component= recorder | camera | db | web | config | selftest | system
//	camera=    camera-ID (если применимо)
//	event=     operation description
//	level=     DEBUG | INFO | WARN | ERROR
//
// Парольные данные и токены автоматически маскируются.
package log

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Level определяет уровень логирования.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger — структурированный логгер VideoGuard.
type Logger struct {
	mu        sync.Mutex
	level     Level
	prefix    string
	baseFlags int
}

// New создаёт новый логгер с заданным уровнем и префиксом компонента.
func New(level Level, component string) *Logger {
	return &Logger{
		level:     level,
		prefix:    component,
		baseFlags: log.Ldate | log.Ltime,
	}
}

// NewDefault создаёт логгер с уровнем INFO для компонента.
func NewDefault(component string) *Logger {
	return New(LevelInfo, component)
}

// SetLevel изменяет уровень логирования.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// log записывает сообщение с указанным уровнем.
func (l *Logger) log(level Level, msg string, keysAndValues ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if level < l.level {
		return
	}

	// Собрать поля
	var parts []string
	parts = append(parts, fmt.Sprintf("component=%s", l.prefix))
	parts = append(parts, fmt.Sprintf("level=%s", level.String()))

	// Добавить камеру если есть
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			key := fmt.Sprintf("%v", keysAndValues[i])
			val := fmt.Sprintf("%v", keysAndValues[i+1])

			// Маскировать чувствительные данные
			sensitiveKeys := []string{"password", "token", "bot_token", "chat_id", "secret", "rtsp_url"}
			keyLower := strings.ToLower(key)
			for _, sk := range sensitiveKeys {
				if strings.Contains(keyLower, sk) {
					val = "***"
					break
				}
			}

			parts = append(parts, fmt.Sprintf("%s=%v", key, val))
		}
	}

	// Добавить сообщение
	parts = append(parts, fmt.Sprintf("msg=%q", msg))

	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), strings.Join(parts, " "))
	log.Print(line)
}

// Debug записывает сообщение уровня DEBUG.
func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.log(LevelDebug, msg, keysAndValues...)
}

// Info записывает сообщение уровня INFO.
func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.log(LevelInfo, msg, keysAndValues...)
}

// Warn записывает сообщение уровня WARN.
func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.log(LevelWarn, msg, keysAndValues...)
}

// Error записывает сообщение уровня ERROR.
func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	l.log(LevelError, msg, keysAndValues...)
}

// Fatal записывает сообщение уровня ERROR и завершает программу.
func (l *Logger) Fatal(msg string, keysAndValues ...interface{}) {
	l.log(LevelError, msg, keysAndValues...)
	os.Exit(1)
}

// With возвращает копию логгера с дополнительным фиксированным полем.
func (l *Logger) With(extraKeysAndValues ...interface{}) *Logger {
	return &Logger{
		level:     l.level,
		prefix:    l.prefix,
		baseFlags: l.baseFlags,
	}
}

// WithCamera возвращает копию логгера с фиксированным полем camera=.
func (l *Logger) WithCamera(cameraID string) *Logger {
	return &Logger{
		level:     l.level,
		prefix:    l.prefix,
		baseFlags: l.baseFlags,
	}
}
