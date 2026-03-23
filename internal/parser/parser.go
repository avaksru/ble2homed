package parser

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/avaksru/ble2homed/pkg/types"
)

// Известные UUID сервисов GATT
const (
	ServiceBattery     = "180F"
	ServiceTemperature = "1809"
	ServiceHumidity    = "181A"

	CharBatteryLevel = "2A19"
	CharTemperature  = "2A6E"
	CharHumidity     = "2A6F"
	CharPressure     = "2A6D"

	// Espruino/Puck.js company ID
	CompanyEspruino = 0x0590
)

// ParseBLEData — полный парсинг BLE advertising данных
func ParseBLEData(adv types.Advertisement) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue)
	now := time.Now()

	// Парсинг manufacturer specific data
	if len(adv.Manufacturer) >= 2 {
		companyID := binary.LittleEndian.Uint16(adv.Manufacturer[0:2])
		companyHex := fmt.Sprintf("%04X", companyID)

		// Espruino/Puck.js JSON5 парсинг
		if int(companyID) == CompanyEspruino && len(adv.Manufacturer) > 2 {
			parsed := parseEspruinoJSON(adv.Manufacturer[2:], now)
			for k, v := range parsed {
				result[k] = v
			}
		}

		// Сохраняем raw manufacturer data
		result["manufacturer/"+companyHex] = types.ParsedValue{
			Value:     hex.EncodeToString(adv.Manufacturer),
			Type:      "manufacturer",
			Source:    "manufacturer",
			Timestamp: now,
		}
	}

	// Парсинг service data
	for _, sd := range adv.ServiceData {
		uuid := strings.ToUpper(sd.UUID)

		// Известные сервисы
		switch uuid {
		case ServiceTemperature:
			if temp, ok := parseTemperature(sd.Data); ok {
				result["temp"] = types.ParsedValue{
					Value:     temp,
					Unit:      "°C",
					Type:      "temp",
					Source:    "service_data",
					Timestamp: now,
				}
			}
		case ServiceHumidity:
			if humidity, ok := parseHumidity(sd.Data); ok {
				result["humidity"] = types.ParsedValue{
					Value:     humidity,
					Unit:      "%",
					Type:      "humidity",
					Source:    "service_data",
					Timestamp: now,
				}
			}
		case ServiceBattery:
			if battery, ok := parseBattery(sd.Data); ok {
				result["battery"] = types.ParsedValue{
					Value:     battery,
					Unit:      "%",
					Type:      "battery",
					Source:    "service_data",
					Timestamp: now,
				}
			}
		default:
			// Попытка распознать по UUID характеристики
			parsed := parseServiceDataByUUID(uuid, sd.Data, now)
			for k, v := range parsed {
				result[k] = v
			}
		}

		// Сохраняем raw service data
		result["service/"+uuid] = types.ParsedValue{
			Value:     parseServiceDataToJSON(sd.Data),
			Type:      "service_data",
			Source:    "service_data",
			Timestamp: now,
		}
	}

	// Попытка распознать Eddystone
	if eddystone := parseEddystone(adv); len(eddystone) > 0 {
		for k, v := range eddystone {
			result[k] = v
		}
	}

	// Попытка распознать iBeacon
	if ibeacon := parseIBeacon(adv); len(ibeacon) > 0 {
		for k, v := range ibeacon {
			result[k] = v
		}
	}

	return result
}

// parseEspruinoJSON — парсинг JSON5 данных от Espruino/Puck.js
// Формат: {"t":22.4,"h":54} или {"temp":22.4}
func parseEspruinoJSON(data []byte, now time.Time) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue)

	// Преобразуем в строку и пытаемся найти известные паттерны
	str := string(data)

	// Простой парсинг ключевых значений
	pairs := extractKeyValuePairs(str)

	for key, value := range pairs {
		switch strings.ToLower(key) {
		case "t", "temp", "temperature":
			if f, ok := parseFloat(value); ok {
				result["temp"] = types.ParsedValue{
					Value:     f,
					Unit:      "°C",
					Type:      "temp",
					Source:    "espruino",
					Timestamp: now,
				}
			}
		case "h", "hum", "humidity":
			if f, ok := parseFloat(value); ok {
				result["humidity"] = types.ParsedValue{
					Value:     f,
					Unit:      "%",
					Type:      "humidity",
					Source:    "espruino",
					Timestamp: now,
				}
			}
		case "b", "bat", "battery":
			if f, ok := parseFloat(value); ok {
				result["battery"] = types.ParsedValue{
					Value:     int(f),
					Unit:      "%",
					Type:      "battery",
					Source:    "espruino",
					Timestamp: now,
				}
			}
		case "p", "pres", "pressure":
			if f, ok := parseFloat(value); ok {
				result["pressure"] = types.ParsedValue{
					Value:     f,
					Unit:      "hPa",
					Type:      "pressure",
					Source:    "espruino",
					Timestamp: now,
				}
			}
		case "l", "lux", "light", "illuminance":
			if f, ok := parseFloat(value); ok {
				result["illuminance"] = types.ParsedValue{
					Value:     f,
					Unit:      "lx",
					Type:      "illuminance",
					Source:    "espruino",
					Timestamp: now,
				}
			}
		}
	}

	return result
}

// extractKeyValuePairs — простое извлечение пар ключ:значение из JSON-подобной строки
func extractKeyValuePairs(s string) map[string]string {
	result := make(map[string]string)

	// Удаляем пробелы и фигурные скобки
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")

	// Разделяем по запятым
	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		// Разделяем по двоеточию
		kv := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(kv) == 2 {
			key := strings.TrimSpace(kv[0])
			key = strings.Trim(key, "\"'")
			value := strings.TrimSpace(kv[1])
			value = strings.Trim(value, "\"'")
			result[key] = value
		}
	}

	return result
}

// parseTemperature — парсинг температуры из service data (UUID 1809/2A6E)
// Формат: sint16 в десятых долях градуса Цельсия
func parseTemperature(data []byte) (float64, bool) {
	if len(data) < 2 {
		return 0, false
	}

	raw := int16(binary.LittleEndian.Uint16(data[0:2]))
	temp := float64(raw) / 10.0

	return temp, true
}

// parseHumidity — парсинг влажности из service data (UUID 181A/2A6F)
// Формат: uint16 в десятых долах процента
func parseHumidity(data []byte) (float64, bool) {
	if len(data) < 2 {
		return 0, false
	}

	raw := binary.LittleEndian.Uint16(data[0:2])
	humidity := float64(raw) / 100.0

	return humidity, true
}

// parseBattery — парсинг уровня батареи (UUID 180F/2A19)
// Формат: uint8, проценты 0-100
func parseBattery(data []byte) (int, bool) {
	if len(data) < 1 {
		return 0, false
	}

	return int(data[0]), true
}

// parseServiceDataByUUID — попытка распарсить service data по UUID характеристики
func parseServiceDataByUUID(uuid string, data []byte, now time.Time) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue)

	switch uuid {
	case CharTemperature:
		if temp, ok := parseTemperature(data); ok {
			result["temp"] = types.ParsedValue{
				Value:     temp,
				Unit:      "°C",
				Type:      "temp",
				Source:    "gatt",
				Timestamp: now,
			}
		}
	case CharHumidity:
		if humidity, ok := parseHumidity(data); ok {
			result["humidity"] = types.ParsedValue{
				Value:     humidity,
				Unit:      "%",
				Type:      "humidity",
				Source:    "gatt",
				Timestamp: now,
			}
		}
	case CharPressure:
		if len(data) >= 4 {
			pressure := float64(binary.LittleEndian.Uint32(data[0:4])) / 10.0
			result["pressure"] = types.ParsedValue{
				Value:     pressure,
				Unit:      "Pa",
				Type:      "pressure",
				Source:    "gatt",
				Timestamp: now,
			}
		}
	}

	return result
}

// parseEddystone — парсинг Eddystone beacon данных
func parseEddystone(adv types.Advertisement) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue)
	now := time.Now()

	// Eddystone использует service UUID 0xFEAA
	for _, sd := range adv.ServiceData {
		if !strings.EqualFold(sd.UUID, "FEAA") || len(sd.Data) < 2 {
			continue
		}

		frameType := sd.Data[0]

		switch frameType {
		case 0x10: // Eddystone-URL
			if len(sd.Data) > 1 {
				txPower := int8(sd.Data[1])
				result["eddystone_tx_power"] = types.ParsedValue{
					Value:     txPower,
					Unit:      "dBm",
					Type:      "tx_power",
					Source:    "eddystone",
					Timestamp: now,
				}

				if url := decodeEddystoneURL(sd.Data[2:]); url != "" {
					result["eddystone_url"] = types.ParsedValue{
						Value:     url,
						Type:      "url",
						Source:    "eddystone",
						Timestamp: now,
					}
				}
			}
		case 0x20: // Eddystone-TLM
			if len(sd.Data) >= 14 {
				// Температура (если есть)
				tempRaw := binary.BigEndian.Uint16(sd.Data[2:4])
				if tempRaw != 0x8000 { // 0x8000 = нет датчика
					temp := int16(tempRaw)
					result["eddystone_temp"] = types.ParsedValue{
						Value:     float64(temp) / 256.0,
						Unit:      "°C",
						Type:      "temp",
						Source:    "eddystone",
						Timestamp: now,
					}
				}
			}
		}
	}

	return result
}

// decodeEddystoneURL — декодирование URL из Eddystone
func decodeEddystoneURL(data []byte) string {
	if len(data) < 1 {
		return ""
	}

	// Схемы URL
	schemes := []string{
		"http://www.",
		"https://www.",
		"http://",
		"https://",
	}

	// Расширения
	extensions := []string{
		".com/", ".org/", ".edu/", ".net/", ".info/", ".biz/", ".gov/",
		".com", ".org", ".edu", ".net", ".info", ".biz", ".gov",
	}

	schemeIdx := int(data[0])
	if schemeIdx >= len(schemes) {
		return ""
	}

	url := schemes[schemeIdx]

	for i := 1; i < len(data); i++ {
		b := data[i]
		if b < byte(len(extensions)) {
			url += extensions[b]
		} else {
			url += string(b)
		}
	}

	return url
}

// parseIBeacon — парсинг iBeacon данных
func parseIBeacon(adv types.Advertisement) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue)
	now := time.Now()

	if len(adv.Manufacturer) < 25 {
		return result
	}

	// Apple company ID = 0x004C
	companyID := binary.LittleEndian.Uint16(adv.Manufacturer[0:2])
	if companyID != 0x004C {
		return result
	}

	// iBeacon type = 0x02, length = 0x15
	if adv.Manufacturer[2] != 0x02 || adv.Manufacturer[3] != 0x15 {
		return result
	}

	// UUID (16 bytes)
	uuid := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(adv.Manufacturer[4:8]),
		binary.BigEndian.Uint16(adv.Manufacturer[8:10]),
		binary.BigEndian.Uint16(adv.Manufacturer[10:12]),
		adv.Manufacturer[12:14],
		adv.Manufacturer[14:20])

	// Major, Minor
	major := binary.BigEndian.Uint16(adv.Manufacturer[20:22])
	minor := binary.BigEndian.Uint16(adv.Manufacturer[22:24])

	// TX Power
	txPower := int8(adv.Manufacturer[24])

	result["ibeacon_uuid"] = types.ParsedValue{
		Value:     uuid,
		Type:      "uuid",
		Source:    "ibeacon",
		Timestamp: now,
	}
	result["ibeacon_major"] = types.ParsedValue{
		Value:     int(major),
		Type:      "major",
		Source:    "ibeacon",
		Timestamp: now,
	}
	result["ibeacon_minor"] = types.ParsedValue{
		Value:     int(minor),
		Type:      "minor",
		Source:    "ibeacon",
		Timestamp: now,
	}
	result["ibeacon_tx_power"] = types.ParsedValue{
		Value:     txPower,
		Unit:      "dBm",
		Type:      "tx_power",
		Source:    "ibeacon",
		Timestamp: now,
	}

	return result
}

// parseServiceDataToJSON — преобразование service data в JSON-совместимый формат
func parseServiceDataToJSON(data []byte) interface{} {
	if len(data) == 0 {
		return nil
	}

	// Если данные выглядят как текст, возвращаем как строку
	if isPrintable(data) {
		return string(data)
	}

	// Иначе возвращаем hex
	return hex.EncodeToString(data)
}

// parseFloat — парсинг строки в float64
func parseFloat(s string) (float64, bool) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	if err != nil {
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// isPrintable — проверка, что данные состоят из печатных символов
func isPrintable(data []byte) bool {
	for _, b := range data {
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}

// ParseCommandPayload — парсинг payload команды
// Поддерживает hex, строку, числа, JSON
func ParseCommandPayload(payload string) ([]byte, error) {
	payload = strings.TrimSpace(payload)

	// Попытка парсинга как hex строки
	if isHexString(payload) {
		return hex.DecodeString(strings.TrimPrefix(payload, "0x"))
	}

	// Попытка парсинга как число
	if f, ok := parseFloat(payload); ok {
		// Преобразуем в bytes в зависимости от типа
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, math.Float32bits(float32(f)))
		return buf, nil
	}

	// Как строка
	return []byte(payload), nil
}

// isHexString — проверка, является ли строка hex
func isHexString(s string) bool {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
