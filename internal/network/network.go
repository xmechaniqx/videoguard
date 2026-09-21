// Package network предоставляет мониторинг сетевых подключений и модемов.
//
// Пакет не управляет модемом, а только:
// - Видит, есть интернет или нет
// - Генерирует события network_lost/network_restored
// - Отдаёт статус веб-интерфейсу
//
// Поддерживает получение данных через ModemManager (если доступен).
package network

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"videoguard/internal/events"
)

// EventGenerator — интерфейс для генерации событий.
type EventGenerator interface {
	Create(event *events.Event) error
}

// ModemInfo содержит информацию о модеме.
type ModemInfo struct {
	Connected bool    `json:"connected"`
	IP        string  `json:"ip"`
	Operator  string  `json:"operator"`
	Signal    float64 `json:"signal"` // dBm
	Model     string  `json:"model"`
	IMSI      string  `json:"imsi"`
	SIMICCID  string  `json:"sim_iccid"`
	Device    string  `json:"device"`
}

// ConnectivityChecker проверяет наличие интернета.
type ConnectivityChecker struct {
	eventMgr  EventGenerator
	interval  time.Duration
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	lastState bool // true = connected
	mu        sync.RWMutex
}

// NewConnectivityChecker создаёт новый чекер подключений.
func NewConnectivityChecker(eventMgr EventGenerator) *ConnectivityChecker {
	return &ConnectivityChecker{
		eventMgr: eventMgr,
		interval: 30 * time.Second,
	}
}

// Start запускает мониторинг подключений.
func (c *ConnectivityChecker) Start(ctx context.Context) {
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.running = true

	// Сразу проверить
	c.check()

	// Запустить периодическую проверку
	go c.run()
}

// Stop останавливает мониторинг.
func (c *ConnectivityChecker) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.running = false
	log.Printf("[Network] ConnectivityChecker остановлен")
}

func (c *ConnectivityChecker) run() {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.check()
		}
	}
}

func (c *ConnectivityChecker) check() {
	connected := c.hasInternet()

	c.mu.Lock()
	wasConnected := c.lastState
	c.lastState = connected
	c.mu.Unlock()

	if connected && !wasConnected {
		log.Printf("[Network] Интернет восстановлен")
		c.eventMgr.Create(&events.Event{
			Type:     "network_restored",
			CameraID: "system",
			Source:   "system",
		})
		return
	}

	if !connected && wasConnected {
		log.Printf("[Network] Интернет потерян")
		c.eventMgr.Create(&events.Event{
			Type:     "network_lost",
			CameraID: "system",
			Source:   "system",
		})
		return
	}

	if connected {
		log.Printf("[Network] Интернет доступен")
	}
}

// hasInternet проверяет наличие интернета через ping и DNS.
func (c *ConnectivityChecker) hasInternet() bool {
	// Проверить ping
	if c.pingCheck() {
		return true
	}

	// Проверить DNS
	if c.dnsCheck() {
		return true
	}

	// Проверить TCP подключение
	if c.tcpCheck() {
		return true
	}

	return false
}

// pingCheck проверяет доступность через ping.
func (c *ConnectivityChecker) pingCheck() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", "2", "8.8.8.8")
	return cmd.Run() == nil
}

// dnsCheck проверяет разрешение DNS.
func (c *ConnectivityChecker) dnsCheck() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := net.DefaultResolver.LookupIPAddr(ctx, "google.com")
	return err == nil
}

// tcpCheck проверяет TCP подключение.
func (c *ConnectivityChecker) tcpCheck() bool {
	conn, err := net.DialTimeout("tcp", "8.8.8.8:53", 2*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	return true
}

// GetModemInfo возвращает информацию о модеме.
func GetModemInfo() ModemInfo {
	info := ModemInfo{}

	// Попытаться получить данные через ModemManager
	if mmInfo := getModemManagerInfo(); mmInfo != nil {
		return *mmInfo
	}

	// Попытаться через nmcli
	if nmInfo := getNMCLIInfo(); nmInfo != nil {
		return *nmInfo
	}

	// Попытаться через ip и ifconfig
	info = getBasicNetworkInfo()

	return info
}

// getModemManagerInfo получает данные через ModemManager D-Bus.
func getModemManagerInfo() *ModemInfo {
	// Проверить что ModemManager доступен
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "busctl", "list")
	output, err := cmd.Output()
	if err != nil || !strings.Contains(string(output), "ModemManager") {
		return nil
	}

	// Получить список модемов
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()

	cmd2 := exec.CommandContext(ctx2, "busctl", "call", "org.freedesktop.ModemManager1",
		"/org/freedesktop/ModemManager1",
		"org.freedesktop.DBus.ObjectManager",
		"GetManagedObjects")
	output2, err := cmd2.Output()
	if err != nil {
		return nil
	}

	// Парсить modem path
	modemPath := parseModemPath(string(output2))
	if modemPath == "" {
		return nil
	}

	// Получить свойства модема
	ctx3, cancel3 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel3()

	cmd3 := exec.CommandContext(ctx3, "busctl", "get-property",
		"org.freedesktop.ModemManager1",
		modemPath,
		"org.freedesktop.ModemManager1.Modem",
		"CurrentSettings")
	output3, err := cmd3.Output()
	if err != nil {
		return nil
	}

	info := &ModemInfo{}
	parseModemSettings(info, string(output3))

	return info
}

// getNMCLIInfo получает данные через nmcli.
func getNMCLIInfo() *ModemInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nmcli", "-t", "-f", "DEVICE,TYPE,STATE", "device")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	info := &ModemInfo{}
	parseNMCLIOutput(info, string(output))

	return info
}

// getBasicNetworkInfo получает базовую информацию о сети.
func getBasicNetworkInfo() ModemInfo {
	info := ModemInfo{}

	// Получить IP
	info.IP = getLocalIP()

	// Проверить connected
	info.Connected = info.IP != ""

	return info
}

// parseModemPath парсит путь модема из busctl output.
func parseModemPath(output string) string {
	re := regexp.MustCompile(`'/org/freedesktop/ModemManager1/Modem/(\d+)'`)
	matches := re.FindStringSubmatch(output)
	if len(matches) > 1 {
		return fmt.Sprintf("/org/freedesktop/ModemManager1/Modem/%s", matches[1])
	}
	return ""
}

// parseModemSettings парсит настройки модема.
func parseModemSettings(info *ModemInfo, settings string) {
	// Парсить operator, signal, model и т.д.
	if match := regexp.MustCompile(`operator:"([^"]+)"`).FindStringSubmatch(settings); len(match) > 1 {
		info.Operator = match[1]
	}
	if match := regexp.MustCompile(`model:"([^"]+)"`).FindStringSubmatch(settings); len(match) > 1 {
		info.Model = match[1]
	}
}

// parseNMCLIOutput парсит вывод nmcli.
func parseNMCLIOutput(info *ModemInfo, output string) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		parts := strings.Split(line, ":")
		if len(parts) >= 3 {
			device := parts[0]
			devType := parts[1]
			state := parts[2]

			if devType == "gsm" || devType == "cdma" {
				info.Connected = state == "connected"
				info.Device = device

				// Получить дополнительную информацию
				if info.Connected {
					getModemDetails(info, device)
				}
			}
		}
	}
}

// getLocalIP возвращает локальный IP адрес.
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}

	return ""
}

// getModemDetails получает детальную информацию о модеме.
func getModemDetails(info *ModemInfo, device string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Получить operator
	cmd := exec.CommandContext(ctx, "nmcli", "-t", "-f", "OPERATOR", "device", "gsm", "show", device)
	output, err := cmd.Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		if len(lines) > 0 && lines[0] != "" {
			info.Operator = lines[0]
		}
	}

	// Получить signal
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()

	cmd2 := exec.CommandContext(ctx2, "nmcli", "-t", "-f", "SIGNAL", "device", "gsm", "show", device)
	output2, err := cmd2.Output()
	if err == nil {
		var signal int
		fmt.Sscanf(strings.TrimSpace(string(output2)), "%d", &signal)
		info.Signal = float64(signal)
	}

	// Получить IP
	info.IP = getLocalIP()
}

// NetworkStatus содержит полную информацию о сети.
type NetworkStatus struct {
	Connected bool      `json:"connected"`
	IP        string    `json:"ip"`
	Modem     ModemInfo `json:"modem"`
	LastCheck time.Time `json:"last_check"`
}

// GetNetworkStatus возвращает полный статус сети.
func GetNetworkStatus() NetworkStatus {
	checker := &ConnectivityChecker{}
	status := NetworkStatus{
		Connected: checker.hasInternet(),
		IP:        getLocalIP(),
		Modem:     GetModemInfo(),
		LastCheck: time.Now(),
	}

	return status
}

// DeviceInfo содержит информацию о сетевом устройстве.
type DeviceInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	State string `json:"state"`
	IP    string `json:"ip"`
	MAC   string `json:"mac"`
	Speed int    `json:"speed"` // Mbps
}

// GetNetworkDevices возвращает список сетевых устройств.
func GetNetworkDevices() []DeviceInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ip", "-j", "addr", "show")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Парсить JSON output
	var devices []DeviceInfo
	// Упрощённый парсинг — в реальности использовать encoding/json
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "\"ifname\"") {
			// Извлечь имя устройства
			re := regexp.MustCompile(`"ifname":"([^"]+)"`)
			match := re.FindStringSubmatch(line)
			if len(match) > 1 {
				devices = append(devices, DeviceInfo{
					Name: match[1],
				})
			}
		}
	}

	return devices
}

// SignalQuality оценивает качество сигнала.
func SignalQuality(dbm float64) string {
	if dbm >= -50 {
		return "Отличное"
	} else if dbm >= -60 {
		return "Хорошее"
	} else if dbm >= -70 {
		return "Среднее"
	} else if dbm >= -80 {
		return "Слабое"
	}
	return "Критическое"
}
