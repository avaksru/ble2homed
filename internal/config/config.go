package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/avaksru/ble2homed/pkg/types"
	"gopkg.in/yaml.v3"
)

// DefaultConfig — конфигурация по умолчанию
func DefaultConfig() *types.Config {
	return &types.Config{
		MQTT: types.MQTTConfig{
			Broker:   "tcp://localhost:1883",
			ClientID: "ble2homed",
			QoS:      1,
		},
		BLE: types.BLEConfig{
			Adapter:     "hci0",
			ScanTimeout: "0s", // 0 = непрерывное сканирование
			Connect:     false,
		},
		Publish: types.PublishConfig{
			BasePrefix:     "/ble",
			RetainPresence: false,
		},
		History: types.HistoryConfig{
			Enabled:   true,
			Intervals: []string{"1m", "10m", "1h", "24h", "7d"},
		},
		Web: types.WebConfig{
			Enabled: false,
			Port:    8090,
		},
		Log: types.LogConfig{
			Level: "info",
		},
	}
}

// LoadConfig — загрузка конфигурации из файла
// Поддерживает .yaml, .yml и .json форматы
// Если файл не найден, возвращает конфигурацию по умолчанию
func LoadConfig(path string) (*types.Config, error) {
	// Если путь пустой, ищем config.yaml или config.json в текущей директории
	if path == "" {
		path = findConfigFile()
	}

	// Если файл не найден, используем дефолтный конфиг
	if path == "" || !fileExists(path) {
		fmt.Println("Config file not found, using default configuration")
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	config := DefaultConfig()

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml", ".conf":
		if err := yaml.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("failed to parse YAML config: %w", err)
		}
	case ".json":
		if err := json.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("failed to parse JSON config: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported config format: %s (use .yaml, .yml, .conf, or .json)", ext)
	}

	// Валидация конфигурации
	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Преобразование mqtt_host в broker
	if config.MQTTHost != "" {
		config.MQTT.Broker = config.MQTTHost
	}

	// Преобразование mqtt_prefix в base_prefix
	if config.MQTTPrefix != "" {
		config.Publish.BasePrefix = config.MQTTPrefix
	}

	// Нормализация MAC адресов в known_devices
	normalizedKnownDevices := make(map[string]types.KnownDevice)
	var knownDevicesOrder []string
	for mac := range config.KnownDevices {
		normalizedMAC := types.NormalizeMACForTopic(mac)
		normalizedKnownDevices[normalizedMAC] = config.KnownDevices[mac]
		knownDevicesOrder = append(knownDevicesOrder, normalizedMAC)
	}
	config.KnownDevices = normalizedKnownDevices
	config.KnownDevicesOrder = knownDevicesOrder

	// Копируем scanInterval и scanTimeout в BLEConfig
	if config.ScanInterval > 0 {
		config.BLE.ScanInterval = config.ScanInterval
	}
	if config.ScanTimeout > 0 {
		config.BLE.RestartPause = config.ScanTimeout
	}

	// Устанавливаем значения по умолчанию для BLE, если не заданы
	// 0 = непрерывное сканирование без пауз
	if config.BLE.ScanInterval < 0 {
		config.BLE.ScanInterval = 60
	}
	if config.BLE.RestartPause < 0 {
		config.BLE.RestartPause = 5
	}

	fmt.Printf("Config loaded from: %s\n", path)
	return config, nil
}

// findConfigFile — поиск файла конфигурации в стандартных местах
func findConfigFile() string {
	// Порядок поиска: config.yaml, config.yml, config.json
	// В текущей директории и в ./configs/
	candidates := []string{
		"/etc/homed/homed-ble.conf",
		"homed-ble.conf",
		"config.yaml",
		"config.yml",
		"config.json",
		"configs/homed-ble.conf",
		"configs/config.yaml",
		"configs/config.yml",
		"configs/config.json",
		"/etc/ble2homed/homed-ble.conf",
		"/etc/ble2homed/config.yaml",
		"/etc/ble2homed/config.json",
	}

	for _, path := range candidates {
		if fileExists(path) {
			return path
		}
	}

	return ""
}

// fileExists — проверка существования файла
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// validateConfig — валидация конфигурации
func validateConfig(config *types.Config) error {
	// Проверка уровня логирования
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLevels[config.Log.Level] {
		return fmt.Errorf("invalid log level: %s (must be debug, info, warn, or error)", config.Log.Level)
	}

	// Проверка QoS
	if config.MQTT.QoS > 2 {
		return fmt.Errorf("invalid MQTT QoS: %d (must be 0, 1, or 2)", config.MQTT.QoS)
	}

	// Проверка базового префикса
	if config.Publish.BasePrefix == "" {
		config.Publish.BasePrefix = "/ble"
	}
	// Убираем trailing slash
	config.Publish.BasePrefix = strings.TrimSuffix(config.Publish.BasePrefix, "/")

	// Проверка MQTT prefix
	if config.MQTTPrefix == "" {
		config.MQTTPrefix = "homed"
	}

	// Проверка интервалов истории
	if config.History.Enabled && len(config.History.Intervals) == 0 {
		config.History.Intervals = []string{"1m", "10m", "1h", "24h", "7d"}
	}

	return nil
}

// SaveConfig — сохранение конфигурации в файл
func SaveConfig(config *types.Config, path string) error {
	var data []byte
	var err error

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml", ".conf":
		data, err = yaml.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal YAML: %w", err)
		}
	case ".json":
		data, err = json.MarshalIndent(config, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
	default:
		return fmt.Errorf("unsupported config format: %s", ext)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
