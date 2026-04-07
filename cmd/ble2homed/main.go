package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/avaksru/ble2homed/internal/ble"
	"github.com/avaksru/ble2homed/internal/config"
	"github.com/avaksru/ble2homed/internal/discovery"
	"github.com/avaksru/ble2homed/internal/history"
	"github.com/avaksru/ble2homed/internal/mqtt"
	"github.com/avaksru/ble2homed/internal/parser"
	"github.com/avaksru/ble2homed/internal/web"
	"github.com/avaksru/ble2homed/pkg/types"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	version   = "0.0.3"
	buildTime = "unknown"
)

func main() {
	// Флаги командной строки
	configPath := flag.String("config", "", "Path to config file (yaml or json)")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ble2homed version %s (built: %s)\n", version, buildTime)
		os.Exit(0)
	}

	// Загрузка конфигурации
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Настройка логирования
	setupLogger(cfg.Log.Level)

	log.Info().
		Str("version", version).
		Str("base_prefix", cfg.Publish.BasePrefix).
		Msg("Starting ble2homed")

	// Контекст для graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Канал для сигналов ОС
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Запуск приложения
	if err := run(ctx, cfg); err != nil {
		log.Fatal().Err(err).Msg("Failed to start application")
	}

	// Ожидание сигнала завершения
	sig := <-sigChan
	log.Info().Str("signal", sig.String()).Msg("Received shutdown signal")

	// Graceful shutdown
	log.Info().Msg("Starting graceful shutdown...")
	
	// Публикуем offline статус для всех устройств
	log.Info().Msg("Publishing offline status for all devices...")
	
	cancel()
	time.Sleep(2 * time.Second)
	log.Info().Msg("Shutdown complete")
}

// setupLogger — настройка zerolog
func setupLogger(level string) {
	zerolog.TimeFieldFormat = time.RFC3339

	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Красивый вывод в консоль
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}

// run — основная логика приложения
func run(ctx context.Context, cfg *types.Config) error {
	var wg sync.WaitGroup

	// 1. Создаем MQTT клиент
	mqttConfig := &mqtt.MQTTConfig{
		Broker:   cfg.MQTT.Broker,
		Username: cfg.MQTT.Username,
		Password: cfg.MQTT.Password,
		ClientID: cfg.MQTT.ClientID,
		QoS:      cfg.MQTT.QoS,
		Prefix:   cfg.MQTTPrefix,
	}

	mqttClient := mqtt.NewClient(mqttConfig, log.Logger)

	// Подключаемся к MQTT с повторными попытками
	if err := connectWithRetry(ctx, mqttClient, 20*time.Second); err != nil {
		return fmt.Errorf("failed to connect to MQTT after retries: %w", err)
	}
	defer mqttClient.Disconnect()

	// 2. Создаем publisher
	publisher := mqtt.NewPublisher(mqttClient, cfg, log.Logger)

	// 3. Создаем discovery
	hadiscovery := discovery.NewDiscovery(mqttClient, log.Logger)

	// 4. Создаем history manager
	historyConfig := history.HistoryConfig{
		Enabled:   cfg.History.Enabled,
		Intervals: cfg.History.Intervals,
	}
	historyManager := history.NewManager(historyConfig, log.Logger)

	// 5. Создаем subscriber для команд
	subscriber := mqtt.NewSubscriber(mqttClient, publisher, cfg.Publish.BasePrefix, log.Logger)

	// Обработчик команд BLE
	commandHandler := &BLECommandHandler{
		publisher: publisher,
		logger:    log.Logger,
	}

	if err := subscriber.SubscribeCommands(commandHandler); err != nil {
		log.Warn().Err(err).Msg("Failed to subscribe to commands")
	}

	// 6. Создаем BLE сканер
	scanner, err := ble.NewScanner(&cfg.BLE, log.Logger)
	if err != nil {
		return fmt.Errorf("failed to create BLE scanner: %w", err)
	}

	// Обработчик advertising данных
	scanner.SetAdvertisementHandler(func(adv types.Advertisement) {
		// Фильтр: если only_known_devices = true, пропускаем неизвестные устройства
		if cfg.OnlyKnownDevices {
			normalizedMAC := types.NormalizeMACForTopic(adv.Addr)
			if _, known := cfg.KnownDevices[normalizedMAC]; !known {
				log.Debug().
					Str("mac", adv.Addr).
					Msg("Skipping unknown device (only_known_devices=true)")
				return
			}
		}

		// Парсим данные
		parsed := parser.ParseBLEData(adv)

		log.Debug().
			Str("mac", adv.Addr).
			Str("name", adv.Name).
			Int("rssi", adv.RSSI).
			Int("parsed_count", len(parsed)).
			Msg("BLE advertisement processed")

		// Публикуем через publisher
		if err := publisher.PublishAdvertisement(adv.Addr, adv, parsed); err != nil {
			log.Error().Err(err).Str("mac", adv.Addr).Msg("Failed to publish advertisement")
			return
		}

		// Добавляем в историю
		for key, val := range parsed {
			if val.Value != nil {
				if f, ok := toFloat64(val.Value); ok {
					historyManager.AddValue(adv.Addr, key, f)
				}
			}
		}

		// Публикация discovery (периодически)
		device, exists := publisher.GetDevice(adv.Addr)
		if exists {
			hadiscovery.PublishDiscovery(device, cfg.Publish.BasePrefix)
		}
	})

	// Запускаем BLE сканер
	if err := scanner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start BLE scanner: %w", err)
	}
	defer scanner.Stop()

	// 7. Запускаем веб-сервер
	webServer := web.NewServer(&cfg.Web, publisher, log.Logger)
	if err := webServer.Start(); err != nil {
		log.Warn().Err(err).Msg("Failed to start web server")
	}
	defer webServer.Stop()

	// 8. Публикуем availability
	hadiscovery.PublishAvailability(cfg.Publish.BasePrefix)

	// 9. Периодические задачи
	wg.Add(1)
	go func() {
		defer wg.Done()
		periodicTasks(ctx, publisher, hadiscovery, historyManager, cfg)
	}()

	log.Info().Msg("ble2homed is running")
	wg.Wait()
	return nil
}

// periodicTasks — периодические задачи
func periodicTasks(ctx context.Context, publisher *mqtt.Publisher, hadiscovery *discovery.Discovery, historyManager *history.Manager, cfg *types.Config) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	historyTicker := time.NewTicker(10 * time.Second)
	defer historyTicker.Stop()

	cleanupTicker := time.NewTicker(1 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Публикация discovery для всех устройств
			for _, device := range publisher.GetAllDevices() {
				hadiscovery.PublishDiscovery(device, cfg.Publish.BasePrefix)
			}
			// Публикация availability
			hadiscovery.PublishAvailability(cfg.Publish.BasePrefix)
			// Проверка устройств на offline по presence_timeout
			publisher.CheckOfflineDevices()

		case <-historyTicker.C:
		// Публикация исторических данных
		for mac, device := range publisher.GetAllDevices() {
			averages := historyManager.GetAllAverages(mac)
			for field, intervals := range averages {
				for interval, avg := range intervals {
					publisher.PublishHistoryValue(mac, field, interval, avg)
				}
			}
			_ = device
		}

		case <-cleanupTicker.C:
			// Очистка старых данных (7 дней)
			historyManager.Cleanup(7 * 24 * time.Hour)
		}
	}
}

// BLECommandHandler — обработчик команд BLE
type BLECommandHandler struct {
	publisher *mqtt.Publisher
	logger    zerolog.Logger
}

func (h *BLECommandHandler) HandleWrite(mac, service, char string, payload []byte) error {
	h.logger.Info().
		Str("mac", mac).
		Str("service", service).
		Str("char", char).
		Int("payload_len", len(payload)).
		Msg("BLE write command")

	// TODO: Реализовать BLE write через подключение к устройству
	return fmt.Errorf("BLE write not implemented yet")
}

func (h *BLECommandHandler) HandleRead(mac, service, char string) ([]byte, error) {
	h.logger.Info().
		Str("mac", mac).
		Str("service", service).
		Str("char", char).
		Msg("BLE read command")

	// TODO: Реализовать BLE read через подключение к устройству
	return nil, fmt.Errorf("BLE read not implemented yet")
}

func (h *BLECommandHandler) HandleNotify(mac, service, char string, enable bool) error {
	h.logger.Info().
		Str("mac", mac).
		Str("service", service).
		Str("char", char).
		Bool("enable", enable).
		Msg("BLE notify command")

	// TODO: Реализовать BLE notify через подключение к устройству
	return fmt.Errorf("BLE notify not implemented yet")
}

func (h *BLECommandHandler) HandlePing(mac string) error {
	h.logger.Info().Str("mac", mac).Msg("Ping command")
	return nil
}

// toFloat64 — преобразование interface{} в float64
func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case int32:
		return float64(val), true
	default:
		return 0, false
	}
}

// connectWithRetry — подключение к MQTT с повторными попытками
func connectWithRetry(ctx context.Context, client *mqtt.Client, retryInterval time.Duration) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := client.Connect(); err == nil {
				log.Info().Msg("Successfully connected to MQTT broker")
				return nil
			}

			log.Warn().Dur("retry_interval", retryInterval).Msg("Failed to connect to MQTT, retrying in 20 seconds")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryInterval):
				continue
			}
		}
	}
}
