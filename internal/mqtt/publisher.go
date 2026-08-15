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
	offlinePublished    map[string]bool      // флаг что оффлайн статус уже был отправлен
	mu                  sync.RWMutex
	dbMu                sync.Mutex
	maxDevices          int // максимальное количество устройств
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

	// Настраиваем zerolog для вывода в консоль с уровнем из конфигурации
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.SetGlobalLevel(getLogLevel(config.Log.Level))

	publisher := &Publisher{
		client:             client,
		config:             config,
		logger:             logger.With().Str("component", "publisher").Logger(),
		devices:            make(map[string]*types.Device),
		deviceCreatedTimes: make(map[string]time.Time),
		offlinePublished:   make(map[string]bool),
		maxDevices:         maxDevices,
		version:            version,
		permitJoin:         !config.OnlyKnownDevices,
	}

	// ✅ Загружаем все устройства из базы данных ОДИН РАЗ при старте
	if err := publisher.loadKnownDevicesFromDatabase(); err != nil {
		publisher.logger.Warn().Err(err).Msg("Failed to load known devices from database at startup")
	}

	return publisher
}

func getLogLevel(level string) zerolog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
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

func (p *Publisher) findDeviceByIdentifier(identifier string) (string, *types.Device) {
	normalizedID := types.NormalizeMACForTopic(identifier)
	if device, exists := p.devices[normalizedID]; exists {
		return normalizedID, device
	}

	if _, exists := p.config.KnownDevices[normalizedID]; exists {
		return normalizedID, nil
	}

	for mac, device := range p.devices {
		if knownDevice, exists := p.config.KnownDevices[mac]; exists && knownDevice.Name != "" && strings.EqualFold(knownDevice.Name, identifier) {
			return mac, device
		}
	}

	for mac, knownDevice := range p.config.KnownDevices {
		if knownDevice.Name != "" && strings.EqualFold(knownDevice.Name, identifier) {
			return mac, nil
		}
	}

	return "", nil
}

func (p *Publisher) RemoveDevice(identifier string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	mac, _ := p.findDeviceByIdentifier(identifier)
	if mac == "" {
		return "", fmt.Errorf("device not found: %s", identifier)
	}

	delete(p.devices, mac)
	delete(p.deviceCreatedTimes, mac)

	if p.config.KnownDevices != nil {
		if _, exists := p.config.KnownDevices[mac]; exists {
			delete(p.config.KnownDevices, mac)
			for i, id := range p.config.KnownDevicesOrder {
				if id == mac {
					p.config.KnownDevicesOrder = append(p.config.KnownDevicesOrder[:i], p.config.KnownDevicesOrder[i+1:]...)
					break
				}
			}
		}
	}

	if err := p.removeDeviceFromDatabase(mac); err != nil {
		return "", err
	}

	return mac, nil
}

func (p *Publisher) removeDeviceFromDatabase(mac string) error {
	db, err := p.loadDeviceDatabase()
	if err != nil {
		return err
	}

	normalizedID := types.NormalizeMACForTopic(mac)
	if normalizedID == "" {
		normalizedID = mac
	}

	p.dbMu.Lock()
	defer p.dbMu.Unlock()

	filtered := make([]dbDevice, 0, len(db.Devices))
	for _, entry := range db.Devices {
		if types.NormalizeMACForTopic(entry.ID) == normalizedID {
			continue
		}
		filtered = append(filtered, entry)
	}

	if len(filtered) == len(db.Devices) {
		return nil
	}

	db.Devices = filtered
	db.Timestamp = time.Now().Unix()
	db.Version = p.version

	return p.saveDeviceDatabase(db)
}

func (p *Publisher) UpdateDevice(identifier string, data map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	mac, device := p.findDeviceByIdentifier(identifier)
	if mac == "" {
		mac = types.NormalizeMACForTopic(identifier)
		if mac == "" {
			mac = identifier
		}
	}

	if device == nil {
		device = types.NewDevice(mac)
		p.devices[mac] = device
	}

	if p.config.KnownDevices == nil {
		p.config.KnownDevices = make(map[string]types.KnownDevice)
	}

	knownDevice := p.config.KnownDevices[mac]

	if name, ok := data["name"].(string); ok && name != "" {
		device.Name = name
		knownDevice.Name = name
	}

	if active, ok := data["active"].(bool); ok {
		device.SetOnline(active)
	}

	if cloud, ok := data["cloud"].(bool); ok {
		knownDevice.HOMEdCloud = cloud
	}

	if discovery, ok := data["discovery"].(bool); ok {
		knownDevice.HOMEdDiscovery = discovery
	}

	p.config.KnownDevices[mac] = knownDevice
	if !containsString(p.config.KnownDevicesOrder, mac) {
		p.config.KnownDevicesOrder = append(p.config.KnownDevicesOrder, mac)
	}

	return p.saveKnownDeviceToDatabase(device, knownDevice, data)
}

func (p *Publisher) saveKnownDeviceToDatabase(device *types.Device, knownDevice types.KnownDevice, data map[string]interface{}) error {
	p.dbMu.Lock()
	defer p.dbMu.Unlock()

	db, err := p.loadDeviceDatabase()
	if err != nil {
		return err
	}

	// Дедупликация при загрузке - на случай если в базе уже есть дубликаты
	db.Devices = p.deduplicateDevices(db.Devices)

	normalizedID := types.NormalizeMACForTopic(device.MAC)
	if normalizedID == "" {
		normalizedID = device.MAC
	}

	name := device.Name
	if name == "" {
		name = normalizedID
	}

	exposes := device.GetHomedExpose().Common.Items
	if parsed, ok := parseStringSlice(data["exposes"]); ok && len(parsed) > 0 {
		exposes = parsed
	}

	real := false
	if r, ok := data["real"].(bool); ok {
		real = r
	}

	updated := false
	for i, existing := range db.Devices {
		if types.NormalizeMACForTopic(existing.ID) == normalizedID {
			// Обновляем только изменяемые поля
			if db.Devices[i].Name != name {
				db.Devices[i].Name = name
				updated = true
			}
			
			if db.Devices[i].Active != device.IsOnline() {
				db.Devices[i].Active = device.IsOnline()
				updated = true
			}
			
			if db.Devices[i].Cloud != knownDevice.HOMEdCloud {
				db.Devices[i].Cloud = knownDevice.HOMEdCloud
				updated = true
			}
			
			if db.Devices[i].Discovery != knownDevice.HOMEdDiscovery {
				db.Devices[i].Discovery = knownDevice.HOMEdDiscovery
				updated = true
			}
			
			if db.Devices[i].Real != real {
				db.Devices[i].Real = real
				updated = true
			}
			
			// ✅ НИКОГДА НЕ УДАЛЯЕМ СУЩЕСТВУЮЩИЕ EXPOSES
			// Добавляем только новые, которых еще нет в списке
			existingExposes := make(map[string]bool)
			for _, exp := range existing.Exposes {
				existingExposes[exp] = true
			}
			
			for _, newExp := range exposes {
				if !existingExposes[newExp] {
					db.Devices[i].Exposes = append(db.Devices[i].Exposes, newExp)
					updated = true
				}
			}
			
			break
		}
	}

	if !updated {
		db.Devices = append(db.Devices, dbDevice{
			Active:    device.IsOnline(),
			Cloud:     knownDevice.HOMEdCloud,
			Discovery: knownDevice.HOMEdDiscovery,
			Exposes:   exposes,
			ID:        normalizedID,
			Name:      name,
			Real:      real,
		})
	}

	db.Timestamp = time.Now().Unix()
	db.Version = p.version

	return p.saveDeviceDatabase(db)
}

func parseStringSlice(value interface{}) ([]string, bool) {
	switch items := value.(type) {
	case []string:
		return items, true
	case []interface{}:
		result := make([]string, 0, len(items))
		for _, item := range items {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result, true
	default:
		return nil, false
	}
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
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

func (p *Publisher) RefreshKnownDevicesFromDatabase() error {
	return p.loadKnownDevicesFromDatabase()
}

func (p *Publisher) loadKnownDevicesFromDatabase() error {
	db, err := p.loadDeviceDatabase()
	if err != nil {
		return err
	}

	if len(db.Devices) == 0 {
		return nil
	}

	// Дедупликация устройств из базы данных перед загрузкой
	db.Devices = p.deduplicateDevices(db.Devices)

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

// deduplicateDevices — удаление дубликатов устройств по MAC-адресу
// Оставляет только первую запись для каждого уникального MAC
func (p *Publisher) deduplicateDevices(devices []dbDevice) []dbDevice {
	seen := make(map[string]bool)
	result := make([]dbDevice, 0, len(devices))
	
	for _, device := range devices {
		normalizedID := types.NormalizeMACForTopic(device.ID)
		if normalizedID == "" {
			continue
		}
		
		if !seen[normalizedID] {
			seen[normalizedID] = true
			result = append(result, device)
		} else {
			p.logger.Debug().
				Str("mac", normalizedID).
				Msg("Removing duplicate device from database")
		}
	}
	
	return result
}

func (p *Publisher) updateDeviceDatabase(device *types.Device) (bool, error) {
	if !p.hasUsefulDeviceData(device) {
		return false, nil
	}

	p.dbMu.Lock()
	defer p.dbMu.Unlock()

	db, err := p.loadDeviceDatabase()
	if err != nil {
		return false, err
	}

	// Дедупликация при загрузке - на случай если в базе уже есть дубликаты
	db.Devices = p.deduplicateDevices(db.Devices)

	normalizedID := types.NormalizeMACForTopic(device.MAC)
	if normalizedID == "" {
		normalizedID = device.MAC
	}

	exposes := device.GetHomedExpose().Common.Items

	// 🔄 Если устройство уже есть в БД — обновляем только exposes (добавляем новые)
	for i, existing := range db.Devices {
		if types.NormalizeMACForTopic(existing.ID) == normalizedID {
			existingExposes := make(map[string]bool)
			for _, exp := range existing.Exposes {
				existingExposes[exp] = true
			}

			updated := false
			for _, newExp := range exposes {
				if !existingExposes[newExp] {
					db.Devices[i].Exposes = append(db.Devices[i].Exposes, newExp)
					updated = true
				}
			}

			if updated {
				db.Timestamp = time.Now().Unix()
				db.Version = p.version
				if err := p.saveDeviceDatabase(db); err != nil {
					return false, err
				}
				p.logger.Debug().
					Str("mac", normalizedID).
					Msg("Updated exposes in database")
			} else {
				p.logger.Debug().
					Str("mac", normalizedID).
					Msg("Device already exists in database, exposes up to date")
			}
			return false, nil
		}
	}

	// Устройство не найдено в БД — добавляем, если permitJoin включён
	// или устройство уже есть в known_devices (известное устройство)
	_, isKnown := p.config.KnownDevices[normalizedID]
	if !p.IsPermitJoin() && !isKnown {
		return false, nil
	}

	name := device.Name
	if name == "" {
		name = normalizedID
	}

	// Добавляем новое устройство в базу
	db.Devices = append(db.Devices, dbDevice{
		Active:    device.IsOnline(),
		Cloud:     false,
		Discovery: false,
		Exposes:   exposes,
		ID:        normalizedID,
		Name:      name,
		Real:      false,
	})

	if p.config.KnownDevices == nil {
		p.config.KnownDevices = make(map[string]types.KnownDevice)
	}
	p.config.KnownDevices[normalizedID] = types.KnownDevice{
		Name:           name,
		HOMEdCloud:     false,
		HOMEdDiscovery: false,
	}
	p.config.KnownDevicesOrder = append(p.config.KnownDevicesOrder, normalizedID)

	db.Timestamp = time.Now().Unix()
	db.Version = p.version

	if err := p.saveDeviceDatabase(db); err != nil {
		return false, err
	}

	return true, nil
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

func (p *Publisher) ShouldProcessAdvertisement(mac string) bool {
	if p.IsPermitJoin() {
		return true
	}

	normalizedMAC := types.NormalizeMACForTopic(mac)
	if normalizedMAC == "" {
		return false
	}

	_, known := p.config.KnownDevices[normalizedMAC]
	return known
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

	// ✅ Всегда публикуем online статус при получении данных (асинхронно, чтобы не блокировать BLE обработчик)
	p.logger.Debug().Str("mac", mac).Msg("Publishing online status for active device")
	go func(macAddr string, lastSeen time.Time) {
		if err := p.publishDeviceStatus(macAddr, "online", lastSeen); err != nil {
			p.logger.Error().Err(err).Str("mac", macAddr).Msg("Failed to publish online status")
		}
	}(mac, device.GetLastSeen())

	// Сбрасываем флаг отправки оффлайн статуса когда устройство вернулось онлайн
	p.mu.Lock()
	delete(p.offlinePublished, mac)
	p.mu.Unlock()

	// Если устройство перешло из оффлайна — логируем это отдельно
	if wasOffline {
		p.logger.Info().Str("mac", mac).Msg("Device came online")
	}

	// Обновляем распарсенные значения
	for key, val := range parsed {
		device.SetParsedValue(key, val)
		// Сохраняем battery в device.Battery для GetFDFlat()
		if key == "battery" {
			if b, ok := val.Value.(int); ok {
				device.Battery = &b
			}
		}
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

	// ✅ Сначала обновляем exposes в БД, чтобы expose публиковался с полным набором
	newEntry, err := p.updateDeviceDatabase(device)
	if err != nil {
		p.logger.Error().Err(err).Str("mac", mac).Msg("Failed to update device in database")
	} else if newEntry {
		p.PublishBleStatusNow()
	} else {
		p.PublishBleStatusIfChanged()
	}

	// Публикуем в HOMEd стиле (асинхронно, чтобы не блокировать BLE обработчик)
	go func(macAddr string, advCopy types.Advertisement, parsedCopy map[string]types.ParsedValue, deviceCopy *types.Device) {
		if err := p.publishHomed(macAddr, advCopy, parsedCopy, deviceCopy); err != nil {
			p.logger.Error().Err(err).Str("mac", macAddr).Msg("Failed to publish advertisement")
		}
	}(mac, adv, parsed, device)

	// Публикуем expose для HOMEd (после обновления БД, чтобы набор полей был полным)
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
		// fd/{mac} — текущие значения как плоский JSON
		fdFlat := device.GetFDFlat()
		fdBytes, _ := json.Marshal(fdFlat)
		if err := p.client.PublishJSON(fmt.Sprintf("%s/fd/ble/%s", base, topicName), fdBytes, false); err != nil {
			return err
		}
	}

	return nil
}

// lastExposeHash — кэш хэшей последних опубликованных expose для каждого MAC
var lastExposeHash sync.Map

// publishExpose — публикация expose для HOMEd (публикуется только при изменении набора полей в БД)
func (p *Publisher) publishExpose(mac string, device *types.Device) error {
	// Всегда берем exposes из базы данных, чтобы набор полей был стабильным
	// и не менялся при получении частичных BLE пакетов
	db, err := p.loadDeviceDatabase()
	if err != nil {
		return err
	}

	normalizedID := types.NormalizeMACForTopic(mac)
	if normalizedID == "" {
		normalizedID = mac
	}

	var dbEntry *dbDevice
	for _, entry := range db.Devices {
		if types.NormalizeMACForTopic(entry.ID) == normalizedID {
			dbEntry = &entry
			break
		}
	}

	if dbEntry == nil || len(dbEntry.Exposes) == 0 {
		return nil
	}

	// Собираем только sensor поля (без "last")
	sensorItems := make([]string, 0)
	for _, item := range dbEntry.Exposes {
		if item != "last" && item != "rssi" {
			sensorItems = append(sensorItems, item)
		}
	}

	// Если нет сенсорных полей — не публикуем
	if len(sensorItems) == 0 {
		return nil
	}

	// Проверяем, изменился ли набор exposes с прошлого раза
	currentHash := fmt.Sprintf("%v", sensorItems)
	lastHashObj, _ := lastExposeHash.LoadOrStore(mac, "")
	lastHash, _ := lastHashObj.(string)

	if currentHash == lastHash {
		return nil // Набор полей не изменился — пропускаем
	}

	// Строим expose из данных базы
	items := make([]string, 0, len(dbEntry.Exposes))
	options := make(map[string]types.ExposeOption, len(dbEntry.Exposes))

	for _, item := range dbEntry.Exposes {
		if item == "last" {
			continue
		}
		items = append(items, item)

		unit := ""
		switch item {
		case "temperature":
			unit = "°C"
		case "humidity":
			unit = "%"
		case "battery":
			unit = "%"
		case "voltage":
			unit = "V"
		case "pressure":
			unit = "hPa"
		case "illuminance":
			unit = "lx"
		case "rssi":
			unit = "dBm"
		}

		options[item] = types.ExposeOption{
			State: "measurement",
			Type:  "sensor",
			Unit:  unit,
		}
	}

	expose := types.HomedExpose{
		Common: types.ExposeCommon{
			Items:   items,
			Options: options,
		},
	}

	base := p.config.MQTTPrefix
	topicName := p.config.GetDeviceTopicName(mac)

	exposeBytes, err := json.Marshal(expose)
	if err != nil {
		return err
	}

	topic := fmt.Sprintf("%s/expose/ble/%s", base, topicName)
	err = p.client.PublishJSON(topic, exposeBytes, true)

	if err == nil {
		lastExposeHash.Store(mac, currentHash)
		p.logger.Debug().Str("mac", mac).Strs("items", sensorItems).Msg("Expose published from database")
	}

	return err
}

// PublishStartupExpose — публикация expose на старте из базы данных
func (p *Publisher) PublishStartupExpose(mac string) error {
	db, err := p.loadDeviceDatabase()
	if err != nil {
		return err
	}

	normalizedID := types.NormalizeMACForTopic(mac)
	if normalizedID == "" {
		normalizedID = mac
	}

	// Ищем устройство в базе данных
	var dbEntry *dbDevice
	for _, entry := range db.Devices {
		if types.NormalizeMACForTopic(entry.ID) == normalizedID {
			dbEntry = &entry
			break
		}
	}

	if dbEntry == nil {
		return nil // Устройства нет в БД, expose будет опубликован при первом обнаружении
	}

	exposes := dbEntry.Exposes
	if len(exposes) == 0 {
		return nil // Нет exposes для публикации
	}

	// Строим expose из данных базы
	items := make([]string, 0, len(exposes))
	options := make(map[string]types.ExposeOption, len(exposes))

	for _, item := range exposes {
		if item == "last" {
			continue
		}

		items = append(items, item)

		unit := ""
		class := ""
		switch item {
		case "temperature":
			unit = "°C"
		case "humidity":
			unit = "%"
		case "battery":
			unit = "%"
		case "voltage":
			unit = "V"
		case "pressure":
			unit = "hPa"
		case "illuminance":
			unit = "lx"
		case "rssi":
			unit = "dBm"
		}

		options[item] = types.ExposeOption{
			Class: class,
			State: "measurement",
			Type:  "sensor",
			Unit:  unit,
		}
	}

	expose := types.HomedExpose{
		Common: types.ExposeCommon{
			Items:   items,
			Options: options,
		},
	}

	base := p.config.MQTTPrefix
	topicName := p.config.GetDeviceTopicName(mac)

	exposeBytes, err := json.Marshal(expose)
	if err != nil {
		return err
	}

	topic := fmt.Sprintf("%s/expose/ble/%s", base, topicName)
	if err := p.client.PublishJSON(topic, exposeBytes, true); err != nil {
		return err
	}

	// Инициализируем хэш expose, чтобы runtime publishExpose() не перезаписал
	// полный expose из БД частичными данными от первого BLE пакета
	sensorItems := make([]string, 0)
	for _, item := range exposes {
		if item != "last" && item != "rssi" {
			sensorItems = append(sensorItems, item)
		}
	}
	lastExposeHash.Store(mac, fmt.Sprintf("%v", sensorItems))

	return nil
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
// Если force=true, публикация выполняется независимо от хэша.
// Возвращает true если был опубликован новый статус, false если ничего не изменилось.
func (p *Publisher) publishBleStatus(force bool) (bool, error) {
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

		statusDevice := types.BleStatusDevice{
			Active:    true,
			Cloud:     knownDevice.HOMEdCloud,
			Discovery: knownDevice.HOMEdDiscovery,
			ID:        mac,
			Name:      knownDevice.Name,
			Real:      true,
		}

		// Всегда берем exposes из базы данных, чтобы набор полей был стабильным
		// и не менялся при получении частичных BLE пакетов
		db, err := p.loadDeviceDatabase()
		if err == nil {
			for _, dbDevice := range db.Devices {
				if types.NormalizeMACForTopic(dbDevice.ID) == mac {
					statusDevice.Exposes = dbDevice.Exposes
					statusDevice.Last = 0
					break
				}
			}
		}

		status.Devices = append(status.Devices, statusDevice)
	}

	// Сериализуем и считаем хэш только по устройствам и состоянию PermitJoin,
	// чтобы timestamp не ломал логику публикации при отсутствии изменений.
	hashSource := struct {
		Devices    []types.BleStatusDevice `json:"devices"`
		PermitJoin bool                    `json:"permitJoin"`
		Names      bool                    `json:"names"`
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

	// Если хэш не изменился и публикация не принудительная, пропускаем публикацию
	if !force && currentHash == p.lastBleStatusHash {
		return false, nil
	}

	statusBytes, err := json.Marshal(status)
	if err != nil {
		return false, err
	}

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
	published, err := p.publishBleStatus(false)
	if err != nil {
		p.logger.Error().Err(err).Msg("Failed to publish BLE status")
	} else if published {
		p.logger.Debug().Msg("BLE status was updated and published")
	}
}

func (p *Publisher) PublishBleStatusNow() {
	_, err := p.publishBleStatus(true)
	if err != nil {
		p.logger.Error().Err(err).Msg("Failed to publish BLE status now")
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

func (p *Publisher) PublishEvent(eventType string, payload interface{}) error {
	topic := fmt.Sprintf("%s/event/%s", p.config.Publish.BasePrefix, eventType)

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return p.client.PublishJSON(topic, jsonBytes, false)
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

			// ✅ Отправляем статус OFFLINE ТОЛЬКО ОДИН РАЗ при переходе
			p.mu.Lock()
			alreadySent := p.offlinePublished[mac]
			p.mu.Unlock()

			if !alreadySent {
				p.logger.Info().
					Str("mac", mac).
					Float64("elapsed", elapsed).
					Int("timeout", timeout).
					Msg("Device went offline (timeout)")

				if err := p.publishDeviceStatus(mac, "offline", lastSeen); err != nil {
					p.logger.Error().Err(err).Str("mac", mac).Msg("Failed to publish offline status")
				} else {
					p.mu.Lock()
					p.offlinePublished[mac] = true
					p.mu.Unlock()
				}
			}
		}
	}

	// Если only_known_devices=true, публикуем offline для всех известных устройств
	if p.config.OnlyKnownDevices {
		for mac := range p.config.KnownDevices {
			_, exists := p.devices[mac]
			if !exists {
				// ✅ Отправляем статус OFFLINE ТОЛЬКО ОДИН РАЗ для известных устройств
				p.mu.Lock()
				alreadySent := p.offlinePublished[mac]
				p.mu.Unlock()

				if !alreadySent {
					// Устройство не найдено в активных — публикуем offline
					if err := p.publishDeviceOffline(mac, &types.Device{
						MAC:      mac,
						LastSeen: time.Now(),
						Online:   false,
					}); err != nil {
						p.logger.Error().Err(err).Str("mac", mac).Msg("Failed to publish offline status for known device")
					} else {
						p.mu.Lock()
						p.offlinePublished[mac] = true
						p.mu.Unlock()
					}
				}
			}
		}
	}
}