package mqtt

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	mqttlib "github.com/eclipse/paho.mqtt.golang"
	"github.com/rs/zerolog"
)

// Subscriber — подписка на MQTT топики команд
type Subscriber struct {
	client          *Client
	publisher       *Publisher
	base            string
	logger          zerolog.Logger
	getPropsLimiter sync.Map // rate limiter для getProperties логов (deviceID -> time.Time)
}

type bleCommand struct {
	Action string                 `json:"action"`
	Device string                 `json:"device,omitempty"`
	ID     string                 `json:"id,omitempty"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

func (c bleCommand) Identifier() string {
	if c.Device != "" {
		return c.Device
	}
	if c.ID != "" {
		return c.ID
	}
	if c.Data != nil {
		if nestedID, ok := c.Data["id"].(string); ok && nestedID != "" {
			return nestedID
		}
		if nestedDevice, ok := c.Data["device"].(string); ok && nestedDevice != "" {
			return nestedDevice
		}
	}
	return ""
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

		// Обработка команд из топика /command/ble
		if topic == fmt.Sprintf("%s/command/ble", s.base) {
			var cmd bleCommand

			if err := json.Unmarshal(payload, &cmd); err != nil {
				s.logger.Error().Err(err).Msg("Failed to parse BLE command payload")
				return
			}

			deviceID := cmd.Identifier()

			switch cmd.Action {
			case "togglePermitJoin":
				s.logger.Info().Msg("Received togglePermitJoin command")
				if err := s.publisher.SetPermitJoin(!s.publisher.IsPermitJoin()); err != nil {
					s.logger.Error().Err(err).Msg("Failed to toggle permitJoin")
					return
				}
				s.publisher.PublishBleStatusIfChanged()
				return

			case "removeDevice":
				if deviceID == "" {
					s.logger.Warn().Msg("removeDevice command missing device identifier")
					return
				}
				s.logger.Info().Str("device", deviceID).Msg("Received removeDevice command")
				removedID, err := s.publisher.RemoveDevice(deviceID)
				if err != nil {
					s.logger.Error().Err(err).Str("device", deviceID).Msg("Failed to remove device")
					return
				}
s.publisher.PublishEvent("ble", map[string]string{"device": removedID, "event": "removed"})
s.publisher.PublishBleStatusNow()
				return

			case "updateDevice":
				if deviceID == "" {
					s.logger.Warn().Msg("updateDevice command missing device identifier")
					return
				}
				if len(cmd.Data) == 0 {
					s.logger.Warn().Str("device", deviceID).Msg("updateDevice command missing data payload")
					return
				}
				s.logger.Info().Str("device", deviceID).Msg("Received updateDevice command")

				if err := s.publisher.UpdateDevice(deviceID, cmd.Data); err != nil {
					s.logger.Error().Err(err).Str("device", deviceID).Msg("Failed to update device")
					return
				}

s.publisher.PublishEvent("ble", map[string]string{"device": deviceID, "event": "updated"})
s.publisher.PublishBleStatusNow()
				return

			case "getProperties":
				if deviceID == "" {
					s.logger.Warn().Msg("getProperties command missing device identifier")
					return
				}
				s.logger.Info().Str("device", deviceID).Msg("Received getProperties command")

				device, exists := s.publisher.GetDevice(deviceID)
				if !exists {
					// Rate limit: логируем не чаще 1 раза в минуту для каждого device
					if lastLog, loaded := s.getPropsLimiter.LoadOrStore(deviceID, time.Now()); loaded {
						if time.Since(lastLog.(time.Time)) >= 1*time.Minute {
							s.getPropsLimiter.Store(deviceID, time.Now())
							s.logger.Debug().Str("device", deviceID).Msg("Device not found for getProperties request (device may not be discovered yet)")
						}
					} else {
						s.logger.Debug().Str("device", deviceID).Msg("Device not found for getProperties request (device may not be discovered yet)")
					}
					return
				}

				// Публикуем асинхронно, чтобы не блокировать MQTT обработчик
				deviceCopy := device
				go func() {
					fdData := deviceCopy.GetFDFlat()
					fdBytes, err := json.Marshal(fdData)
					if err != nil {
						s.logger.Error().Err(err).Str("device", deviceID).Msg("Failed to marshal device properties")
						return
					}

					topicName := s.publisher.Config().GetDeviceTopicName(deviceCopy.MAC)
					fdTopic := fmt.Sprintf("%s/fd/ble/%s", s.publisher.Config().Publish.BasePrefix, topicName)

					if err := s.client.PublishJSON(fdTopic, fdBytes, false); err != nil {
						s.logger.Error().Err(err).Str("topic", fdTopic).Msg("Failed to publish device properties")
					} else {
						s.logger.Debug().Str("topic", fdTopic).Msg("Device properties published successfully")
					}
				}()

				return

			default:
				s.logger.Warn().Str("action", cmd.Action).Msg("Unknown BLE command action")
				return
			}
		}
		s.logger.Warn().Str("topic", topic).Msg("Unknown command topic format")
	}

	topics := map[string]byte{
		fmt.Sprintf("%s/command/ble", s.base):  1,		
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
