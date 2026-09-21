// Package web предоставляет вспомогательный логгер для HTTP-сервера.
package web

import (
	"fmt"
	"log"
	"strings"
)

// logger — упрощённый логгер для HTTP-сервера.
type logger struct {
	level  int
	prefix string
}

// newLogger создаёт новый логгер с заданным уровнем.
func newLogger(level string) *logger {
	l := 1 // info по умолчанию
	switch level {
	case "debug":
		l = 0
	case "info":
		l = 1
	case "warn":
		l = 2
	case "error":
		l = 3
	}

	return &logger{
		level:  l,
		prefix: "[HTTP]",
	}
}

// Info логирует информационное сообщение.
func (l *logger) Info(msg string, keysAndValues ...interface{}) {
	if l.level > 1 {
		return
	}
	l.log("INFO", msg, keysAndValues...)
}

// Warn логирует предупреждение.
func (l *logger) Warn(msg string, keysAndValues ...interface{}) {
	if l.level > 2 {
		return
	}
	l.log("WARN", msg, keysAndValues...)
}

// Error логирует ошибку.
func (l *logger) Error(msg string, keysAndValues ...interface{}) {
	if l.level > 3 {
		return
	}
	l.log("ERROR", msg, keysAndValues...)
}

// log — внутренний метод логирования.
func (l *logger) log(level, msg string, keysAndValues ...interface{}) {
	parts := []string{l.prefix, level, msg}

	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			parts = append(parts, fmt.Sprintf("%v=%v", keysAndValues[i], keysAndValues[i+1]))
		}
	}

	log.Println(strings.Join(parts, " "))
}
