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

	// HomeAssistant/Puck.js company ID
	CompanyHomeAssistant = 0x0590

	// ATC1441 company ID
	CompanyATC = 0x0001
)

// ParseBLEData — полный парсинг BLE advertising данных
func ParseBLEData(adv types.Advertisement, cfg *types.BLEConfig) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue, 8)
	now := time.Now()

	// Парсинг manufacturer specific data
	if len(adv.Manufacturer) >= 2 {
		companyID := binary.LittleEndian.Uint16(adv.Manufacturer[0:2])

		// HomeAssistant/Puck.js JSON5 парсинг
		if !cfg.DisableJSONParsing && int(companyID) == CompanyHomeAssistant && len(adv.Manufacturer) > 2 {
			parsed := parseHomeAssistantJSON(adv.Manufacturer[2:], now)
			for k, v := range parsed {
				result[k] = v
			}
		}

		// ATC (ATC1441) формат
		if int(companyID) == CompanyATC && len(adv.Manufacturer) >= 15 {
			parsed := parseATCData(adv.Manufacturer[2:], now)
			for k, v := range parsed {
				result[k] = v
			}
		}

		// Сохраняем raw manufacturer data
		if !cfg.DisableManufacturerRaw {
			var buf [4]byte
			hex.Encode(buf[:], adv.Manufacturer[0:2])
			companyHex := string(buf[:])

			result["manufacturer/"+companyHex] = types.ParsedValue{
				Value:     hex.EncodeToString(adv.Manufacturer),
				Type:      "manufacturer",
				Source:    "manufacturer",
				Timestamp: now,
			}
		}
	}

	// Парсинг service data
	for _, sd := range adv.ServiceData {
		uuid := strings.ToUpper(sd.UUID)
		beforeCount := len(result)

		// ✅ BTHome v2 FCD2
		if strings.Contains(uuid, "FCD2") {
			battery := int(sd.Data[4])
			tempRaw := int16(binary.LittleEndian.Uint16(sd.Data[6:8]))
			humidityRaw := binary.LittleEndian.Uint16(sd.Data[9:11])
			
			temp := float64(tempRaw) / 100.0
			humidity := float64(humidityRaw) / 100.0

			result["temp"] = types.ParsedValue{Value: temp, Unit: "°C", Type: "temp", Source: "BTHome v2", Timestamp: now}
			result["humidity"] = types.ParsedValue{Value: humidity, Unit: "%", Type: "humidity", Source: "BTHome v2", Timestamp: now}
			result["battery"] = types.ParsedValue{Value: battery, Unit: "%", Type: "battery", Source: "BTHome v2", Timestamp: now}

			if len(sd.Data) == 14 {
				voltageRaw := int16(binary.LittleEndian.Uint16(sd.Data[10:12]))
				voltage := float64(voltageRaw)
				if voltageRaw > 1000 { voltage = voltage / 1000.0 }
				result["voltage"] = types.ParsedValue{Value: voltage, Unit: "V", Type: "voltage", Source: "BTHome v2", Timestamp: now}
			}
		}

		// ✅ Xiaomi MIJIA FE95
		if strings.Contains(uuid, "FE95") && len(sd.Data) == 18 {
			tempRaw := int16(binary.LittleEndian.Uint16(sd.Data[14:16]))
			humidityRaw := binary.LittleEndian.Uint16(sd.Data[16:18])
			
			temp := float64(tempRaw) / 10.0
			humidity := float64(humidityRaw) / 10.0

			result["temp"] = types.ParsedValue{Value: temp, Unit: "°C", Type: "temp", Source: "Xiaomi MIJIA", Timestamp: now}
			result["humidity"] = types.ParsedValue{Value: humidity, Unit: "%", Type: "humidity", Source: "Xiaomi MIJIA", Timestamp: now}
		}

		// Известные сервисы
		switch {
		case strings.Contains(uuid, ServiceTemperature):
			if temp, ok := parseTemperature(sd.Data); ok {
				result["temp"] = types.ParsedValue{
					Value:     temp,
					Unit:      "°C",
					Type:      "temp",
					Source:    "service_data",
					Timestamp: now,
				}
			}
		case strings.Contains(uuid, ServiceHumidity):

			// Стандартный GATT влажность всегда 2 байта. Если больше - это ATC1441!
			if len(sd.Data) == 2 {
				if humidity, ok := parseHumidity(sd.Data); ok {
					result["humidity"] = types.ParsedValue{
						Value:     humidity,
						Unit:      "%",
						Type:      "humidity",
						Source:    "service_data",
						Timestamp: now,
					}
				}
			} else if len(sd.Data) >= 13 {
				//* fmt.Printf ("%s DBG ✅ Это ATC1441! Вызываю парсер...\n",
				//	time.Now().Format("3:04PM"))
				// Это формат ATC1441!
				parsed := parseATCServiceData(sd.Data, now)
				//* fmt.Printf ("%s DBG Парсер вернул %d полей\n",
				//	time.Now().Format("3:04PM"),
				//	len(parsed))
				for k, v := range parsed {
					result[k] = v
					//* fmt.Printf ("%s DBG Добавил поле: %s = %v\n",
					//	time.Now().Format("3:04PM"),
					//	k,
					//	v.Value)
				}
			}
		case strings.Contains(uuid, ServiceBattery):
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

		// Выводим лог только если удалось распарсить полезные значения
		if len(result) > beforeCount {
			//* fmt.Printf ("%s INF Результат парсинга service data mac=%s uuid=%s data=%s (размер: %d байт)\n",
			//	time.Now().Format("3:04PM"),
			//	strings.ToLower(adv.Addr),
			//	uuid,
			//	hex.EncodeToString(sd.Data),
			//	len(sd.Data))
		}


	}

	// Попытка распознать Eddystone
	if !cfg.DisableEddystone {
		if eddystone := parseEddystone(adv); len(eddystone) > 0 {
			for k, v := range eddystone {
				result[k] = v
			}
		}
	}

	// Попытка распознать iBeacon
	if !cfg.DisableIBeacon {
		if ibeacon := parseIBeacon(adv); len(ibeacon) > 0 {
			for k, v := range ibeacon {
				result[k] = v
			}
		}
	}

	return result
}

// parseHomeAssistantJSON — парсинг JSON5 данных от HomeAssistant/Puck.js
// Формат: {"t":22.4,"h":54} или {"temp":22.4}
func parseHomeAssistantJSON(data []byte, now time.Time) map[string]types.ParsedValue {
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
					Source:    "HomeAssistant",
					Timestamp: now,
				}
			}
		case "h", "hum", "humidity":
			if f, ok := parseFloat(value); ok {
				result["humidity"] = types.ParsedValue{
					Value:     f,
					Unit:      "%",
					Type:      "humidity",
					Source:    "HomeAssistant",
					Timestamp: now,
				}
			}
		case "b", "bat", "battery":
			if f, ok := parseFloat(value); ok {
				result["battery"] = types.ParsedValue{
					Value:     int(f),
					Unit:      "%",
					Type:      "battery",
					Source:    "HomeAssistant",
					Timestamp: now,
				}
			}
		case "p", "pres", "pressure":
			if f, ok := parseFloat(value); ok {
				result["pressure"] = types.ParsedValue{
					Value:     f,
					Unit:      "hPa",
					Type:      "pressure",
					Source:    "HomeAssistant",
					Timestamp: now,
				}
			}
		case "l", "lux", "light", "illuminance":
			if f, ok := parseFloat(value); ok {
				result["illuminance"] = types.ParsedValue{
					Value:     f,
					Unit:      "lx",
					Type:      "illuminance",
					Source:    "HomeAssistant",
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

// parseATCData — парсинг данных формата ATC1441 (псевдоним для обратной совместимости)
func parseATCData(data []byte, now time.Time) map[string]types.ParsedValue {
	return parseATCServiceData(data, now)
}

// parseATCServiceData — парсинг данных формата ATC1441
// Поддерживает 3 формата:
//  1. Стандартный ATC1441 13 байт: MAC(6) | Температура(2) | Влажность(1) | Батарея(1) | Напряжение(2) | Флаги(1)
//  2. PVVX формат 15 байт: MAC(6) | Температура(2) | Влажность(2) | Батарея(1) | Напряжение(2) | Счетчик(1) | Флаги(1) | RSSI(1)
//  3. BTHome v2 формат
func parseATCServiceData(data []byte, now time.Time) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue)

	
	if len(data) < 13 {
		return result
	}

	var tempRaw int16
	var humidity float64
	var battery int
	var voltageRaw uint16

	if len(data) == 13 {
		// ✅ Стандартный ATC1441 формат (Big Endian)
		tempRaw = int16(binary.BigEndian.Uint16(data[6:8]))
		humidity = float64(data[8])
		battery = int(data[9])
		voltageRaw = binary.BigEndian.Uint16(data[10:12])
		
		// В ATC1441 температура в десятых долях градуса!
		temp := float64(tempRaw) / 10.0

		//* fmt.Printf ("%s DBG Парсинг ATC (стандартный 13 байт): len=%d data=%s tempRaw=%d temp=%.2f°C hum=%d%% bat=%d%% volt=%dmV\n",
		//	time.Now().Format("3:04PM"),
		//	len(data),
		//	hex.EncodeToString(data),
		//	int(tempRaw),
		//	temp,
		//	int(humidity),
		//	battery,
		//	int(voltageRaw))

		result["temp"] = types.ParsedValue{
			Value:     temp,
			Unit:      "°C",
			Type:      "temp",
			Source:    "ATC1441",
			Timestamp: now,
		}

	} else if len(data) >= 15 {
		// ✅ PVVX / новый формат (Little Endian)
		// ОФИЦИАЛЬНЫЙ ФОРМАТ PVVX:
		// 0-5: MAC адрес (обратный порядок)
		// 6-7: Температура (int16, сотые доли °C)
		// 8-9: Влажность (uint16, сотые доли %)
		// 10-11: Напряжение (uint16, мВ)
		// 12: Счетчик пакетов
		// 13: Флаги
		// 14: RSSI
		
		tempRaw = int16(binary.LittleEndian.Uint16(data[6:8]))
		humidityRaw := binary.LittleEndian.Uint16(data[8:10])
		voltageRaw = binary.LittleEndian.Uint16(data[10:12])
		
		// В PVVX температура в сотых долях, влажность в сотых долях %
		temp := float64(tempRaw) / 100.0
		humidity = float64(humidityRaw) / 100.0

		// ✅ Батарея в PVVX ПЕРЕДАЁТСЯ ЯВНО! Находится в байте 12!
		battery = int(data[12])

		//* fmt.Printf ("%s DBG Парсинг ATC (PVVX 15 байт): len=%d data=%s tempRaw=%d temp=%.2f°C humRaw=%d hum=%.1f%% volt=%dmV bat=%d%% (рассчитано)\n",
		//	time.Now().Format("3:04PM"),
		//	len(data),
		//	hex.EncodeToString(data),
		//	int(tempRaw),
		//	temp,
		//	int(humidityRaw),
		//	humidity,
		//	int(voltageRaw),
		//	battery)

		result["temp"] = types.ParsedValue{
			Value:     temp,
			Unit:      "°C",
			Type:      "temp",
			Source:    "ATC1441/PVVX",
			Timestamp: now,
		}
	}

	// Общие поля для всех форматов
	result["humidity"] = types.ParsedValue{
		Value:     humidity,
		Unit:      "%",
		Type:      "humidity",
		Source:    "ATC1441",
		Timestamp: now,
	}

	result["battery"] = types.ParsedValue{
		Value:     battery,
		Unit:      "%",
		Type:      "battery",
		Source:    "ATC1441",
		Timestamp: now,
	}

	voltage := float64(voltageRaw) / 1000.0
	result["voltage"] = types.ParsedValue{
		Value:     voltage,
		Unit:      "V",
		Type:      "voltage",
		Source:    "ATC1441",
		Timestamp: now,
	}

	//* fmt.Printf ("%s ✅ УСПЕШНО распарсен ATC: temp=%.2f°C humidity=%.1f%% battery=%d%% voltage=%.3fV\n",
	//	time.Now().Format("3:04PM"),
	//	result["temp"].Value.(float64),
	//	humidity,
	//	battery,
	//	voltage)

	return result
}
