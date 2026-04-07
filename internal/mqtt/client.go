package mqtt

import (
	"fmt"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/rs/zerolog"
)

// Client — MQTT клиент
type Client struct {
	config    *MQTTConfig
	logger    zerolog.Logger
	client    mqtt.Client
	mu        sync.RWMutex
	connected bool
	onConnect func()
	onMessage func(topic string, payload []byte)
}

// MQTTConfig — конфигурация MQTT
type MQTTConfig struct {
	Broker   string
	Username string
	Password string
	ClientID string
	QoS      byte
	Prefix   string
}

// NewClient — создание нового MQTT клиента
func NewClient(config *MQTTConfig, logger zerolog.Logger) *Client {
	return &Client{
		config: config,
		logger: logger.With().Str("component", "mqtt").Logger(),
	}
}

// SetConnectHandler — установка обработчика подключения
func (c *Client) SetConnectHandler(handler func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onConnect = handler
}

// SetMessageHandler — установка обработчика сообщений
func (c *Client) SetMessageHandler(handler func(topic string, payload []byte)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMessage = handler
}

// Connect — подключение к MQTT брокеру
func (c *Client) Connect() error {
	opts := mqtt.NewClientOptions().
		AddBroker(c.config.Broker).
		SetClientID(c.config.ClientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(10 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second)

	if c.config.Username != "" {
		opts.SetUsername(c.config.Username)
	}
	if c.config.Password != "" {
		opts.SetPassword(c.config.Password)
	}

	// Настройка Last Will and Testament (LWT)
	if c.config.Prefix != "" {
		willTopic := fmt.Sprintf("%s/service/ble", c.config.Prefix)
		willPayload := `{"status":"offline"}`
		opts.SetWill(willTopic, willPayload, c.config.QoS, true)
		c.logger.Info().Str("topic", willTopic).Msg("LWT configured")
	}

	// Обработчик подключения
	opts.SetOnConnectHandler(func(client mqtt.Client) {
		c.mu.Lock()
		c.connected = true
		handler := c.onConnect
		c.mu.Unlock()

		c.logger.Info().Str("broker", c.config.Broker).Msg("Connected to MQTT broker")

		// Публикуем статус online при подключении
		if err := c.PublishOnlineStatus(); err != nil {
			c.logger.Error().Err(err).Msg("Failed to publish online status")
		}

		if handler != nil {
			handler()
		}
	})

	// Обработчик потери соединения
	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()

		c.logger.Error().Err(err).Msg("Connection to MQTT broker lost")
	})

	// Обработчик сообщений (глобальный, если нужен)
	opts.SetDefaultPublishHandler(func(client mqtt.Client, msg mqtt.Message) {
		c.mu.RLock()
		handler := c.onMessage
		c.mu.RUnlock()

		if handler != nil {
			handler(msg.Topic(), msg.Payload())
		}
	})

	c.client = mqtt.NewClient(opts)

	c.logger.Info().Str("broker", c.config.Broker).Str("client_id", c.config.ClientID).Msg("Connecting to MQTT broker")

	token := c.client.Connect()
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to connect to MQTT broker: %w", token.Error())
	}

	// Синхронно устанавливаем флаг подключения
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	return nil
}

// Disconnect — отключение от MQTT брокера
func (c *Client) Disconnect() {
	if c.client != nil && c.client.IsConnected() {
		// При нормальном отключении брокер не отправляет LWT, поэтому публикуем статус offline явно
		if c.config.Prefix != "" {
			topic := fmt.Sprintf("%s/service/ble", c.config.Prefix)
			payload := []byte(`{"status":"offline"}`)
			if err := c.PublishJSON(topic, payload, true); err != nil {
				c.logger.Error().Err(err).Msg("Failed to publish offline status on disconnect")
			}
			// Небольшая пауза чтобы сообщение успело уйти до отключения
			time.Sleep(100 * time.Millisecond)
		}

		c.client.Disconnect(250)
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
		c.logger.Info().Msg("Disconnected from MQTT broker")
	}
}

// Publish — публикация сообщения
func (c *Client) Publish(topic string, payload interface{}, retain bool) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	var payloadBytes []byte
	switch v := payload.(type) {
	case string:
		payloadBytes = []byte(v)
	case []byte:
		payloadBytes = v
	default:
		// Для других типов используем fmt.Sprintf
		payloadBytes = []byte(fmt.Sprintf("%v", v))
	}

	token := c.client.Publish(topic, c.config.QoS, retain, payloadBytes)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish to %s: %w", topic, token.Error())
	}

	c.logger.Debug().
		Str("topic", topic).
		Int("payload_len", len(payloadBytes)).
		Bool("retain", retain).
		Msg("Published message")

	return nil
}

// PublishJSON — публикация JSON сообщения
func (c *Client) PublishJSON(topic string, jsonBytes []byte, retain bool) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	token := c.client.Publish(topic, c.config.QoS, retain, jsonBytes)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish JSON to %s: %w", topic, token.Error())
	}

	c.logger.Debug().
		Str("topic", topic).
		Int("payload_len", len(jsonBytes)).
		Bool("retain", retain).
		Msg("Published JSON message")

	return nil
}

// Subscribe — подписка на топик
func (c *Client) Subscribe(topic string, handler mqtt.MessageHandler) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	token := c.client.Subscribe(topic, c.config.QoS, handler)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", topic, token.Error())
	}

	c.logger.Info().Str("topic", topic).Msg("Subscribed to topic")
	return nil
}

// SubscribeMultiple — подписка на несколько топиков
func (c *Client) SubscribeMultiple(topics map[string]byte, handler mqtt.MessageHandler) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	token := c.client.SubscribeMultiple(topics, handler)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to subscribe to topics: %w", token.Error())
	}

	for topic := range topics {
		c.logger.Info().Str("topic", topic).Msg("Subscribed to topic")
	}
	return nil
}

// Unsubscribe — отписка от топика
func (c *Client) Unsubscribe(topics ...string) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	token := c.client.Unsubscribe(topics...)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to unsubscribe: %w", token.Error())
	}

	for _, topic := range topics {
		c.logger.Info().Str("topic", topic).Msg("Unsubscribed from topic")
	}
	return nil
}

// IsConnected — проверка подключения
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected && c.client != nil && c.client.IsConnected()
}

// GetClientID — получение client ID
func (c *Client) GetClientID() string {
	return c.config.ClientID
}

// PublishOnlineStatus — публикация статуса "online" в топик service/ble
func (c *Client) PublishOnlineStatus() error {
	if c.config.Prefix == "" {
		return nil
	}
	topic := fmt.Sprintf("%s/service/ble", c.config.Prefix)
	payload := []byte(`{"status":"online"}`)
	return c.PublishJSON(topic, payload, true)
}
