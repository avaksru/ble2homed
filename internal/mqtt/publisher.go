package mqtt

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/avaksru/ble2homed/pkg/types"
	"github.com/rs/zerolog"
)

// Publisher — публикация данных в MQTT с поддержкой разных режимов
type Publisher struct {
	client              *Client
	config              *types.Config
	logger              zerolog.Logger
	devices             map[string]*types.Device
	deviceCreatedTimes  map[string]time.Time // время создания каждого устройства
	mu                  sync.RWMutex
	maxDevices          int    // максимальное количество устройств
	lastBleStatusHash   string // хэш последнего опубликованного status/ble
	lastDeviceAddedTime time.Time
}

// NewPublisher — создание нового publisher
func NewPublisher(client *Client, config *types.Config, logger zerolog.Logger) *Publisher {
	maxDevices := 1000 // по умолчанию 1000 устройств
	if config.MaxConnections > 0 {
		maxDevices = config.MaxConnections
	}
	return &Publisher{
		client:             client,
		config:             config,
		logger:             logger.With().Str("component", "publisher").Logger(),
		devices:            make(map[string]*types.Device),
		deviceCreatedTimes: make(map[string]time.Time),
		maxDevices:         maxDevices,
	}
}

// GetOrCreateDevice — получение или создание устройства
func (p *Publisher) GetOrCreateDevice(mac string) *types.Device {
	p.mu.Lock()
	defer p.mu.Unlock()

	normalizedMAC := types.NormalizeMACForTopic(mac)

	if device, exists := p.devices[normalizedMAC]; exists {
		return device
	}

	// Проверяем лимит устройств
	if len(p.devices) >= p.maxDevices {
		p.logger.Warn().
			Int("current", len(p.devices)).
			Int("max", p.maxDevices).
			Str("mac", normalizedMAC).
			Msg("Max devices limit reached, cannot add new device")
		return nil
	}

	device := types.NewDevice(normalizedMAC)
	p.devices[normalizedMAC] = device
	p.deviceCreatedTimes[normalizedMAC] = time.Now()
	p.logger.Info().Str("mac", normalizedMAC).Msg("New device discovered")
	p.lastDeviceAddedTime = time.Now()
	return device
}

// GetDevice — получение устройства (поддерживает как MAC так и настроенное имя)
func (p *Publisher) GetDevice(identifier string) (*types.Device, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	// Сначала пробуем как MAC
	normalizedMAC := types.NormalizeMACForTopic(identifier)
	if device, exists := p.devices[normalizedMAC]; exists {
		return device, exists
	}

	// Если не найдено - ищем по настроенному имени
	for mac, knownDevice := range p.config.KnownDevices {
		if knownDevice.Name != "" && strings.EqualFold(knownDevice.Name, identifier) {
			if device, exists := p.devices[mac]; exists {
				return device, exists
			}
		}
	}

	return nil, false
}

// GetAllDevices — получение всех устройств
func (p *Publisher) GetAllDevices() map[string]*types.Device {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]*types.Device, len(p.devices))
	for k, v := range p.devices {
		result[k] = v
	}
	return result
}

// Config — получить конфигурацию
func (p *Publisher) Config() *types.Config {
	return p.config
}

// IsDeviceNew — проверка, является ли устройство недавно добавленным
// Внутренне вызывает GetOrCreateDevice для корректного отслеживания
func (p *Publisher) IsDeviceNew(mac string) bool {
	p.mu.RLock()
	normalizedMAC := types.NormalizeMACForTopic(mac)
	_, exists := p.devices[normalizedMAC]
	p.mu.RUnlock()

	// Если устройства не было в момента проверки, оно новое
	return !exists
}

// PublishAdvertisement — публикация advertising данных в HOMEd стиле
func (p *Publisher) PublishAdvertisement(mac string, adv types.Advertisement, parsed map[string]types.ParsedValue) error {
	device := p.GetOrCreateDevice(mac)
	if device == nil {
		return fmt.Errorf("cannot create device: max devices limit reached")
	}

	wasOffline := !device.IsOnline()
	device.UpdateLastSeen()
	device.RSSI = adv.RSSI
	if adv.Name != "" {
		device.Name = adv.Name
	}

	// ✅ Всегда публикуем online статус при получении данных
	// Это решает проблему когда после перезапуска брокера/сервиса статус остаётся offline
	p.logger.Debug().Str("mac", mac).Msg("Publishing online status for active device")
	if err := p.publishDeviceStatus(mac, "online", device.GetLastSeen()); err != nil {
		p.logger.Error().Err(err).Str("mac", mac).Msg("Failed to publish online status")
	}

	// Если устройство перешло из оффлайна — логируем это отдельно
	if wasOffline {
		p.logger.Info().Str("mac", mac).Msg("Device came online")
	}

	// Обновляем распарсенные значения
	for key, val := range parsed {
		device.SetParsedValue(key, val)
	}

	// Логируем распарсенные полезные данные
	usefulFields := map[string]interface{}{}
	for key, val := range parsed {
		switch key {
		case "temp", "humidity", "battery", "pressure", "illuminance":
			if val.Value != nil {
				usefulFields[key] = val.Value
			}
		}
	}

	if len(usefulFields) > 0 {
		logEvent := p.logger.Info().Str("mac", mac)
		for k, v := range usefulFields {
			logEvent = logEvent.Interface(k, v)
		}
		logEvent.Msg("Device sensor data parsed")
	}

	// Публикуем в HOMEd стиле
	if err := p.publishHomed(mac, adv, parsed, device); err != nil {
		return err
	}

	// Публикуем expose для HOMEd
	if err := p.publishExpose(mac, device); err != nil {
		return err
	}

	return nil
}

// publishHomed — публикация в стиле HOMEd
func (p *Publisher) publishHomed(mac string, adv types.Advertisement, parsed map[string]types.ParsedValue, device *types.Device) error {
	base := p.config.Publish.BasePrefix
	topicName := p.config.GetDeviceTopicName(mac)

	// Проверяем есть ли полезные данные для публикации
	hasUsefulData := false
	for key, val := range parsed {
		switch key {
		case "temp", "humidity", "battery", "pressure", "illuminance":
			if val.Value != nil {
				hasUsefulData = true
				break
			}
		}
	}

	// Публикуем только если есть полезные данные
	if hasUsefulData {
		// fd/{name_or_mac} — текущие значения как плоский JSON
		fdFlat := device.GetFDFlat()
		fdBytes, _ := json.Marshal(fdFlat)
		if err := p.client.PublishJSON(fmt.Sprintf("%s/fd/ble/%s", base, topicName), fdBytes, false); err != nil {
			return err
		}
	}

	return nil
}

// publishExpose — публикация expose для HOMEd
func (p *Publisher) publishExpose(mac string, device *types.Device) error {
	// ✅ Публикуем только ОДИН РАЗ после первого обнаружения
	device.Mu.RLock()
	alreadyPublished := device.ExposePublished
	device.Mu.RUnlock()

	if alreadyPublished {
		return nil
	}

	// Публикуем только если есть полезные сенсорные данные
	hasUsefulData := false

	parsedValues := device.GetParsedValues()
	for key := range parsedValues {
		switch key {
		case "temp", "humidity", "battery", "pressure", "illuminance":
			hasUsefulData = true
			break
		}
	}

	if !hasUsefulData {
		// Нет полезных данных - пропускаем публикацию expose
		return nil
	}

	base := p.config.MQTTPrefix
	topicName := p.config.GetDeviceTopicName(mac)

	// Получаем expose данные
	homedExpose := device.GetHomedExpose()

	// Публикуем expose
	exposeBytes, err := json.Marshal(homedExpose)
	if err != nil {
		return err
	}

	topic := fmt.Sprintf("%s/expose/ble/%s", base, topicName)
	err = p.client.PublishJSON(topic, exposeBytes, p.config.Retain)

	if err == nil {
		// Устанавливаем флаг что опубликовали успешно
		device.Mu.Lock()
		device.ExposePublished = true
		device.Mu.Unlock()

		p.logger.Debug().Str("mac", mac).Msg("Expose published once, will not publish again")
	}

	return err
}

// publishDeviceOffline — публикация offline статуса устройства
func (p *Publisher) publishDeviceOffline(mac string, device *types.Device) error {
	base := p.config.MQTTPrefix
	topicName := p.config.GetDeviceTopicName(mac)

	topic := fmt.Sprintf("%s/device/ble/%s", base, topicName)

	payload := map[string]interface{}{
		"lastSeen": device.GetLastSeen().Unix(),
		"status":   "offline",
	}

	payloadBytes, _ := json.Marshal(payload)
	return p.client.PublishJSON(topic, payloadBytes, p.config.Retain)
}

// publishBleStatus — публикация статуса всех устройств в топик status/ble
// Устройства сортируются в порядке, определённом в config.json (KnownDevicesOrder).
// Возвращает true если был опубликован новый статус, false если ничего не изменилось.
func (p *Publisher) publishBleStatus() (bool, error) {
	base := p.config.MQTTPrefix
	topic := fmt.Sprintf("%s/status/ble", base)

	var status types.BleStatus
	status.Devices = make([]types.BleStatusDevice, 0)

	// Используем KnownDevicesOrder для сохранения порядка из config.json
	for _, mac := range p.config.KnownDevicesOrder {
		knownDevice, exists := p.config.KnownDevices[mac]
		if !exists {
			continue // На случай если порядок рассинхронизирован с картой
		}

		device, deviceExists := p.devices[mac]

		statusDevice := types.BleStatusDevice{
			Active:    deviceExists && device.IsOnline(),
			Cloud:     knownDevice.HOMEdCloud,
			Discovery: knownDevice.HOMEdDiscovery,
			ID:        mac,
			Name:      knownDevice.Name,
			Real:      true,
		}

		// Получаем expose только для активных устройств
		if deviceExists {
			homedExpose := device.GetHomedExpose()
			statusDevice.Exposes = homedExpose.Common.Items
			statusDevice.Options = homedExpose.Common.Options
		}

		status.Devices = append(status.Devices, statusDevice)
	}

	// Сериализуем и считаем хэш
	statusBytes, err := json.Marshal(status)
	if err != nil {
		return false, err
	}

	// Вычисляем хэш текущего состояния
	currentHash := fmt.Sprintf("%x", md5.Sum(statusBytes))

	// Если хэш не изменился, пропускаем публикацию
	if currentHash == p.lastBleStatusHash {
		return false, nil
	}

	// Публикуем только если изменилось
	if err := p.client.PublishJSON(topic, statusBytes, p.config.Retain); err != nil {
		return false, err
	}

	p.lastBleStatusHash = currentHash
	p.logger.Info().
		Int("device_count", len(status.Devices)).
		Str("topic", topic).
		Msg("BLE status published")

	return true, nil
}

// PublishBleStatusIfChanged — публикация BLE статуса если произошли изменения
// Вызывается при добавлении нового устройства или изменении статуса
func (p *Publisher) PublishBleStatusIfChanged() {
	published, err := p.publishBleStatus()
	if err != nil {
		p.logger.Error().Err(err).Msg("Failed to publish BLE status")
	} else if published {
		p.logger.Debug().Msg("BLE status was updated and published")
	}
}

// PublishHistoryValue — публикация исторического значения
func (p *Publisher) PublishHistoryValue(mac, field, interval string, value float64) error {
	base := p.config.Publish.BasePrefix
	topicName := p.config.GetDeviceTopicName(mac)

	topic := fmt.Sprintf("%s/hist/%s/%s/%s", base, interval, topicName, field)
	return p.client.Publish(topic, value, false)
}

// PublishCommandResponse — публикация ответа на команду
func (p *Publisher) PublishCommandResponse(mac, service, char string, data []byte, responseType string) error {
	base := p.config.Publish.BasePrefix
	topicName := p.config.GetDeviceTopicName(mac)

	var topic string
	switch responseType {
	case "data":
		topic = fmt.Sprintf("%s/fd/%s/data/%s/%s", base, topicName, service, char)
	case "written":
		topic = fmt.Sprintf("%s/fd/%s/written/%s/%s", base, topicName, service, char)
	case "pong":
		topic = fmt.Sprintf("%s/fd/%s/pong", base, topicName)
	default:
		return fmt.Errorf("unknown response type: %s", responseType)
	}

	return p.client.Publish(topic, data, false)
}

// publishDeviceStatus — публикация статуса устройства (online/offline)
func (p *Publisher) publishDeviceStatus(mac string, status string, lastSeen time.Time) error {
	topicName := p.config.GetDeviceTopicName(mac)
	topic := fmt.Sprintf("%s/device/ble/%s", p.config.MQTTPrefix, topicName)

	payload := map[string]interface{}{
		"lastSeen": lastSeen.Unix(),
		"status":   status,
	}

	payloadBytes, _ := json.Marshal(payload)
	return p.client.PublishJSON(topic, payloadBytes, p.config.Retain)
}

// CheckOfflineDevices — проверка устройств на offline по presence_timeout
func (p *Publisher) CheckOfflineDevices() {
	// Получаем копию устройств под RLock
	p.mu.RLock()
	devicesCopy := make(map[string]*types.Device, len(p.devices))
	for k, v := range p.devices {
		devicesCopy[k] = v
	}
	p.mu.RUnlock()

	now := time.Now()

	for mac, device := range devicesCopy {
		// Проверяем online статус через потокобезопасные методы
		isOnline := device.IsOnline()
		lastSeen := device.GetLastSeen()

		if !isOnline {
			continue // Уже offline
		}

		// Определяем timeout для устройства
		timeout := p.config.GetPresenceTimeout(mac)

		if timeout <= 0 {
			continue // timeout не задан, пропускаем
		}

		// Проверяем, прошло ли достаточно времени
		elapsed := now.Sub(lastSeen).Seconds()
		if elapsed > float64(timeout) {
			// Устройство offline — используем потокобезопасный метод
			device.SetOnline(false)

			p.logger.Info().
				Str("mac", mac).
				Float64("elapsed", elapsed).
				Int("timeout", timeout).
				Msg("Device went offline (timeout)")

			if err := p.publishDeviceStatus(mac, "offline", lastSeen); err != nil {
				p.logger.Error().Err(err).Str("mac", mac).Msg("Failed to publish offline status")
			}
		}
	}

	// Если only_known_devices=true, публикуем offline для всех известных устройств
	if p.config.OnlyKnownDevices {
		for mac := range p.config.KnownDevices {
			_, exists := p.devices[mac]
			if !exists {
				// Устройство не найдено в активных — публикуем offline
				if err := p.publishDeviceOffline(mac, &types.Device{
					MAC:      mac,
					LastSeen: time.Now(),
					Online:   false,
				}); err != nil {
					p.logger.Error().Err(err).Str("mac", mac).Msg("Failed to publish offline status for known device")
				}
			}
		}
	}
}
