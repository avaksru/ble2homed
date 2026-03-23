package mqtt

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/avaksru/ble2homed/pkg/types"
	"github.com/rs/zerolog"
)

// Publisher — публикация данных в MQTT с поддержкой разных режимов
type Publisher struct {
	client  *Client
	config  *types.Config
	logger  zerolog.Logger
	devices map[string]*types.Device
	mu      sync.RWMutex
}

// NewPublisher — создание нового publisher
func NewPublisher(client *Client, config *types.Config, logger zerolog.Logger) *Publisher {
	return &Publisher{
		client:  client,
		config:  config,
		logger:  logger.With().Str("component", "publisher").Logger(),
		devices: make(map[string]*types.Device),
	}
}

// GetOrCreateDevice — получение или создание устройства
func (p *Publisher) GetOrCreateDevice(mac string) *types.Device {
	p.mu.Lock()
	defer p.mu.Unlock()

	normalizedMAC := normalizeMACForTopic(mac)

	if device, exists := p.devices[normalizedMAC]; exists {
		return device
	}

	device := types.NewDevice(normalizedMAC)
	p.devices[normalizedMAC] = device
	p.logger.Info().Str("mac", normalizedMAC).Msg("New device discovered")
	return device
}

// GetDevice — получение устройства
func (p *Publisher) GetDevice(mac string) (*types.Device, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	normalizedMAC := normalizeMACForTopic(mac)
	device, exists := p.devices[normalizedMAC]
	return device, exists
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

// PublishAdvertisement — публикация advertising данных
// Выбирает режим публикации на основе конфигурации
func (p *Publisher) PublishAdvertisement(mac string, adv types.Advertisement, parsed map[string]types.ParsedValue) error {
	device := p.GetOrCreateDevice(mac)
	device.UpdateLastSeen()
	device.RSSI = adv.RSSI
	if adv.Name != "" {
		device.Name = adv.Name
	}

	// Обновляем распарсенные значения
	for key, val := range parsed {
		device.SetParsedValue(key, val)
	}

	// Публикуем в зависимости от режима
	switch p.config.Publish.Mode {
	case "homeassistant":
		return p.publishhomeassistant(mac, adv, parsed, device)
	case "homed":
		return p.publishHomed(mac, adv, parsed, device)
	case "both":
		if err := p.publishhomeassistant(mac, adv, parsed, device); err != nil {
			p.logger.Error().Err(err).Str("mac", mac).Msg("Failed to publish homeassistant style")
		}
		if err := p.publishHomed(mac, adv, parsed, device); err != nil {
			p.logger.Error().Err(err).Str("mac", mac).Msg("Failed to publish homed style")
		}
		return nil
	default:
		return fmt.Errorf("unknown publish mode: %s", p.config.Publish.Mode)
	}
}

// publishhomeassistant — публикация в стиле homeassistant
func (p *Publisher) publishhomeassistant(mac string, adv types.Advertisement, parsed map[string]types.ParsedValue, device *types.Device) error {
	base := p.config.Publish.BasePrefix
	macLower := strings.ToLower(mac)

	// presence/{mac}
	presence := "1"
	if !device.Online {
		presence = "0"
	}
	if err := p.client.Publish(fmt.Sprintf("%s/presence/%s", base, macLower), presence, p.config.Publish.RetainPresence); err != nil {
		return err
	}

	// advertise/{mac} — полный JSON
	advJSON := map[string]interface{}{
		"mac":          macLower,
		"name":         adv.Name,
		"rssi":         adv.RSSI,
		"manufacturer": adv.Manufacturer,
		"service_data": adv.ServiceData,
		"services":     adv.Services,
	}

	// Добавляем распарсенные значения в advertise JSON
	for key, val := range parsed {
		advJSON[key] = val.Value
	}

	advBytes, _ := json.Marshal(advJSON)
	if err := p.client.PublishJSON(fmt.Sprintf("%s/advertise/%s", base, macLower), advBytes, p.config.Retain); err != nil {
		return err
	}

	// advertise/{mac}/rssi
	if err := p.client.Publish(fmt.Sprintf("%s/advertise/%s/rssi", base, macLower), adv.RSSI, p.config.Retain); err != nil {
		return err
	}

	// advertise/{mac}/name
	if adv.Name != "" {
		if err := p.client.Publish(fmt.Sprintf("%s/advertise/%s/name", base, macLower), adv.Name, p.config.Retain); err != nil {
			return err
		}
	}

	// advertise/{mac}/{field} — отдельные топики для каждого распарсенного значения
	for key, val := range parsed {
		topic := fmt.Sprintf("%s/advertise/%s/%s", base, macLower, key)

		// Специальные топики для manufacturer и service
		if strings.HasPrefix(key, "manufacturer/") {
			// manufacturer/{company_id}
			if err := p.client.Publish(fmt.Sprintf("%s/advertise/%s/%s", base, macLower, key), val.Value, p.config.Retain); err != nil {
				p.logger.Warn().Err(err).Str("topic", topic).Msg("Failed to publish manufacturer topic")
			}
			continue
		}

		if strings.HasPrefix(key, "service/") {
			// service/{uuid}
			if err := p.client.Publish(fmt.Sprintf("%s/advertise/%s/%s", base, macLower, key), val.Value, p.config.Retain); err != nil {
				p.logger.Warn().Err(err).Str("topic", topic).Msg("Failed to publish service topic")
			}
			continue
		}

		// Обычные топики (temp, humidity, battery и т.д.)
		if val.Value != nil {
			if err := p.client.Publish(topic, val.Value, p.config.Retain); err != nil {
				p.logger.Warn().Err(err).Str("topic", topic).Msg("Failed to publish value topic")
			}
		}
	}

	return nil
}

// publishHomed — публикация в стиле HOMEd
func (p *Publisher) publishHomed(mac string, adv types.Advertisement, parsed map[string]types.ParsedValue, device *types.Device) error {
	base := p.config.Publish.BasePrefix
	macLower := strings.ToLower(mac)

	// device/{mac} — информация об устройстве
	deviceInfo := map[string]interface{}{
		"last_seen": device.LastSeen.Unix(),
		"online":    device.Online,
	}
	if device.Name != "" {
		deviceInfo["name"] = device.Name
	}
	deviceBytes, _ := json.Marshal(deviceInfo)
	if err := p.client.PublishJSON(fmt.Sprintf("%s/device/%s", base, macLower), deviceBytes, p.config.Retain); err != nil {
		return err
	}

	// expose/{mac} — список сенсоров и свойств
	exposeList := device.GetExposeList()
	exposeBytes, _ := json.Marshal(exposeList)
	if err := p.client.PublishJSON(fmt.Sprintf("%s/expose/%s", base, macLower), exposeBytes, p.config.Retain); err != nil {
		return err
	}

	// fd/{mac} — текущие значения как плоский JSON
	fdFlat := device.GetFDFlat()
	fdBytes, _ := json.Marshal(fdFlat)
	if err := p.client.PublishJSON(fmt.Sprintf("%s/fd/%s", base, macLower), fdBytes, p.config.Retain); err != nil {
		return err
	}

	// fd/{mac}/{field} — отдельные топики для recorder
	for key, value := range fdFlat {
		topic := fmt.Sprintf("%s/fd/%s/%s", base, macLower, key)
		if err := p.client.Publish(topic, value, p.config.Retain); err != nil {
			p.logger.Warn().Err(err).Str("topic", topic).Msg("Failed to publish fd field")
		}
	}

	return nil
}

// PublishHistoryValue — публикация исторического значения
func (p *Publisher) PublishHistoryValue(mac, field, interval string, value float64) error {
	base := p.config.Publish.BasePrefix
	macLower := strings.ToLower(mac)

	topic := fmt.Sprintf("%s/hist/%s/%s/%s", base, interval, macLower, field)
	return p.client.Publish(topic, value, false)
}

// PublishCommandResponse — публикация ответа на команду
func (p *Publisher) PublishCommandResponse(mac, service, char string, data []byte, responseType string) error {
	base := p.config.Publish.BasePrefix
	macLower := strings.ToLower(mac)

	var topic string
	switch responseType {
	case "data":
		topic = fmt.Sprintf("%s/fd/%s/data/%s/%s", base, macLower, service, char)
	case "written":
		topic = fmt.Sprintf("%s/fd/%s/written/%s/%s", base, macLower, service, char)
	case "pong":
		topic = fmt.Sprintf("%s/fd/%s/pong", base, macLower)
	default:
		return fmt.Errorf("unknown response type: %s", responseType)
	}

	return p.client.Publish(topic, data, false)
}

// normalizeMACForTopic — нормализация MAC-адреса для использования в топиках (lowercase)
func normalizeMACForTopic(mac string) string {
	mac = strings.ToLower(mac)
	mac = strings.ReplaceAll(mac, "-", ":")
	return mac
}
