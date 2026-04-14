package mqtt

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	dbMu                sync.Mutex
	maxDevices          int    // максимальное количество устройств
	version             string
	permitJoin          bool
	lastBleStatusHash   string // хэш последнего опубликованного status/ble
	lastDeviceAddedTime time.Time
}

// NewPublisher — создание нового publisher
func NewPublisher(client *Client, config *types.Config, logger zerolog.Logger, version string) *Publisher {
	maxDevices := 1000 // по умолчанию 1000 устройств
	if config.MaxConnections > 0 {
		maxDevices = config.MaxConnections
	}

	if config.DatabasePath == "" {
		config.DatabasePath = "/opt/homed-ble/database.json"
	}

	return &Publisher{
		client:             client,
		config:             config,
		logger:             logger.With().Str("component", "publisher").Logger(),
		devices:            make(map[string]*types.Device),
		deviceCreatedTimes: make(map[string]time.Time),
		maxDevices:         maxDevices,
		version:            version,
		permitJoin:         !config.OnlyKnownDevices,
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
type deviceDatabase struct {
	Devices   []dbDevice `json:"devices"`
	Timestamp int64      `json:"timestamp"`
	Version   string     `json:"version"`
}

type dbDevice struct {
	Active    bool     `json:"active"`
	Cloud     bool     `json:"cloud"`
	Discovery bool     `json:"discovery"`
	Exposes   []string `json:"exposes"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Real      bool     `json:"real"`
}

func (p *Publisher) Config() *types.Config {
	return p.config
}

func (p *Publisher) IsPermitJoin() bool {
	return p.permitJoin
}

func (p *Publisher) SetPermitJoin(enabled bool) error {
	p.permitJoin = enabled
	p.config.OnlyKnownDevices = !enabled

	if !enabled {
		if err := p.loadKnownDevicesFromDatabase(); err != nil {
			p.logger.Warn().Err(err).Msg("Failed to load known devices from database")
		}
	}

	return nil
}

func (p *Publisher) loadKnownDevicesFromDatabase() error {
	db, err := p.loadDeviceDatabase()
	if err != nil {
		return err
	}

	if len(db.Devices) == 0 {
		return nil
	}

	knownDevices := make(map[string]types.KnownDevice, len(db.Devices))
	order := make([]string, 0, len(db.Devices))

	for _, entry := range db.Devices {
		id := types.NormalizeMACForTopic(entry.ID)
		if id == "" {
			continue
		}

		knownDevices[id] = types.KnownDevice{
			Name:           entry.Name,
			HOMEdCloud:     entry.Cloud,
			HOMEdDiscovery: entry.Discovery,
		}
		order = append(order, id)
	}

	if len(knownDevices) == 0 {
		return nil
	}

	p.config.KnownDevices = knownDevices
	p.config.KnownDevicesOrder = order
	return nil
}

func (p *Publisher) loadDeviceDatabase() (*deviceDatabase, error) {
	path := filepath.FromSlash(p.config.DatabasePath)
	if path == "" {
		return &deviceDatabase{Devices: []dbDevice{}}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &deviceDatabase{Devices: []dbDevice{}}, nil
		}
		return nil, err
	}

	var db deviceDatabase
	if err := json.Unmarshal(data, &db); err != nil {
		return nil, err
	}

	if db.Devices == nil {
		db.Devices = []dbDevice{}
	}

	return &db, nil
}

func (p *Publisher) saveDeviceDatabase(db *deviceDatabase) error {
	path := filepath.FromSlash(p.config.DatabasePath)
	if path == "" {
		return fmt.Errorf("database path is not configured")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	content, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, content, 0o644)
}

func (p *Publisher) hasUsefulDeviceData(device *types.Device) bool {
	parsedValues := device.GetParsedValues()
	for key, val := range parsedValues {
		switch key {
		case "temp", "humidity", "battery", "pressure", "illuminance":
			if val.Value != nil {
				return true
			}
		}
	}
	return false
}

func (p *Publisher) updateDeviceDatabase(device *types.Device) error {
	if !p.hasUsefulDeviceData(device) {
		return nil
	}

	db, err := p.loadDeviceDatabase()
	if err != nil {
		return err
	}

	p.dbMu.Lock()
	defer p.dbMu.Unlock()

	normalizedID := types.NormalizeMACForTopic(device.MAC)
	if normalizedID == "" {
		normalizedID = device.MAC
	}

	name := device.Name
	if name == "" {
		name = normalizedID
	}

	exposes := device.GetHomedExpose().Common.Items

	updated := false
	for i, existing := range db.Devices {
		if types.NormalizeMACForTopic(existing.ID) == normalizedID {
			db.Devices[i].Active = device.IsOnline()
			db.Devices[i].Name = name
			db.Devices[i].Exposes = exposes
			updated = true
			break
		}
	}

	if !updated {
		db.Devices = append(db.Devices, dbDevice{
			Active:    device.IsOnline(),
			Cloud:     false,
			Discovery: false,
			Exposes:   exposes,
			ID:        normalizedID,
			Name:      name,
			Real:      false,
		})
	}

	db.Timestamp = time.Now().Unix()
	db.Version = p.version

	return p.saveDeviceDatabase(db)
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

	if p.IsPermitJoin() {
		if err := p.updateDeviceDatabase(device); err != nil {
			p.logger.Error().Err(err).Str("mac", mac).Msg("Failed to save device into database")
		}
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
	err = p.client.PublishJSON(topic, exposeBytes, true)

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
	return p.client.PublishJSON(topic, payloadBytes, true)
}

// publishBleStatus — публикация статуса всех устройств в топик status/ble
// Устройства сортируются в порядке, определённом в config.json (KnownDevicesOrder).
// Возвращает true если был опубликован новый статус, false если ничего не изменилось.
func (p *Publisher) publishBleStatus() (bool, error) {
	base := p.config.MQTTPrefix
	topic := fmt.Sprintf("%s/status/ble", base)

	var status types.BleStatus
	status.Devices = make([]types.BleStatusDevice, 0)
	status.Names = false
	status.PermitJoin = p.permitJoin
	status.Version = p.version
	status.Timestamp = time.Now().Unix()

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

	// Сериализуем и считаем хэш только по устройствам и состоянию PermitJoin,
	// чтобы timestamp не ломал логику публикации при отсутствии изменений.
	hashSource := struct {
		Devices    []types.BleStatusDevice `json:"devices"`
		PermitJoin bool                   `json:"permitJoin"`
		Names      bool                   `json:"names"`
	}{
		Devices:    status.Devices,
		PermitJoin: status.PermitJoin,
		Names:      status.Names,
	}
	hashBytes, err := json.Marshal(hashSource)
	if err != nil {
		return false, err
	}

	// Вычисляем хэш текущего состояния
	currentHash := fmt.Sprintf("%x", md5.Sum(hashBytes))

	// Если хэш не изменился, пропускаем публикацию
	if currentHash == p.lastBleStatusHash {
		return false, nil
	}

	statusBytes, err := json.Marshal(status)
	if err != nil {
		return false, err
	}

	// Публикуем только если изменилось
	if err := p.client.PublishJSON(topic, statusBytes, true); err != nil {
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
	return p.client.PublishJSON(topic, payloadBytes, true)
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
