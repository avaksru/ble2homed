package ble

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/avaksru/ble2homed/pkg/types"
	"github.com/rs/zerolog"
	"tinygo.org/x/bluetooth"
)

// timeShard — один сегмент сегментированной карты времени
type timeShard struct {
	mu     sync.RWMutex
	values map[string]time.Time
}

// shardedTimeMap — сегментированная карта для минимальных конфликтов блокировок
type shardedTimeMap struct {
	shards [16]timeShard
}

func newShardedTimeMap() *shardedTimeMap {
	m := &shardedTimeMap{}
	for i := range m.shards {
		m.shards[i].values = make(map[string]time.Time)
	}
	return m
}

func (m *shardedTimeMap) getShard(key string) *timeShard {
	hash := uint8(0)
	if len(key) > 0 {
		hash = key[0]
	}
	return &m.shards[hash%16]
}

func (m *shardedTimeMap) Get(key string) (time.Time, bool) {
	shard := m.getShard(key)
	shard.mu.RLock()
	t, ok := shard.values[key]
	shard.mu.RUnlock()
	return t, ok
}

func (m *shardedTimeMap) Set(key string, t time.Time) {
	shard := m.getShard(key)
	shard.mu.Lock()
	shard.values[key] = t
	shard.mu.Unlock()
}

func (m *shardedTimeMap) Cleanup(olderThan time.Time) int {
	removed := 0
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		for k, v := range shard.values {
			if v.Before(olderThan) {
				delete(shard.values, k)
				removed++
			}
		}
		shard.mu.Unlock()
	}
	return removed
}

// Scanner — BLE сканер устройств
type Scanner struct {
	config         *types.BLEConfig
	logger         zerolog.Logger
	adapter        *bluetooth.Adapter
	filterMACs     map[string]bool
	onAdv          func(types.Advertisement)
	mu             sync.RWMutex
	running        bool
	cancel         context.CancelFunc
	macCache       sync.Map // lock-free кэш нормализованных MAC-адресов
	advPool        sync.Pool // пул объектов Advertisement
	bytesPool      sync.Pool // пул байтовых буферов для manufacturer data
	lastAdvTime    atomic.Pointer[time.Time] // время последнего пакета (lock-free)
	lastDeviceTime *shardedTimeMap // сегментированная карта дедубликации
	watchdogCancel context.CancelFunc
	debugEnabled   bool
}

// NewScanner — создание нового BLE сканера
func NewScanner(config *types.BLEConfig, logger zerolog.Logger) (*Scanner, error) {
	// Создаем BLE адаптер
	adapter := bluetooth.DefaultAdapter

	// Включаем адаптер
	if err := adapter.Enable(); err != nil {
		return nil, fmt.Errorf("failed to enable BLE adapter: %w", err)
	}

	// Создаем map фильтров MAC-адресов
	filterMACs := make(map[string]bool)
	for _, mac := range config.FilterMACs {
		normalized := normalizeMAC(mac)
		filterMACs[normalized] = true
	}

	now := time.Now()

	s := &Scanner{
		config:         config,
		logger:         logger.With().Str("component", "ble_scanner").Logger(),
		adapter:        adapter,
		filterMACs:     filterMACs,
		lastDeviceTime: newShardedTimeMap(),
		debugEnabled:   logger.GetLevel() <= zerolog.DebugLevel,
		advPool: sync.Pool{
			New: func() interface{} {
				return &types.Advertisement{}
			},
		},
		bytesPool: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 0, 128)
				return &buf
			},
		},
	}
	s.lastAdvTime.Store(&now)

	return s, nil
}

// SetAdvertisementHandler — установка обработчика advertising данных
func (s *Scanner) SetAdvertisementHandler(handler func(types.Advertisement)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAdv = handler
}

// Start — запуск сканирования
func (s *Scanner) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scanner already running")
	}

	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true
	s.mu.Unlock()

		s.logger.Info().
			Int("filter_count", len(s.filterMACs)).
			Msg("Starting BLE scanner")

	// Запускаем вотчдог для детектирования зависаний
	watchdogCtx, watchdogCancel := context.WithCancel(ctx)
	s.watchdogCancel = watchdogCancel
	go s.watchdogLoop(watchdogCtx)

	// Запускаем сканирование в отдельной горутине
	go s.scanLoop(ctx)

	return nil
}

// Stop — остановка сканирования
func (s *Scanner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}

	s.logger.Info().Msg("Stopping BLE scanner...")

	// Сначала отменяем контекст чтобы остановить все циклы
	if s.cancel != nil {
		s.cancel()
	}

	s.running = false
	s.mu.Unlock()

	// Даем небольшую паузу чтобы цикл сканирования успел выйти
	time.Sleep(50 * time.Millisecond)

	// Теперь останавливаем сканирование на адаптере
	s.mu.Lock()
	if err := s.adapter.StopScan(); err != nil {
		// Идемпотентный вызов - игнорируем ошибку что сканирование уже остановлено
		if !strings.Contains(err.Error(), "there is no scan in progress") {
			s.logger.Warn().Err(err).Msg("Failed to stop scan on adapter")
		}
	}
	s.mu.Unlock()

	// Останавливаем вотчдог
	if s.watchdogCancel != nil {
		s.watchdogCancel()
	}

	// Ждем с таймаутом полной остановки всех операций
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

waitLoop:
	for {
		select {
		case <-timeout:
			s.logger.Warn().Msg("Timeout waiting for scanner stop")
			break waitLoop
		case <-ticker.C:
			s.mu.RLock()
			if !s.running {
				s.mu.RUnlock()
				break waitLoop
			}
			s.mu.RUnlock()
		}
	}

	s.logger.Info().Msg("✅ BLE scanner stopped completely, bluetoothd resources released")
}

// scanLoop — основной цикл сканирования
func (s *Scanner) scanLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("Scan loop stopped by context")
			return
		default:
			if err := s.doScan(ctx); err != nil {
				// Игнорируем ошибку отмены контекста - это нормальный сценарий остановки
				if errors.Is(err, context.Canceled) {
					s.logger.Debug().Msg("Scan stopped gracefully")
					return
				}
				
				s.logger.Error().Err(err).Msg("Scan error, retrying in 5 seconds")
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
					continue
				}
			}

	// Пауза между циклами сканирования
	pauseDuration := time.Duration(s.config.RestartPause) * time.Second

	if pauseDuration > 0 {
		s.logger.Info().
			Dur("pause", pauseDuration).
			Msg("Pausing before next scan cycle")

		select {
		case <-ctx.Done():
			s.logger.Info().Msg("Scan loop stopped by context during pause")
			return
		case <-time.After(pauseDuration):
			continue
		}
	}

	// Если пауза 0 - запускаем сканирование сразу же без задержки
	s.logger.Debug().Msg("No pause between scan cycles, restarting immediately")
		}
	}
}

// doScan — выполнение одного цикла сканирования
func (s *Scanner) doScan(ctx context.Context) error {
	// Создаем контекст с таймаутом для этого цикла сканирования
	scanDuration := time.Duration(s.config.ScanInterval) * time.Second
	var scanCtx context.Context
	var scanCancel context.CancelFunc

	if scanDuration <= 0 {
		// 0 = непрерывное сканирование без таймаута
		scanCtx = ctx
		scanCancel = func() {}
	} else {
		scanCtx, scanCancel = context.WithTimeout(ctx, scanDuration)
	}
	defer scanCancel()

	// Обработчик advertising пакетов
	advHandler := func(adapter *bluetooth.Adapter, scanResult bluetooth.ScanResult) {
		// Проверяем фильтр MAC-адресов с кэшированием
		addr := scanResult.Address.String()
		if len(s.filterMACs) > 0 {
			normalizedAddr := s.normalizeMACCached(addr)
			if !s.filterMACs[normalizedAddr] {
				return
			}
		}

		// ✅ Фильтр по минимальному RSSI
		if int(scanResult.RSSI) < -90 {
			return
		}

		// ✅ Дедубликация: игнорируем пакеты от одного устройства чаще чем раз в 1 секунду
		now := time.Now()
		lastTime, exists := s.lastDeviceTime.Get(addr)
		if exists && now.Sub(lastTime) < 1*time.Second {
			return
		}
		s.lastDeviceTime.Set(addr, now)

		// Получаем объект из пула
		advPtr := s.advPool.Get().(*types.Advertisement)
		adv := advPtr
		adv.Addr = addr
		adv.RSSI = int(scanResult.RSSI)
		adv.Name = ""
		adv.Manufacturer = adv.Manufacturer[:0]
		adv.ServiceData = adv.ServiceData[:0]

		// Извлекаем имя устройства
		if scanResult.LocalName() != "" {
			adv.Name = scanResult.LocalName()
		}

		// Извлекаем manufacturer data
		if manufacturerData := scanResult.ManufacturerData(); len(manufacturerData) > 0 {
			for _, md := range manufacturerData {
				if md.CompanyID != 0 {
					// Преобразуем в []byte формат: [company_id_lo, company_id_hi, data...]
					data := make([]byte, 2+len(md.Data))
					data[0] = byte(md.CompanyID)
					data[1] = byte(md.CompanyID >> 8)
					copy(data[2:], md.Data)
					adv.Manufacturer = data
					break
				}
			}
		}

		// Извлекаем service data
		if serviceData := scanResult.ServiceData(); len(serviceData) > 0 {
			for _, sd := range serviceData {
				adv.ServiceData = append(adv.ServiceData, types.ServiceData{
					UUID: sd.UUID.String(),
					Data: sd.Data,
				})
			}
		}

		s.lastAdvTime.Store(&now)

		s.logger.Debug().
			Str("mac", adv.Addr).
			Str("name", adv.Name).
			Int("rssi", adv.RSSI).
			Int("manufacturer_len", len(adv.Manufacturer)).
			Int("service_data_count", len(adv.ServiceData)).
			Msg("BLE advertisement received")

		// Вызываем обработчик
		s.mu.RLock()
		handler := s.onAdv
		s.mu.RUnlock()

		if handler != nil {
			handler(*adv)
		}

		// Возвращаем объект в пул
		s.advPool.Put(adv)
	}

	// Запускаем сканирование
	if scanDuration > 0 {
		s.logger.Info().
			Dur("duration", scanDuration).
			Msg("Starting BLE scan")
	} else {
		s.logger.Info().Msg("Starting continuous BLE scan (no timeout)")
	}

	// Запускаем сканирование в горутине
	errChan := make(chan error, 1)
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		err := s.adapter.Scan(advHandler)
		if err != nil && scanCtx.Err() == nil {
			errChan <- fmt.Errorf("scan failed: %w", err)
		}
		close(errChan)
	}()

	// Ждем либо завершения таймаута, либо ошибки, либо отмены контекста
	select {
	case <-scanCtx.Done():
		// Таймаут сканирования - останавливаем сканирование
		s.logger.Info().Msg("Scan interval completed, stopping scan")
		s.adapter.StopScan()
		// Ждем завершения горутины сканирования
		<-scanDone
		return nil
	case err := <-errChan:
		if err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
		// Внешний контекст отменен
		s.adapter.StopScan()
		<-scanDone
		return ctx.Err()
	}
}

// ScanOnce — сканирование в течение указанного времени
func (s *Scanner) ScanOnce(duration time.Duration) ([]types.Advertisement, error) {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var results []types.Advertisement
	var mu sync.Mutex

	advHandler := func(adapter *bluetooth.Adapter, scanResult bluetooth.ScanResult) {
		// Проверяем фильтр MAC-адресов
		addr := scanResult.Address.String()
		if len(s.filterMACs) > 0 {
			normalizedAddr := normalizeMAC(addr)
			if !s.filterMACs[normalizedAddr] {
				return
			}
		}

		adv := types.Advertisement{
			Addr: addr,
			RSSI: int(scanResult.RSSI),
		}

		if scanResult.LocalName() != "" {
			adv.Name = scanResult.LocalName()
		}

		if manufacturerData := scanResult.ManufacturerData(); len(manufacturerData) > 0 {
			for _, md := range manufacturerData {
				if md.CompanyID != 0 {
					data := make([]byte, 2+len(md.Data))
					data[0] = byte(md.CompanyID)
					data[1] = byte(md.CompanyID >> 8)
					copy(data[2:], md.Data)
					adv.Manufacturer = data
					break
				}
			}
		}

		if serviceData := scanResult.ServiceData(); len(serviceData) > 0 {
			for _, sd := range serviceData {
				adv.ServiceData = append(adv.ServiceData, types.ServiceData{
					UUID: sd.UUID.String(),
					Data: sd.Data,
				})
			}
		}

		mu.Lock()
		// Проверяем, есть ли уже это устройство
		for i, existing := range results {
			if existing.Addr == adv.Addr {
				results[i] = adv // Обновляем
				mu.Unlock()
				return
			}
		}
		results = append(results, adv)
		mu.Unlock()
	}

	// Запускаем сканирование в горутине
	errChan := make(chan error, 1)
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		err := s.adapter.Scan(advHandler)
		if err != nil && ctx.Err() == nil {
			errChan <- fmt.Errorf("scan failed: %w", err)
		}
		close(errChan)
	}()

	// Ждем либо завершения таймаута, либо ошибки
	select {
	case <-ctx.Done():
		// Таймаут сканирования - останавливаем сканирование
		s.adapter.StopScan()
		// Ждем завершения горутины сканирования
		<-scanDone
		return results, nil
	case err := <-errChan:
		if err != nil {
			return nil, err
		}
		return results, nil
	}
}

// IsRunning — проверка, запущен ли сканер
func (s *Scanner) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// normalizeMAC — нормализация MAC-адреса
func normalizeMAC(mac string) string {
	// Удаляем разделители и приводим к uppercase
	mac = strings.ToUpper(mac)
	mac = strings.ReplaceAll(mac, "-", "")
	mac = strings.ReplaceAll(mac, ":", "")
	mac = strings.ReplaceAll(mac, ".", "")

	// Добавляем двоеточия
	if len(mac) == 12 {
		return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
			mac[0:2], mac[2:4], mac[4:6],
			mac[6:8], mac[8:10], mac[10:12])
	}

	return mac
}

// normalizeMACCached — нормализация MAC-адреса с кэшированием (lock-free)
func (s *Scanner) normalizeMACCached(mac string) string {
	if cached, ok := s.macCache.Load(mac); ok {
		return cached.(string)
	}

	normalized := normalizeMAC(mac)
	s.macCache.Store(mac, normalized)

	return normalized
}

// watchdogLoop - вотчдог который отслеживает зависание bluetooth стека
func (s *Scanner) watchdogLoop(ctx context.Context) {
	const watchdogTimeout = 600 * time.Second
	checkTicker := time.NewTicker(60 * time.Second)
	defer checkTicker.Stop()

	s.logger.Info().Dur("timeout", watchdogTimeout).Msg("Watchdog started")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("Watchdog stopped")
			return
		case <-checkTicker.C:
			lastAdv := s.lastAdvTime.Load()
			timeSinceLastAdv := time.Since(*lastAdv)

			if timeSinceLastAdv > watchdogTimeout {
				s.logger.Error().
					Dur("since_last", timeSinceLastAdv).
					Msg("❌ NO BLE PACKETS RECEIVED FOR TOO LONG! Bluetooth stack probably hanged. Restarting adapter...")

				// Принудительно останавливаем сканирование и перезапускаем адаптер
				s.mu.Lock()
				if err := s.adapter.StopScan(); err != nil {
					s.logger.Warn().Err(err).Msg("Failed to stop scan during recovery")
				}

				// Переинициализируем адаптер полностью
				if err := s.adapter.Enable(); err != nil {
					s.logger.Error().Err(err).Msg("Failed to re-enable BLE adapter during recovery")
				}
				now := time.Now()
				s.lastAdvTime.Store(&now)
				s.mu.Unlock()

				s.logger.Info().Msg("✅ BLE adapter restarted successfully, scan will resume automatically")
			}
		}
	}
}
