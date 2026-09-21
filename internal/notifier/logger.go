// Package notifier предоставляет вспомогательный логгер.
package notifier

import (
	"fmt"
	"log"
	"strings"
)

// logger — упрощённый логгер для пакета notifier.
type logger struct{}

// newLogger создаёт новый логгер.
func newLogger() *logger {
	return &logger{}
}

// Info логирует информационное сообщение.
func (l *logger) Info(msg string, keysAndValues ...interface{}) {
	parts := []string{"[NOTIFIER]", "INFO", msg}
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			parts = append(parts, fmt.Sprintf("%v=%v", keysAndValues[i], keysAndValues[i+1]))
		}
	}
	log.Println(strings.Join(parts, " "))
}

// Warn логирует предупреждение.
func (l *logger) Warn(msg string, keysAndValues ...interface{}) {
	parts := []string{"[NOTIFIER]", "WARN", msg}
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			parts = append(parts, fmt.Sprintf("%v=%v", keysAndValues[i], keysAndValues[i+1]))
		}
	}
	log.Println(strings.Join(parts, " "))
}

// Error логирует ошибку.
func (l *logger) Error(msg string, keysAndValues ...interface{}) {
	parts := []string{"[NOTIFIER]", "ERROR", msg}
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			parts = append(parts, fmt.Sprintf("%v=%v", keysAndValues[i], keysAndValues[i+1]))
		}
	}
	log.Println(strings.Join(parts, " "))
}
