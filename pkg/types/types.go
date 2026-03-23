package types

import (
	"sync"
	"time"
)

// Config — основная конфигурация приложения
type Config struct {
	OnlyKnownDevices   bool                    `yaml:"only_known_devices" json:"only_known_devices"`
	ScanTimeout        int                     `yaml:"scanTimeout" json:"scanTimeout"`
	ScanInterval       int                     `yaml:"scanInterval" json:"scanInterval"`
	Retain             bool                    `yaml:"retain" json:"retain"`
	KnownDevices       map[string]KnownDevice  `yaml:"known_devices" json:"known_devices"`
	MinRSSI            int                     `yaml:"min_rssi" json:"min_rssi"`
	BLETimeout         int                     `yaml:"ble_timeout" json:"ble_timeout"`
	PresenceTimeout    int                     `yaml:"presence_timeout" json:"presence_timeout"`
	MaxConnections     int                     `yaml:"max_connections" json:"max_connections"`
	ConnectionTimeout  int                     `yaml:"connection_timeout" json:"connection_timeout"`
	HistoryPath        string                  `yaml:"history_path" json:"history_path"`
	AdvertisedServices map[string]ServiceInfo  `yaml:"advertised_services" json:"advertised_services"`
	HTTPPort           int                     `yaml:"http_port" json:"http_port"`
	HTTPProxy          bool                    `yaml:"http_proxy" json:"http_proxy"`
	HTTPWhitelist      []string                `yaml:"http_whitelist" json:"http_whitelist"`
	MQTTHost           string                  `yaml:"mqtt_host" json:"mqtt_host"`
	MQTT               MQTTConfig              `yaml:"mqtt" json:"mqtt"`
	MQTTOptions        map[string]interface{}  `yaml:"mqtt_options" json:"mqtt_options"`
	MQTTPrefix         string                  `yaml:"mqtt_prefix" json:"mqtt_prefix"`
	MQTTAdvertise      bool                    `yaml:"mqtt_advertise" json:"mqtt_advertise"`
	MQTTAdvertiseManufacturerData bool         `yaml:"mqtt_advertise_manufacturer_data" json:"mqtt_advertise_manufacturer_data"`
	MQTTAdvertiseServiceData bool             `yaml:"mqtt_advertise_service_data" json:"mqtt_advertise_service_data"`
	MQTTFormatJSON     bool                    `yaml:"mqtt_format_json" json:"mqtt_format_json"`
	MQTTFormatDecodedKeyTopic bool            `yaml:"mqtt_format_decoded_key_topic" json:"mqtt_format_decoded_key_topic"`
	HomeAssistant      bool                    `yaml:"homeassistant" json:"homeassistant"`
	HOMEd              bool                    `yaml:"homed" json:"homed"`
	BLE                BLEConfig               `yaml:"ble" json:"ble"`
	Publish            PublishConfig           `yaml:"publish" json:"publish"`
	Discovery          DiscoveryConfig         `yaml:"discovery" json:"discovery"`
	History            HistoryConfig           `yaml:"history" json:"history"`
	Web                WebConfig               `yaml:"web" json:"web"`
	Log                LogConfig               `yaml:"log" json:"log"`
}

// KnownDevice — информация об известном устройстве
type KnownDevice struct {
	Name             string                 `yaml:"name" json:"name"`
	HOMEdCloud       bool                   `yaml:"homed_cloud" json:"homed_cloud"`
	HOMEdDiscovery   bool                   `yaml:"homed_discovery" json:"homed_discovery"`
	MinRSSI          int                    `yaml:"min_rssi" json:"min_rssi"`
	CacheState       bool                   `yaml:"cache_state" json:"cache_state"`
	BindKey          string                 `yaml:"bind_key" json:"bind_key"`
	PresenceTimeout  int                    `yaml:"presence_timeout" json:"presence_timeout"`
	Model            string                 `yaml:"model" json:"model"`
}

// ServiceInfo — информация о сервисе
type ServiceInfo struct {
	Name string `yaml:"name" json:"name"`
}

// MQTTConfig — настройки MQTT брокера
type MQTTConfig struct {
	Broker   string `yaml:"broker" json:"broker"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
	ClientID string `yaml:"client_id" json:"client_id"`
	QoS      byte   `yaml:"qos" json:"qos"`
}

// BLEConfig — настройки BLE сканера
type BLEConfig struct {
	Adapter      string   `yaml:"adapter" json:"adapter"`
	ScanTimeout  string   `yaml:"scan_timeout" json:"scan_timeout"`
	ScanInterval int      `yaml:"scanInterval" json:"scanInterval"`   // длительность одного сканирования (секунды)
	RestartPause int      `yaml:"scanTimeout" json:"scanTimeout"`     // пауза между циклами сканирования (секунды)
	FilterMACs   []string `yaml:"filter_macs" json:"filter_macs"`
	Connect      bool     `yaml:"connect" json:"connect"` // Подключаться для GATT операций
}

// PublishConfig — настройки публикации
type PublishConfig struct {
	Mode           string `yaml:"mode" json:"mode"`                       // homeassistant, homed, both
	BasePrefix     string `yaml:"base_prefix" json:"base_prefix"`         // базовый префикс топиков
	RetainPresence bool   `yaml:"retain_presence" json:"retain_presence"` // retain для presence
}

// DiscoveryConfig — настройки Home Assistant MQTT Discovery
type DiscoveryConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Prefix  string `yaml:"prefix" json:"prefix"` // homeassistant
}

// HistoryConfig — настройки истории значений
type HistoryConfig struct {
	Enabled   bool     `yaml:"enabled" json:"enabled"`
	Intervals []string `yaml:"intervals" json:"intervals"` // 1m, 10m, 1h, 24h, 7d
}

// WebConfig — настройки веб-сервера
type WebConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	Port    int  `yaml:"port" json:"port"`
}

// LogConfig — настройки логирования
type LogConfig struct {
	Level string `yaml:"level" json:"level"` // debug, info, warn, error
}

// Device — информация об устройстве
type Device struct {
	MAC          string                 `json:"mac"`
	Name         string                 `json:"name,omitempty"`
	RSSI         int                    `json:"rssi"`
	LastSeen     time.Time              `json:"last_seen"`
	Online       bool                   `json:"online"`
	Battery      *int                   `json:"battery,omitempty"`
	Manufacturer map[string]interface{} `json:"manufacturer,omitempty"`
	ServiceData  map[string]interface{} `json:"service_data,omitempty"`
	ParsedValues map[string]ParsedValue `json:"parsed_values,omitempty"`

	// Внутренние поля
	mu             sync.RWMutex
	historyBuffers map[string]*HistoryRing
}

// ParsedValue — распарсенное значение из advertising
type ParsedValue struct {
	Value     interface{} `json:"value"`
	Unit      string      `json:"unit,omitempty"`
	Type      string      `json:"type"`   // temp, humidity, battery, pressure, etc.
	Source    string      `json:"source"` // manufacturer, service_data, gatt
	Timestamp time.Time   `json:"timestamp"`
}

// Advertisement — данные из BLE advertising
type Advertisement struct {
	Addr         string
	Name         string
	RSSI         int
	Manufacturer []byte
	ServiceData  []ServiceData
	Services     []string
}

// ServiceData — данные сервиса из advertising
type ServiceData struct {
	UUID string
	Data []byte
}

// Command — команда, полученная через MQTT
type Command struct {
	Type    string // write, read, notify, ping
	MAC     string
	Service string
	Char    string
	Payload []byte
	Topic   string
}

// HistoryPoint — точка данных для истории
type HistoryPoint struct {
	Value     float64
	Timestamp time.Time
}

// HistoryRing — кольцевой буфер для хранения истории
type HistoryRing struct {
	Points []HistoryPoint
	Size   int
	Index  int
	Full   bool
	mu     sync.Mutex
}

// DeviceExpose — информация для топика expose в HOMEd стиле
type DeviceExpose struct {
	Type     string   `json:"type"` // sensor, binary_sensor, switch
	Name     string   `json:"name"`
	Property string   `json:"property"` // temp, humidity, battery
	Unit     string   `json:"unit,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Values   []string `json:"values,omitempty"` // для enum типов
}

// NewDevice — создание нового устройства
func NewDevice(mac string) *Device {
	return &Device{
		MAC:            mac,
		LastSeen:       time.Now(),
		Online:         true,
		ParsedValues:   make(map[string]ParsedValue),
		historyBuffers: make(map[string]*HistoryRing),
	}
}

// UpdateLastSeen — обновить время последнего обнаружения
func (d *Device) UpdateLastSeen() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.LastSeen = time.Now()
	d.Online = true
}

// SetParsedValue — установить распарсенное значение
func (d *Device) SetParsedValue(key string, value ParsedValue) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ParsedValues[key] = value
}

// GetParsedValue — получить распарсенное значение
func (d *Device) GetParsedValue(key string) (ParsedValue, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	val, ok := d.ParsedValues[key]
	return val, ok
}

// GetParsedValues — получить все распарсенные значения (копию)
func (d *Device) GetParsedValues() map[string]ParsedValue {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make(map[string]ParsedValue, len(d.ParsedValues))
	for k, v := range d.ParsedValues {
		result[k] = v
	}
	return result
}

// GetFDFlat — получить плоский JSON для топика fd (HOMEd стиль)
func (d *Device) GetFDFlat() map[string]interface{} {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make(map[string]interface{})
	result["rssi"] = d.RSSI

	if d.Battery != nil {
		result["battery"] = *d.Battery
	}

	for key, val := range d.ParsedValues {
		if val.Value != nil {
			result[key] = val.Value
		}
	}

	return result
}

// GetExposeList — получить список expose для топика expose (HOMEd стиль)
func (d *Device) GetExposeList() []DeviceExpose {
	d.mu.RLock()
	defer d.mu.RUnlock()

	exposes := []DeviceExpose{
		{
			Type:     "sensor",
			Name:     "RSSI",
			Property: "rssi",
			Unit:     "dBm",
		},
	}

	if d.Battery != nil {
		exposes = append(exposes, DeviceExpose{
			Type:     "sensor",
			Name:     "Battery",
			Property: "battery",
			Unit:     "%",
			Min:      floatPtr(0),
			Max:      floatPtr(100),
		})
	}

	for key, val := range d.ParsedValues {
		expose := DeviceExpose{
			Type:     "sensor",
			Name:     key,
			Property: key,
			Unit:     val.Unit,
		}

		switch val.Type {
		case "temp":
			expose.Name = "Temperature"
			if expose.Unit == "" {
				expose.Unit = "°C"
			}
		case "humidity":
			expose.Name = "Humidity"
			if expose.Unit == "" {
				expose.Unit = "%"
			}
			expose.Min = floatPtr(0)
			expose.Max = floatPtr(100)
		case "pressure":
			expose.Name = "Pressure"
			if expose.Unit == "" {
				expose.Unit = "hPa"
			}
		case "illuminance":
			expose.Name = "Illuminance"
			if expose.Unit == "" {
				expose.Unit = "lx"
			}
		}

		exposes = append(exposes, expose)
	}

	return exposes
}

func floatPtr(f float64) *float64 {
	return &f
}
