package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/avaksru/ble2homed/internal/mqtt"
	"github.com/avaksru/ble2homed/pkg/types"
	"github.com/rs/zerolog"
)

// Server — веб-сервер для мониторинга
type Server struct {
	config    *types.WebConfig
	publisher *mqtt.Publisher
	logger    zerolog.Logger
	server    *http.Server
}

// NewServer — создание нового веб-сервера
func NewServer(config *types.WebConfig, publisher *mqtt.Publisher, logger zerolog.Logger) *Server {
	return &Server{
		config:    config,
		publisher: publisher,
		logger:    logger.With().Str("component", "web").Logger(),
	}
}

// Start — запуск веб-сервера
func (s *Server) Start() error {
	if !s.config.Enabled {
		s.logger.Info().Msg("Web server disabled")
		return nil
	}

	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/device/", s.handleDevice)

	// Статические файлы (если нужны)
	mux.HandleFunc("/", s.handleIndex)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: mux,
	}

	s.logger.Info().Int("port", s.config.Port).Msg("Starting web server")

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error().Err(err).Msg("Web server error")
		}
	}()

	return nil
}

// Stop — остановка веб-сервера
func (s *Server) Stop() {
	if s.server != nil {
		s.logger.Info().Msg("Stopping web server")
		s.server.Close()
	}
}

// handleStatus — обработчик статуса
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	status := map[string]interface{}{
		"status":    "running",
		"timestamp": time.Now().Unix(),
	}

	json.NewEncoder(w).Encode(status)
}

// handleDevices — обработчик списка устройств
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	devices := s.publisher.GetAllDevices()

	type DeviceInfo struct {
		MAC          string                 `json:"mac"`
		Name         string                 `json:"name"`
		RSSI         int                    `json:"rssi"`
		LastSeen     time.Time              `json:"last_seen"`
		Online       bool                   `json:"online"`
		Battery      *int                   `json:"battery,omitempty"`
		ParsedValues map[string]interface{} `json:"parsed_values,omitempty"`
	}

	var deviceList []DeviceInfo
	for _, device := range devices {
		info := DeviceInfo{
			MAC:      device.MAC,
			Name:     device.Name,
			RSSI:     device.RSSI,
			LastSeen: device.LastSeen,
			Online:   device.Online,
			Battery:  device.Battery,
		}

		// Преобразуем parsed values
		parsedValues := device.GetParsedValues()
		if len(parsedValues) > 0 {
			info.ParsedValues = make(map[string]interface{})
			for key, val := range parsedValues {
				info.ParsedValues[key] = map[string]interface{}{
					"value": val.Value,
					"unit":  val.Unit,
					"type":  val.Type,
				}
			}
		}

		deviceList = append(deviceList, info)
	}

	json.NewEncoder(w).Encode(deviceList)
}

// handleDevice — обработчик конкретного устройства
func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Извлекаем MAC из URL
	mac := r.URL.Path[len("/api/device/"):]
	if mac == "" {
		http.Error(w, "MAC address required", http.StatusBadRequest)
		return
	}

	device, exists := s.publisher.GetDevice(mac)
	if !exists {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	info := map[string]interface{}{
		"mac":           device.MAC,
		"name":          device.Name,
		"rssi":          device.RSSI,
		"last_seen":     device.LastSeen,
		"online":        device.Online,
		"battery":       device.Battery,
		"fd_flat":       device.GetFDFlat(),
		"expose_list":   device.GetExposeList(),
		"parsed_values": device.GetParsedValues(),
	}

	json.NewEncoder(w).Encode(info)
}

// handleIndex — обработчик главной страницы
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	html := `<!DOCTYPE html>
<html>
<head>
    <title>ble2homed</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        h1 { color: #333; }
        .device { border: 1px solid #ddd; padding: 10px; margin: 10px 0; border-radius: 5px; }
        .device-header { font-weight: bold; margin-bottom: 5px; }
        .device-info { color: #666; font-size: 14px; }
        .online { color: green; }
        .offline { color: red; }
    </style>
</head>
<body>
    <h1>ble2homed - BLE to MQTT Bridge</h1>
    <p>Status: <span class="online">Running</span></p>
    <p>API Endpoints:</p>
    <ul>
        <li><a href="/api/status">/api/status</a> - Server status</li>
        <li><a href="/api/devices">/api/devices</a> - List all devices</li>
        <li>/api/device/{mac} - Get specific device</li>
    </ul>
    <div id="devices"></div>
    <script>
        fetch('/api/devices')
            .then(r => r.json())
            .then(devices => {
                const container = document.getElementById('devices');
                if (!devices || devices.length === 0) {
                    container.innerHTML = '<p>No devices discovered yet.</p>';
                    return;
                }
                container.innerHTML = '<h2>Discovered Devices</h2>' +
                    devices.map(d => ` + "`" + `
                        <div class="device">
                            <div class="device-header">${d.name || d.mac}</div>
                            <div class="device-info">
                                MAC: ${d.mac}<br>
                                RSSI: ${d.rssi} dBm<br>
                                Status: ${d.online ? '<span class="online">Online</span>' : '<span class="offline">Offline</span>'}<br>
                                Last seen: ${new Date(d.last_seen).toLocaleString()}
                            </div>
                        </div>
                    ` + "`" + `).join('');
            })
            .catch(err => console.error('Error loading devices:', err));
    </script>
</body>
</html>`

	w.Write([]byte(html))
}