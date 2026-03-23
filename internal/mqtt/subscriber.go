package mqtt

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	mqttlib "github.com/eclipse/paho.mqtt.golang"
	"github.com/rs/zerolog"
)

// Subscriber — подписка на MQTT топики команд
type Subscriber struct {
	client    *Client
	publisher *Publisher
	base      string
	logger    zerolog.Logger
}

// CommandHandler — интерфейс обработчика команд
type CommandHandler interface {
	HandleWrite(mac, service, char string, payload []byte) error
	HandleRead(mac, service, char string) ([]byte, error)
	HandleNotify(mac, service, char string, enable bool) error
	HandlePing(mac string) error
}

// NewSubscriber — создание нового subscriber
func NewSubscriber(client *Client, publisher *Publisher, base string, logger zerolog.Logger) *Subscriber {
	return &Subscriber{
		client:    client,
		publisher: publisher,
		base:      base,
		logger:    logger.With().Str("component", "subscriber").Logger(),
	}
}

// SubscribeCommands — подписка на топики команд
func (s *Subscriber) SubscribeCommands(handler CommandHandler) error {
	writePattern := regexp.MustCompile(fmt.Sprintf(`^%s/td/([^/]+)/write/([^/]+)/([^/]+)$`, regexp.QuoteMeta(s.base)))
	readPattern := regexp.MustCompile(fmt.Sprintf(`^%s/td/([^/]+)/read/([^/]+)/([^/]+)$`, regexp.QuoteMeta(s.base)))
	notifyPattern := regexp.MustCompile(fmt.Sprintf(`^%s/td/([^/]+)/notify/([^/]+)/([^/]+)$`, regexp.QuoteMeta(s.base)))
	pingPattern := regexp.MustCompile(fmt.Sprintf(`^%s/td/([^/]+)/ping$`, regexp.QuoteMeta(s.base)))

	messageHandler := func(client mqttlib.Client, msg mqttlib.Message) {
		topic := msg.Topic()
		payload := msg.Payload()

		s.logger.Debug().
			Str("topic", topic).
			Int("payload_len", len(payload)).
			Msg("Received command message")

		if matches := writePattern.FindStringSubmatch(topic); matches != nil {
			mac, service, char := matches[1], matches[2], matches[3]
			s.logger.Info().Str("mac", mac).Str("service", service).Str("char", char).Msg("Write command")
			if err := handler.HandleWrite(mac, service, char, payload); err != nil {
				s.logger.Error().Err(err).Msg("Failed to handle write command")
			} else {
				s.publisher.PublishCommandResponse(mac, service, char, payload, "written")
			}
			return
		}

		if matches := readPattern.FindStringSubmatch(topic); matches != nil {
			mac, service, char := matches[1], matches[2], matches[3]
			s.logger.Info().Str("mac", mac).Str("service", service).Str("char", char).Msg("Read command")
			data, err := handler.HandleRead(mac, service, char)
			if err != nil {
				s.logger.Error().Err(err).Msg("Failed to handle read command")
			} else {
				s.publisher.PublishCommandResponse(mac, service, char, data, "data")
			}
			return
		}

		if matches := notifyPattern.FindStringSubmatch(topic); matches != nil {
			mac, service, char := matches[1], matches[2], matches[3]
			enable := parseBoolPayload(payload)
			s.logger.Info().Str("mac", mac).Str("service", service).Str("char", char).Bool("enable", enable).Msg("Notify command")
			if err := handler.HandleNotify(mac, service, char, enable); err != nil {
				s.logger.Error().Err(err).Msg("Failed to handle notify command")
			}
			return
		}

		if matches := pingPattern.FindStringSubmatch(topic); matches != nil {
			mac := matches[1]
			s.logger.Info().Str("mac", mac).Msg("Ping command")
			if err := handler.HandlePing(mac); err != nil {
				s.logger.Error().Err(err).Msg("Failed to handle ping command")
			} else {
				s.publisher.PublishCommandResponse(mac, "", "", nil, "pong")
			}
			return
		}

		s.logger.Warn().Str("topic", topic).Msg("Unknown command topic format")
	}

	topics := map[string]byte{
		fmt.Sprintf("%s/td/+/write/+/+", s.base):  1,
		fmt.Sprintf("%s/td/+/read/+/+", s.base):   1,
		fmt.Sprintf("%s/td/+/notify/+/+", s.base): 1,
		fmt.Sprintf("%s/td/+/ping", s.base):       1,
	}

	if err := s.client.SubscribeMultiple(topics, messageHandler); err != nil {
		return fmt.Errorf("failed to subscribe to command topics: %w", err)
	}

	s.logger.Info().Str("base", s.base).Msg("Subscribed to command topics")
	return nil
}

// SubscribeCustom — подписка на произвольный топик
func (s *Subscriber) SubscribeCustom(topic string, handler mqttlib.MessageHandler) error {
	return s.client.Subscribe(topic, handler)
}

func parseBoolPayload(payload []byte) bool {
	str := strings.TrimSpace(strings.ToLower(string(payload)))
	switch str {
	case "1", "true", "on", "yes", "enable":
		return true
	default:
		return false
	}
}

// ParsePayloadToBytes — преобразование payload в []byte
func ParsePayloadToBytes(payload string) ([]byte, error) {
	payload = strings.TrimSpace(payload)
	if strings.HasPrefix(payload, "0x") || strings.HasPrefix(payload, "0X") {
		hexStr := strings.TrimPrefix(strings.TrimPrefix(payload, "0x"), "0X")
		return hex.DecodeString(hexStr)
	}
	return []byte(payload), nil
}
