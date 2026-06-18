package parser

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/avaksru/ble2homed/pkg/types"
)

// Xiaomi service data UUID
const XiaomiServiceDataUUID = "FE95"

// Xiaomi product IDs
const (
	XiaomiProductLYWSD02    = 0x045b // LYWSD02
	XiaomiProductLYWSD03MMC = 0x055b // LYWSD03MMC
	XiaomiProductLYWSDCGQ   = 0x01aa // Mijia Temperature Humidity Sensor
	XiaomiProductHHCCJCY01  = 0x0098 // HHCCJCY01
	XiaomiProductGCLS002    = 0x03bc // GCLS002
	XiaomiProductLYWSD02MMC = 0x045b // Same as LYWSD02
)

// Xiaomi event types
const (
	XiaomiEventTempHumidity   = 0x100d // 4109 - Temperature and humidity combined
	XiaomiEventTemperature    = 0x1004 // 4100 - Temperature
	XiaomiEventHumidity       = 0x1006 // 4102 - Humidity
	XiaomiEventBattery        = 0x100a // 4106 - Battery
	XiaomiEventIlluminance    = 0x1007 // 4103 - Illuminance
	XiaomiEventMoisture       = 0x1008 // 4104 - Moisture
	XiaomiEventFertility      = 0x1009 // 4105 - Fertility
	XiaomiEventButton         = 0x1001 // Button event
	XiaomiEventMovingWithLight = 0x000f // Moving with light
)

// Frame control flags
const (
	XiaomiFrameCtrlIsFactoryNew  = 1 << 0
	XiaomiFrameCtrlIsConnected   = 1 << 1
	XiaomiFrameCtrlIsCentral     = 1 << 2
	XiaomiFrameCtrlIsEncrypted   = 1 << 3
	XiaomiFrameCtrlHasMacAddress = 1 << 4
	XiaomiFrameCtrlHasCapabilities = 1 << 5
	XiaomiFrameCtrlHasEvent      = 1 << 6
	XiaomiFrameCtrlHasCustomData = 1 << 7
	XiaomiFrameCtrlHasSubtitle   = 1 << 8
	XiaomiFrameCtrlHasBinding    = 1 << 9
)

// XiaomiParser handles parsing of Xiaomi BLE advertisement data
type XiaomiParser struct {
	BindKey string
}

// NewXiaomiParser creates a new Xiaomi parser
func NewXiaomiParser(bindKey string) *XiaomiParser {
	return &XiaomiParser{BindKey: bindKey}
}

// ParseXiaomiServiceData parses Xiaomi service data (FE95)
func ParseXiaomiServiceData(data []byte, bindKey string, now time.Time) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue)
	
	if len(data) < 5 {
		return result
	}

	parser := NewXiaomiParser(bindKey)
	parsed := parser.parse(data, now)
	
	for k, v := range parsed {
		result[k] = v
	}
	
	return result
}

// parse parses the Xiaomi service data
func (p *XiaomiParser) parse(data []byte, now time.Time) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue)
	
	// Parse frame control (2 bytes, little endian)
	frameControl := binary.LittleEndian.Uint16(data[0:2])
	
	// Parse version (4 bits)
	version := (data[2] >> 4) & 0x0F
	
	// Parse product ID (2 bytes, little endian)
	productID := binary.LittleEndian.Uint16(data[2:4])
	
	// Parse frame counter (1 byte)
	frameCounter := data[4]
	
	offset := 5
	
	// Parse MAC address if present
	var macAddress string
	if frameControl & XiaomiFrameCtrlHasMacAddress != 0 {
		if len(data) < offset+6 {
			return result
		}
		macBytes := data[offset : offset+6]
		// Reverse MAC bytes for display
		for i := 0; i < 3; i++ {
			macBytes[i], macBytes[5-i] = macBytes[5-i], macBytes[i]
		}
		macAddress = hex.EncodeToString(macBytes)
		offset += 6
	}
	
	// Parse capabilities if present
	if frameControl & XiaomiFrameCtrlHasCapabilities != 0 {
		if len(data) < offset+1 {
			return result
		}
		_ = data[offset] // capabilities byte, not used currently
		offset += 1
	}
	
	// Check if data is encrypted
	isEncrypted := frameControl & XiaomiFrameCtrlIsEncrypted != 0
	
	// Check if has event
	hasEvent := frameControl & XiaomiFrameCtrlHasEvent != 0
	if !hasEvent {
		return result
	}
	
	// Parse event type (2 bytes, little endian)
	if len(data) < offset+2 {
		return result
	}
	eventType := binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2
	
	// Parse event length (1 byte)
	if len(data) < offset+1 {
		return result
	}
	eventLength := data[offset]
	offset += 1
	
	// Check if we have enough data for the event
	if len(data) < offset+int(eventLength) {
		return result
	}
	
	eventData := data[offset : offset+int(eventLength)]
	
	// Decrypt if needed
	if isEncrypted {
		if p.BindKey == "" {
			// Cannot decrypt without bindkey
			return result
		}
		
		decrypted, err := p.decryptPayload(data, frameCounter, productID, macAddress, eventData, version)
		if err != nil {
			return result
		}
		eventData = decrypted
	}
	
	// Parse event data based on event type
	switch eventType {
	case XiaomiEventTempHumidity:
		if len(eventData) >= 4 {
			temp := float64(int16(binary.LittleEndian.Uint16(eventData[0:2]))) / 10.0
			humidity := float64(binary.LittleEndian.Uint16(eventData[2:4])) / 10.0
			result["temp"] = types.ParsedValue{Value: temp, Unit: "°C", Type: "temp", Source: "Xiaomi LYWSD02", Timestamp: now}
			result["humidity"] = types.ParsedValue{Value: humidity, Unit: "%", Type: "humidity", Source: "Xiaomi LYWSD02", Timestamp: now}
		}
	case XiaomiEventTemperature:
		if len(eventData) >= 2 {
			temp := float64(int16(binary.LittleEndian.Uint16(eventData[0:2]))) / 10.0
			result["temp"] = types.ParsedValue{Value: temp, Unit: "°C", Type: "temp", Source: "Xiaomi LYWSD02", Timestamp: now}
		}
	case XiaomiEventHumidity:
		if len(eventData) >= 2 {
			humidity := float64(binary.LittleEndian.Uint16(eventData[0:2])) / 10.0
			result["humidity"] = types.ParsedValue{Value: humidity, Unit: "%", Type: "humidity", Source: "Xiaomi LYWSD02", Timestamp: now}
		}
	case XiaomiEventBattery:
		if len(eventData) >= 1 {
			battery := int(eventData[0])
			result["battery"] = types.ParsedValue{Value: battery, Unit: "%", Type: "battery", Source: "Xiaomi LYWSD02", Timestamp: now}
		}
	}
	
	return result
}

// decryptPayload decrypts the encrypted Xiaomi payload
func (p *XiaomiParser) decryptPayload(fullData []byte, frameCounter byte, productID uint16, macAddress string, eventData []byte, version byte) ([]byte, error) {
	bindKeyBytes, err := hex.DecodeString(p.BindKey)
	if err != nil {
		return nil, fmt.Errorf("invalid bindkey: %w", err)
	}
	
	if len(bindKeyBytes) != 16 {
		return nil, fmt.Errorf("bindkey must be 16 bytes (32 hex chars)")
	}
	
	msgLength := len(fullData)
	eventOffset := msgLength - len(eventData)
	
	// For legacy format (version <= 3) vs new format
	if version <= 3 {
		return p.decryptLegacyPayload(bindKeyBytes, fullData, frameCounter, productID, eventOffset)
	}
	
	return p.decryptNewPayload(bindKeyBytes, fullData, frameCounter, productID, macAddress, eventOffset, msgLength)
}

// decryptLegacyPayload decrypts legacy format (version <= 3)
func (p *XiaomiParser) decryptLegacyPayload(bindKey []byte, fullData []byte, frameCounter byte, productID uint16, eventOffset int) ([]byte, error) {
	if len(fullData) < eventOffset+6 {
		return nil, fmt.Errorf("insufficient data for legacy decryption")
	}
	
	encryptedPayload := fullData[eventOffset : eventOffset+6]
	
	// Build nonce for legacy format
	// nonce = 0x01 + fullData[0:5] + fullData[len-4:len-1] + fullData[5:10] + 0x0001
	var nonce []byte
	nonce = append(nonce, 0x01)
	nonce = append(nonce, fullData[0:5]...)
	if len(fullData) >= 4 {
		nonce = append(nonce, fullData[len(fullData)-4:len(fullData)-1]...)
	}
	if len(fullData) >= 10 {
		nonce = append(nonce, fullData[5:10]...)
	}
	nonce = append(nonce, 0x00, 0x01)
	
	// Build key for legacy format
	// key = bindKey[0:6] + 0x8d3d3c97 + bindKey[6:]
	var key []byte
	key = append(key, bindKey[0:6]...)
	key = append(key, 0x8d, 0x3d, 0x3c, 0x97)
	key = append(key, bindKey[6:]...)
	
	// Decrypt using AES-CTR
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	
	stream := cipher.NewCTR(block, nonce)
	decrypted := make([]byte, len(encryptedPayload))
	stream.XORKeyStream(decrypted, encryptedPayload)
	
	return decrypted, nil
}

// decryptNewPayload decrypts new format (version >= 4)
// Note: LYWSD02 with factory firmware typically uses version <= 3 (legacy format)
// For new format (version >= 4), AES-CCM is needed which is not in Go standard library.
// This is a placeholder that returns an error - the legacy path handles LYWSD02.
func (p *XiaomiParser) decryptNewPayload(bindKey []byte, fullData []byte, frameCounter byte, productID uint16, macAddress string, eventOffset int, msgLength int) ([]byte, error) {
	return nil, fmt.Errorf("AES-CCM decryption not implemented for new format (version >= 4)")
}

// XiaomiProductName returns the product name for a given product ID
func XiaomiProductName(productID uint16) string {
	names := map[uint16]string{
		0x005d: "HHCCPOT002",
		0x0098: "HHCCJCY01",
		0x01d8: "Stratos",
		0x0153: "YEE-RC",
		0x02df: "JQJCY01YM",
		0x03b6: "YLKG08YL",
		0x03bc: "GCLS002",
		0x040a: "WX08ZM",
		0x045b: "LYWSD02",
		0x055b: "LYWSD03MMC",
		0x0576: "CGD1",
		0x0347: "CGG1",
		0x01aa: "LYWSDCGQ",
		0x03dd: "MUE4094RT",
		0x07f6: "MJYD02YLA",
		0x0387: "MHOC401",
	}
	
	if name, ok := names[productID]; ok {
		return name
	}
	return fmt.Sprintf("Unknown (0x%04x)", productID)
}