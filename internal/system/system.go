// Package system предоставляет функции для мониторинга системы.
//
// Собирает информацию о:
// - Использовании CPU
// - Использовании RAM
// - Использовании диска
// - Загрузке системы
package system

import (
	"os"
	"runtime"
	"time"
)

// Info содержит информацию о системе.
type Info struct {
	Uptime        time.Duration `json:"uptime"`
	CPUCount      int           `json:"cpu_count"`
	MemoryUsed    uint64        `json:"memory_used"`
	MemoryTotal   uint64        `json:"memory_total"`
	MemoryPercent float64       `json:"memory_percent"`
}

// GetInfo возвращает информацию о системе.
func GetInfo() Info {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return Info{
		Uptime:        time.Since(startTime),
		CPUCount:      runtime.NumCPU(),
		MemoryUsed:    m.Alloc,
		MemoryTotal:   m.TotalAlloc,
		MemoryPercent: float64(m.Alloc) / float64(m.TotalAlloc) * 100,
	}
}

// GetMemoryInfo возвращает информацию о памяти из /proc/meminfo.
func GetMemoryInfo() map[string]interface{} {
	info := make(map[string]interface{})

	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return info
	}

	// Парсить /proc/meminfo
	for _, line := range splitLines(string(data)) {
		parts := splitFields(line)
		if len(parts) >= 2 {
			key := parts[0]
			value := parts[1]
			info[key] = value
		}
	}

	return info
}

// splitLines разбивает текст на строки.
func splitLines(text string) []string {
	lines := []string{}
	start := 0
	for i, c := range text {
		if c == '\n' {
			lines = append(lines, text[start:i])
			start = i + 1
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// splitFields разбивает строку на поля.
func splitFields(line string) []string {
	fields := []string{}
	start := 0
	for i, c := range line {
		if c == ' ' || c == '\t' {
			if start < i {
				fields = append(fields, line[start:i])
			}
			start = i + 1
		}
	}
	if start < len(line) {
		fields = append(fields, line[start:])
	}
	return fields
}

// startTime отмечает время запуска пакета.
var startTime = time.Now()
