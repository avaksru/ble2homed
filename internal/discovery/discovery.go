package discovery

import (
	"github.com/avaksru/ble2homed/internal/mqtt"
	"github.com/rs/zerolog"
)

// Discovery — заглушка (Home Assistant MQTT Discovery удалён)
type Discovery struct {
	client *mqtt.Client
	logger zerolog.Logger
}

// NewDiscovery — создание нового discovery (заглушка)
func NewDiscovery(client *mqtt.Client, logger zerolog.Logger) *Discovery {
	return &Discovery{
		client: client,
		logger: logger.With().Str("component", "discovery").Logger(),
	}
}

// PublishDiscovery — заглушка (Home Assistant Discovery удалён)
func (d *Discovery) PublishDiscovery(device interface{}, basePrefix string) {
	// Home Assistant Discovery удалён — используется только HOMEd
}

// PublishAvailability — публикация топика доступности
func (d *Discovery) PublishAvailability(basePrefix string) {
	topic := basePrefix + "/status"
	if err := d.client.Publish(topic, "online", false); err != nil {
		d.logger.Error().Err(err).Msg("Failed to publish availability")
	}
}
