package system

import (
	"testing"
	"time"
)

// TestGetInfo проверяет получение информации о системе.
func TestGetInfo(t *testing.T) {
	info := GetInfo()

	if info.CPUCount <= 0 {
		t.Errorf("ожидалось CPUCount > 0, получено %d", info.CPUCount)
	}

	if info.MemoryUsed > info.MemoryTotal {
		t.Errorf("ожидалось MemoryUsed <= MemoryTotal: %d > %d", info.MemoryUsed, info.MemoryTotal)
	}

	if info.MemoryPercent < 0 || info.MemoryPercent > 100 {
		t.Errorf("ожидался MemoryPercent от 0 до 100, получено %f", info.MemoryPercent)
	}
}

// TestGetMemoryInfo проверяет получение информации о памяти.
func TestGetMemoryInfo(t *testing.T) {
	info := GetMemoryInfo()

	// На Linux должны быть поля MemTotal и MemFree
	if len(info) == 0 {
		t.Log("Внимание: /proc/meminfo недоступен (не Linux?)")
		return
	}

	// Проверить наличие ключевых полей
	if _, ok := info["MemTotal:"]; !ok {
		t.Log("Внимание: MemTotal не найден в /proc/meminfo")
	}
}

// TestSplitLines проверяет разбивку текста на строки.
func TestSplitLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "пустая строка",
			input: "",
			want:  []string{},
		},
		{
			name:  "одна строка",
			input: "hello",
			want:  []string{"hello"},
		},
		{
			name:  "две строки",
			input: "hello\nworld",
			want:  []string{"hello", "world"},
		},
		{
			name:  "три строки",
			input: "line1\nline2\nline3",
			want:  []string{"line1", "line2", "line3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("splitLines() длина = %d, want %d", len(got), len(tt.want))
				return
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("splitLines()[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// TestSplitFields проверяет разбивку строки на поля.
func TestSplitFields(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "пустая строка",
			input: "",
			want:  []string{},
		},
		{
			name:  "одно поле",
			input: "hello",
			want:  []string{"hello"},
		},
		{
			name:  "два поля через пробел",
			input: "hello world",
			want:  []string{"hello", "world"},
		},
		{
			name:  "два поля через табуляцию",
			input: "hello\tworld",
			want:  []string{"hello", "world"},
		},
		{
			name:  "три поля",
			input: "MemTotal:  1000000 kB",
			want:  []string{"MemTotal:", "1000000", "kB"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitFields(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("splitFields() длина = %d, want %d", len(got), len(tt.want))
				return
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("splitFields()[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// TestStartTime проверяет время запуска.
func TestStartTime(t *testing.T) {
	// startTime должен быть установлен при импорте пакета
	if startTime.IsZero() {
		t.Error("ожидалось, что startTime не нулевой")
	}
}

// TestGetInfoConsistency проверяет согласованность данных.
func TestGetInfoConsistency(t *testing.T) {
	info1 := GetInfo()
	time.Sleep(100 * time.Millisecond)
	info2 := GetInfo()

	// Uptime должен увеличиваться
	if info2.Uptime <= info1.Uptime {
		t.Error("ожидалось увеличение uptime")
	}

	// CPUCount должен оставаться постоянным
	if info2.CPUCount != info1.CPUCount {
		t.Errorf("CPUCount изменился: %d -> %d", info1.CPUCount, info2.CPUCount)
	}
}
