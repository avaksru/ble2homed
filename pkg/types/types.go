package types

import (
	"strings"
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
	KnownDevicesOrder  []string                `yaml:"-" json:"-"`
	MinRSSI            int                     `yaml:"min_rssi" json:"min_rssi"`
	BLETimeout         int                     `yaml:"ble_timeout" json:"ble_timeout"`
	PresenceTimeout    int                     `yaml:"presence_timeout" json:"presence_timeout"`
	MaxConnections     int                     `yaml:"max_connections" json:"max_connections"`
	ConnectionTimeout  int                     `yaml:"connection_timeout" json:"connection_timeout"`
	HistoryPath        string                  `yaml:"history_path" json:"history_path"`
	DatabasePath       string                  `yaml:"database_path" json:"database_path"`
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
	HOMEd              bool                    `yaml:"homed" json:"homed"`
	BLE                BLEConfig               `yaml:"ble" json:"ble"`
	Publish            PublishConfig           `yaml:"publish" json:"publish"`
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
	ScanInterval int      `yaml:"scan_interval" json:"scan_interval"`   // длительность одного сканирования (секунды)
	RestartPause int      `yaml:"restart_pause" json:"restart_pause"`   // пауза между циклами сканирования (секунды)
	FilterMACs   []string `yaml:"filter_macs" json:"filter_macs"`
	Connect      bool     `yaml:"connect" json:"connect"` // Подключаться для GATT операций

	// Оптимизации производительности
	DisableEddystone        bool `yaml:"disable_eddystone" json:"disable_eddystone"`
	DisableIBeacon          bool `yaml:"disable_ibeacon" json:"disable_ibeacon"`
	DisableJSONParsing      bool `yaml:"disable_json_parsing" json:"disable_json_parsing"`
	DisableManufacturerRaw  bool `yaml:"disable_manufacturer_raw" json:"disable_manufacturer_raw"`
}

// PublishConfig — настройки публикации
type PublishConfig struct {
	BasePrefix     string `yaml:"base_prefix" json:"base_prefix"`         // базовый префикс топиков
	RetainPresence bool   `yaml:"retain_presence" json:"retain_presence"` // retain для presence
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
	Level      string `yaml:"level" json:"level"`           // debug, info, warn, error
	FilePath   string `yaml:"file_path" json:"file_path"`   // путь к файлу логов (пусто = только консоль)
	MaxSize    int    `yaml:"max_size" json:"max_size"`     // максимальный размер файла в МБ
	MaxBackups int    `yaml:"max_backups" json:"max_backups"` // количество бэкапов
	MaxAge     int    `yaml:"max_age" json:"max_age"`       // максимальный возраст файла в днях
	Compress   bool   `yaml:"compress" json:"compress"`     // сжимать старые логи
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
	Mu sync.RWMutex `json:"-"`

	// Флаг что expose уже был опубликован один раз (только для runtime)
	ExposePublished bool `json:"-"`
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

// HomedExpose — стандартный формат expose для всех устройств
type HomedExpose struct {
	Common ExposeCommon `json:"common"`
	Last   int64        `json:"last"`
}

// BleStatusDevice — информация об устройстве для топика status/ble
type BleStatusDevice struct {
	Active      bool              `json:"active"`
	Cloud       bool              `json:"cloud"`
	Discovery   bool              `json:"discovery"`
	Exposes     []string          `json:"exposes"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Options     map[string]ExposeOption `json:"options,omitempty"`
	Real        bool              `json:"real"`
	Last        int64             `json:"last"`
}

// BleStatus — информация обо всех устройствах для топика status/ble
type BleStatus struct {
	Devices    []BleStatusDevice `json:"devices"`
	Names      bool              `json:"names"`
	PermitJoin bool              `json:"permitJoin"`
	Timestamp  int64             `json:"timestamp"`
	Version    string            `json:"version"`
}

type ExposeCommon struct {
	Items   []string               `json:"items"`
	Options map[string]ExposeOption `json:"options"`
}

type ExposeOption struct {
	Class  string `json:"class,omitempty"`
	State  string `json:"state"`
	Type   string `json:"type"`
	Unit   string `json:"unit,omitempty"`
}

// NewDevice — создание нового устройства
func NewDevice(mac string) *Device {
	return &Device{
		MAC:          mac,
		LastSeen:     time.Now(),
		Online:       true,
		ParsedValues: make(map[string]ParsedValue),
	}
}

// UpdateLastSeen — обновить время последнего обнаружения
func (d *Device) UpdateLastSeen() {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	d.LastSeen = time.Now()
	d.Online = true
}

// GetLastSeen — получить время последнего обнаружения (потокобезопасно)
func (d *Device) GetLastSeen() time.Time {
	d.Mu.RLock()
	defer d.Mu.RUnlock()
	return d.LastSeen
}

// IsOnline — проверить онлайн статус (потокобезопасно)
func (d *Device) IsOnline() bool {
	d.Mu.RLock()
	defer d.Mu.RUnlock()
	return d.Online
}

// SetOnline — установить онлайн статус (потокобезопасно)
func (d *Device) SetOnline(online bool) {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	d.Online = online
}

// SetParsedValue — установить распарсенное значение
func (d *Device) SetParsedValue(key string, value ParsedValue) {
	d.Mu.Lock()
	defer d.Mu.Unlock()
	d.ParsedValues[key] = value
}

// GetParsedValue — получить распарсенное значение
func (d *Device) GetParsedValue(key string) (ParsedValue, bool) {
	d.Mu.RLock()
	defer d.Mu.RUnlock()
	val, ok := d.ParsedValues[key]
	return val, ok
}

// GetParsedValues — получить все распарсенные значения (копию)
func (d *Device) GetParsedValues() map[string]ParsedValue {
	d.Mu.RLock()
	defer d.Mu.RUnlock()
	result := make(map[string]ParsedValue, len(d.ParsedValues))
	for k, v := range d.ParsedValues {
		result[k] = v
	}
	return result
}

// GetFDFlat — получить плоский JSON для топика fd (HOMEd стиль)
func (d *Device) GetFDFlat() map[string]interface{} {
	d.Mu.RLock()
	defer d.Mu.RUnlock()

	result := make(map[string]interface{})
	result["rssi"] = d.RSSI
	result["last"] = d.LastSeen.Unix()

	if d.Battery != nil {
		result["battery"] = *d.Battery
	}

	for key, val := range d.ParsedValues {
		if val.Value != nil {
			if key == "temp" {
				result["temperature"] = val.Value
			} else {
				result[key] = val.Value
			}
		}
	}

	return result
}

// GetExposeList — получить список expose для топика expose (HOMEd стиль)
func (d *Device) GetExposeList() []DeviceExpose {
	d.Mu.RLock()
	defer d.Mu.RUnlock()

	// Фиксированный порядок полей (чтобы набор exposes не менялся местами)
	exposeOrder := []string{"temp", "humidity", "battery", "voltage", "pressure", "illuminance"}
	
	exposes := make([]DeviceExpose, 0, 1+len(exposeOrder))
	
	// RSSI всегда первый
	exposes = append(exposes, DeviceExpose{
		Type:     "sensor",
		Name:     "RSSI",
		Property: "rssi",
		Unit:     "dBm",
	})

	for _, key := range exposeOrder {
		if val, ok := d.ParsedValues[key]; ok {
			expose := DeviceExpose{
				Type:     "sensor",
				Name:     key,
				Property: key,
				Unit:     val.Unit,
			}

			switch val.Type {
			case "temp":
				expose.Name = "Temperature"
				expose.Property = "temperature"
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
			case "battery":
				expose.Name = "Battery"
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
	}

	return exposes
}

// GetHomedExpose — получить стандартный expose для HOMEd
func (d *Device) GetHomedExpose() HomedExpose {
	exposes := d.GetExposeList()
	items := make([]string, len(exposes))
	options := make(map[string]ExposeOption, len(exposes))

	for i, expose := range exposes {
		items[i] = expose.Property
		options[expose.Property] = ExposeOption{
			State: "measurement",
			Type:  "sensor",
			Unit:  expose.Unit,
		}
	}

	items = append(items, "last")

	return HomedExpose{
		Common: ExposeCommon{
			Items:   items,
			Options: options,
		},
		Last: d.LastSeen.Unix(),
	}
}

func floatPtr(f float64) *float64 {
	return &f
}

// GetDeviceTopicName — получить MAC для топика MQTT
func (c *Config) GetDeviceTopicName(mac string) string {
	return NormalizeMACForTopic(mac)
}

// GetPresenceTimeout — получить таймаут присутствия для устройства
// Сначала ищем индивидуальный таймаут в KnownDevices, если не найден используем общий
func (c *Config) GetPresenceTimeout(mac string) int {
	normalizedMAC := NormalizeMACForTopic(mac)
	if knownDevice, exists := c.KnownDevices[normalizedMAC]; exists && knownDevice.PresenceTimeout > 0 {
		return knownDevice.PresenceTimeout
	}
	return c.PresenceTimeout
}

// NormalizeMACForTopic — нормализация MAC-адреса для использования в топиках (lowercase)
func NormalizeMACForTopic(mac string) string {
	mac = strings.ToLower(mac)
	mac = strings.ReplaceAll(mac, "-", ":")
	return mac
}
