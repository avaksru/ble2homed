package discovery

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/avaksru/ble2homed/internal/mqtt"
	"github.com/avaksru/ble2homed/pkg/types"
	"github.com/rs/zerolog"
)

// Discovery — Home Assistant MQTT Discovery
type Discovery struct {
	client *mqtt.Client
	config *types.DiscoveryConfig
	logger zerolog.Logger
}

// NewDiscovery — создание нового discovery
func NewDiscovery(client *mqtt.Client, config *types.DiscoveryConfig, logger zerolog.Logger) *Discovery {
	return &Discovery{
		client: client,
		config: config,
		logger: logger.With().Str("component", "discovery").Logger(),
	}
}

// SensorConfig — конфигурация сенсора для HA Discovery
type SensorConfig struct {
	Name                string `json:"name"`
	UniqueID            string `json:"unique_id"`
	StateTopic          string `json:"state_topic"`
	UnitOfMeasurement   string `json:"unit_of_measurement,omitempty"`
	DeviceClass         string `json:"device_class,omitempty"`
	ValueTemplate       string `json:"value_template,omitempty"`
	AvailabilityTopic   string `json:"availability_topic,omitempty"`
	PayloadAvailable    string `json:"payload_available,omitempty"`
	PayloadNotAvailable string `json:"payload_not_available,omitempty"`
	Device              Device `json:"device"`
}

// Device — информация об устройстве для HA
type Device struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	Model        string   `json:"model,omitempty"`
	SwVersion    string   `json:"sw_version,omitempty"`
}

// PublishDiscovery — публикация discovery конфигураций для устройства
func (d *Discovery) PublishDiscovery(device *types.Device, basePrefix string) {
	if !d.config.Enabled {
		return
	}

	macLower := strings.ToLower(device.MAC)
	deviceID := strings.ReplaceAll(macLower, ":", "_")
	deviceName := device.Name
	if deviceName == "" {
		deviceName = fmt.Sprintf("BLE Device %s", macLower)
	}

	// Базовая информация об устройстве
	baseDevice := Device{
		Identifiers:  []string{fmt.Sprintf("ble_%s", deviceID)},
		Name:         deviceName,
		Manufacturer: "BLE",
		Model:        "BLE Sensor",
		SwVersion:    "ble2homed 1.0.0",
	}

	// RSSI сенсор
	d.publishSensor(SensorConfig{
		Name:              fmt.Sprintf("%s RSSI", deviceName),
		UniqueID:          fmt.Sprintf("ble_%s_rssi", deviceID),
		StateTopic:        fmt.Sprintf("%s/fd/%s/rssi", basePrefix, macLower),
		UnitOfMeasurement: "dBm",
		DeviceClass:       "signal_strength",
		Device:            baseDevice,
	}, deviceID, "rssi")

	// Battery сенсор (если есть)
	if device.Battery != nil {
		d.publishSensor(SensorConfig{
			Name:              fmt.Sprintf("%s Battery", deviceName),
			UniqueID:          fmt.Sprintf("ble_%s_battery", deviceID),
			StateTopic:        fmt.Sprintf("%s/fd/%s/battery", basePrefix, macLower),
			UnitOfMeasurement: "%",
			DeviceClass:       "battery",
			Device:            baseDevice,
		}, deviceID, "battery")
	}

	// Presence binary sensor
	d.publishBinarySensor(SensorConfig{
		Name:                fmt.Sprintf("%s Presence", deviceName),
		UniqueID:            fmt.Sprintf("ble_%s_presence", deviceID),
		StateTopic:          fmt.Sprintf("%s/presence/%s", basePrefix, macLower),
		PayloadAvailable:    "1",
		PayloadNotAvailable: "0",
		Device:              baseDevice,
	}, deviceID, "presence")

	// Распарсенные значения
	for key, val := range device.GetParsedValues() {
		if val.Value == nil {
			continue
		}

		config := SensorConfig{
			Name:              fmt.Sprintf("%s %s", deviceName, strings.Title(key)),
			UniqueID:          fmt.Sprintf("ble_%s_%s", deviceID, key),
			StateTopic:        fmt.Sprintf("%s/fd/%s/%s", basePrefix, macLower, key),
			UnitOfMeasurement: val.Unit,
			Device:            baseDevice,
		}

		// Определяем device class по типу
		switch val.Type {
		case "temp":
			config.DeviceClass = "temperature"
			config.Name = fmt.Sprintf("%s Temperature", deviceName)
		case "humidity":
			config.DeviceClass = "humidity"
			config.Name = fmt.Sprintf("%s Humidity", deviceName)
		case "pressure":
			config.DeviceClass = "pressure"
			config.Name = fmt.Sprintf("%s Pressure", deviceName)
		case "illuminance":
			config.DeviceClass = "illuminance"
			config.Name = fmt.Sprintf("%s Illuminance", deviceName)
		}

		d.publishSensor(config, deviceID, key)
	}
}

// publishSensor — публикация discovery конфигурации сенсора
func (d *Discovery) publishSensor(config SensorConfig, deviceID, sensorType string) {
	topic := fmt.Sprintf("%s/sensor/ble_%s/%s/config", d.config.Prefix, deviceID, sensorType)

	configBytes, err := json.Marshal(config)
	if err != nil {
		d.logger.Error().Err(err).Str("topic", topic).Msg("Failed to marshal sensor config")
		return
	}

	if err := d.client.PublishJSON(topic, configBytes, true); err != nil {
		d.logger.Error().Err(err).Str("topic", topic).Msg("Failed to publish discovery config")
		return
	}

	d.logger.Debug().Str("topic", topic).Msg("Published discovery config")
}

// publishBinarySensor — публикация discovery конфигурации binary sensor
func (d *Discovery) publishBinarySensor(config SensorConfig, deviceID, sensorType string) {
	topic := fmt.Sprintf("%s/binary_sensor/ble_%s/%s/config", d.config.Prefix, deviceID, sensorType)

	configBytes, err := json.Marshal(config)
	if err != nil {
		d.logger.Error().Err(err).Str("topic", topic).Msg("Failed to marshal binary sensor config")
		return
	}

	if err := d.client.PublishJSON(topic, configBytes, true); err != nil {
		d.logger.Error().Err(err).Str("topic", topic).Msg("Failed to publish discovery config")
		return
	}

	d.logger.Debug().Str("topic", topic).Msg("Published binary sensor discovery config")
}

// PublishAvailability — публикация топика доступности
func (d *Discovery) PublishAvailability(basePrefix string) {
	topic := fmt.Sprintf("%s/status", basePrefix)
	if err := d.client.Publish(topic, "online", true); err != nil {
		d.logger.Error().Err(err).Msg("Failed to publish availability")
	}
}