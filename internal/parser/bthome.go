package parser

import (
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/avaksru/ble2homed/pkg/types"
)

// parseBTHomeData разбирает BLE advertising данные в формате BTHome v2 (UUID FCD2).
//
// Формат (по спецификации https://bthome.io/format/ и эталонной реализации
// Home Assistant, Bluetooth-Devices/bthome-ble):
//
//	data[0] — device info:
//	  bit 0    — шифрование (Encrypted)
//	  bit 1    — первые 6 байт после заголовка содержат MAC-адрес
//	  bit 2    — trigger based device
//	  bits 5-7 — версия формата (для v2 должна быть ровно 2)
//	data[1:]  — один или несколько объектов `[тип][данные]`.
//	  Длина данных определяется типом объекта; raw/text содержат байт длины.
//	  Объекты могут идти в произвольном порядке, длина пакета переменная.
//
// Парсер не паникует на коротких/повреждённых пакетах и возвращает только те
// значения, которые удалось корректно прочитать. Зашифрованные пакеты (без
// bindkey/дешифрования) игнорируются.
func parseBTHomeData(data []byte, now time.Time) map[string]types.ParsedValue {
	result := make(map[string]types.ParsedValue)

	if len(data) < 2 {
		return result
	}

	// Бит 0 — шифрование. Мы не умеем расшифровывать BTHome v2 (AES-CCM),
	// поэтому такие пакеты пропускаем целиком.
	if data[0]&0x01 != 0 {
		return result
	}

	// Биты 5-7 — версия формата. Обрабатываем только BTHome v2 (версия 2).
	if (data[0]>>5)&0x07 != 2 {
		return result
	}

	// Бит 1 — первые 6 байт после device info содержат MAC-адрес устройства.
	// Для разбора значений он не нужен, просто пропускаем.
	offset := 1
	if data[0]&0x02 != 0 {
		offset += 6
	}

	for offset < len(data) {
		objType := data[offset]
		spec, ok := bthomeObjectSpecs[objType]
		if !ok {
			// Как в эталонном парсере: неизвестный тип — повреждённый пакет,
			// останавливаем разбор.
			break
		}

		objStart := offset + 1
		dataStart := objStart
		dataLength := spec.Length
		if spec.Command {
			if objStart >= len(data) {
				break
			}
			dataLength = int(data[objStart]&0x1F) + 2
		} else if spec.LengthPrefixed {
			if objStart >= len(data) {
				break
			}
			dataLength = int(data[objStart])
			dataStart++
		}
		objEnd := dataStart + dataLength
		if objEnd > len(data) {
			// Объект обрезан — пакет повреждён, останавливаем разбор.
			break
		}
		objData := data[dataStart:objEnd]
		offset = objEnd

		putBTHomeValue(result, objType, objData, now)
	}

	return result
}

// bthomeObject — описание одного типа объекта BTHome v2.
type bthomeObject struct {
	Length          int     // размер данных объекта в байтах
	Unit            string  // единица измерения ("" если не применимо)
	Type            string  // тип в понятиях проекта (temp/humidity/battery и т.д.)
	Key             string  // ключ в result (совместим с publisher/types)
	Signed          bool    // данные трактуются как знаковое целое
	Scale           float64 // множитель для перевода в физическую единицу
	Binary          bool    // 0/1 — false/true
	LengthPrefixed  bool    // за Length следует байт длины данных
	Text            bool    // данные представляют текст
	Raw             bool    // данные публикуются в hex
	Timestamp       bool    // значение — Unix timestamp в секундах
	Command         bool    // длина команды включает opcode и аргументы
	EventKind       string  // тип события
	FirmwareVersion bool    // данные — компоненты версии firmware
}

// bthomeObjectSpecs — таблица типов объектов BTHome v2.
// Длины, знак и масштабы сверены с const.py из Bluetooth-Devices/bthome-ble
// (эталонная реализация, используется Home Assistant).
var bthomeObjectSpecs = map[byte]bthomeObject{
	0x00: {Length: 1, Type: "packet_id", Key: "packet_id"},
	0x01: {Length: 1, Unit: "%", Type: "battery", Key: "battery"},
	0x02: {Length: 2, Unit: "°C", Type: "temp", Key: "temp", Signed: true, Scale: 0.01},
	0x03: {Length: 2, Unit: "%", Type: "humidity", Key: "humidity", Scale: 0.01},
	0x04: {Length: 3, Unit: "hPa", Type: "pressure", Key: "pressure", Scale: 0.01},
	0x05: {Length: 3, Unit: "lx", Type: "illuminance", Key: "illuminance", Scale: 0.01},
	0x06: {Length: 2, Unit: "kg", Type: "weight", Key: "weight", Scale: 0.01},
	0x07: {Length: 2, Unit: "lb", Type: "weight", Key: "weight_lb", Scale: 0.01},
	0x08: {Length: 2, Unit: "°C", Type: "dew_point", Key: "dew_point", Signed: true, Scale: 0.01},
	0x09: {Length: 1, Type: "count", Key: "count"},
	0x0A: {Length: 3, Unit: "kWh", Type: "energy", Key: "energy", Scale: 0.001},
	0x0B: {Length: 3, Unit: "W", Type: "power", Key: "power", Scale: 0.01},
	0x0C: {Length: 2, Unit: "V", Type: "voltage", Key: "voltage", Scale: 0.001},
	0x0D: {Length: 2, Unit: "µg/m³", Type: "pm2.5", Key: "pm2_5"},
	0x0E: {Length: 2, Unit: "µg/m³", Type: "pm10", Key: "pm10"},
	0x0F: {Length: 1, Type: "binary", Key: "generic", Binary: true},
	0x10: {Length: 1, Type: "binary", Key: "power_binary", Binary: true},
	0x11: {Length: 1, Type: "binary", Key: "opening", Binary: true},
	0x12: {Length: 2, Unit: "ppm", Type: "co2", Key: "co2"},
	0x13: {Length: 2, Unit: "µg/m³", Type: "tvoc", Key: "tvoc"},
	0x14: {Length: 2, Unit: "%", Type: "moisture", Key: "moisture", Scale: 0.01},
	0x15: {Length: 1, Type: "binary", Key: "battery_binary", Binary: true},
	0x16: {Length: 1, Type: "binary", Key: "battery_charging", Binary: true},
	0x17: {Length: 1, Type: "binary", Key: "co_binary", Binary: true},
	0x18: {Length: 1, Type: "binary", Key: "cold", Binary: true},
	0x19: {Length: 1, Type: "binary", Key: "connectivity", Binary: true},
	0x1A: {Length: 1, Type: "binary", Key: "door", Binary: true},
	0x1B: {Length: 1, Type: "binary", Key: "garage_door", Binary: true},
	0x1C: {Length: 1, Type: "binary", Key: "gas_binary", Binary: true},
	0x1D: {Length: 1, Type: "binary", Key: "heat", Binary: true},
	0x1E: {Length: 1, Type: "binary", Key: "light_binary", Binary: true},
	0x1F: {Length: 1, Type: "binary", Key: "lock", Binary: true},
	0x20: {Length: 1, Type: "binary", Key: "moisture_binary", Binary: true},
	0x21: {Length: 1, Type: "binary", Key: "motion", Binary: true},
	0x22: {Length: 1, Type: "binary", Key: "moving", Binary: true},
	0x23: {Length: 1, Type: "binary", Key: "occupancy", Binary: true},
	0x24: {Length: 1, Type: "binary", Key: "plug", Binary: true},
	0x25: {Length: 1, Type: "binary", Key: "presence", Binary: true},
	0x26: {Length: 1, Type: "binary", Key: "problem", Binary: true},
	0x27: {Length: 1, Type: "binary", Key: "running", Binary: true},
	0x28: {Length: 1, Type: "binary", Key: "safety", Binary: true},
	0x29: {Length: 1, Type: "binary", Key: "smoke", Binary: true},
	0x2A: {Length: 1, Type: "binary", Key: "sound", Binary: true},
	0x2B: {Length: 1, Type: "binary", Key: "tamper", Binary: true},
	0x2C: {Length: 1, Type: "binary", Key: "vibration", Binary: true},
	0x2D: {Length: 1, Type: "binary", Key: "window", Binary: true},
	// 0x2E/0x2F — 1-байтные влажность/влажность почвы (отдельные датчики)
	0x2E: {Length: 1, Unit: "%", Type: "humidity", Key: "humidity"},
	0x2F: {Length: 1, Unit: "%", Type: "moisture", Key: "moisture"},
	0x3A: {Length: 1, Type: "event", Key: "button_event", EventKind: "button"},
	0x3B: {Type: "event", Key: "command_event", Command: true},
	0x3C: {Length: 2, Type: "event", Key: "dimmer_event", EventKind: "dimmer"},
	0x3D: {Length: 2, Type: "count", Key: "count"},
	0x3E: {Length: 4, Type: "count", Key: "count"},
	0x3F: {Length: 2, Unit: "°", Type: "rotation", Key: "rotation", Signed: true, Scale: 0.1},
	0x40: {Length: 2, Unit: "mm", Type: "distance", Key: "distance"},
	0x41: {Length: 2, Unit: "m", Type: "distance", Key: "distance", Scale: 0.1},
	0x42: {Length: 3, Unit: "s", Type: "duration", Key: "duration", Scale: 0.001},
	0x43: {Length: 2, Unit: "A", Type: "current", Key: "current", Scale: 0.001},
	0x44: {Length: 2, Unit: "m/s", Type: "speed", Key: "speed", Scale: 0.01},
	0x45: {Length: 2, Unit: "°C", Type: "temp", Key: "temp", Signed: true, Scale: 0.1},
	0x46: {Length: 1, Type: "uv_index", Key: "uv_index", Scale: 0.1},
	0x47: {Length: 2, Unit: "L", Type: "volume", Key: "volume", Scale: 0.1},
	0x48: {Length: 2, Unit: "mL", Type: "volume", Key: "volume"},
	0x49: {Length: 2, Unit: "m3/hr", Type: "volume_flow_rate", Key: "volume_flow_rate", Scale: 0.001},
	0x4A: {Length: 2, Unit: "V", Type: "voltage", Key: "voltage", Scale: 0.1},
	0x4B: {Length: 3, Unit: "m3", Type: "gas", Key: "gas", Scale: 0.001},
	0x4C: {Length: 4, Unit: "m3", Type: "gas", Key: "gas", Scale: 0.001},
	0x4D: {Length: 4, Unit: "kWh", Type: "energy", Key: "energy", Scale: 0.001},
	0x4E: {Length: 4, Unit: "L", Type: "volume", Key: "volume", Scale: 0.001},
	0x4F: {Length: 4, Unit: "L", Type: "water", Key: "water", Scale: 0.001},
	0x50: {Length: 4, Type: "timestamp", Key: "timestamp", Timestamp: true},
	0x51: {Length: 2, Unit: "m/s²", Type: "acceleration", Key: "acceleration", Scale: 0.001},
	0x52: {Length: 2, Unit: "°/s", Type: "gyroscope", Key: "gyroscope", Scale: 0.001},
	0x53: {LengthPrefixed: true, Type: "text", Key: "text", Text: true},
	0x54: {LengthPrefixed: true, Type: "raw", Key: "raw", Raw: true},
	0x55: {Length: 4, Unit: "L", Type: "volume_storage", Key: "volume_storage", Scale: 0.001},
	0x56: {Length: 2, Unit: "µS/cm", Type: "conductivity", Key: "conductivity"},
	0x57: {Length: 1, Unit: "°C", Type: "temp", Key: "temp", Signed: true},
	0x58: {Length: 1, Unit: "°C", Type: "temp", Key: "temp", Signed: true, Scale: 0.35},
	0x59: {Length: 1, Type: "count", Key: "count", Signed: true},
	0x5A: {Length: 2, Type: "count", Key: "count", Signed: true},
	0x5B: {Length: 4, Type: "count", Key: "count", Signed: true},
	0x5C: {Length: 4, Unit: "W", Type: "power", Key: "power", Signed: true, Scale: 0.01},
	0x5D: {Length: 2, Unit: "A", Type: "current", Key: "current", Signed: true, Scale: 0.001},
	0x5E: {Length: 2, Unit: "°", Type: "direction", Key: "direction", Scale: 0.01},
	0x5F: {Length: 2, Unit: "mm", Type: "precipitation", Key: "precipitation", Scale: 0.1},
	0x60: {Length: 1, Type: "channel", Key: "channel"},
	0x61: {Length: 2, Unit: "rpm", Type: "rotational_speed", Key: "rotational_speed"},
	0x62: {Length: 4, Unit: "m/s", Type: "speed", Key: "speed", Signed: true, Scale: 0.000001},
	0x63: {Length: 4, Unit: "m/s²", Type: "acceleration", Key: "acceleration", Signed: true, Scale: 0.000001},
	0x64: {Length: 1, Type: "light_level", Key: "light_level"},
	0x65: {Length: 1, Type: "settings_revision", Key: "settings_revision"},
	0x66: {Length: 2, Unit: "Hz", Type: "frequency", Key: "frequency", Scale: 0.01},
	0x67: {Length: 1, Unit: "%", Type: "progress", Key: "progress"},
	// 0xF0/F1/F2 — device type and firmware version information.
	0xF0: {Length: 2, Type: "device_info", Key: "device_type_id"},
	0xF1: {Length: 4, Type: "device_info", Key: "firmware_version", FirmwareVersion: true},
	0xF2: {Length: 3, Type: "device_info", Key: "firmware_version", FirmwareVersion: true},
}

// putBTHomeValue раскладывает данные объекта в result.
func putBTHomeValue(result map[string]types.ParsedValue, objType byte, objData []byte, now time.Time) {
	spec, ok := bthomeObjectSpecs[objType]
	if !ok || !spec.LengthPrefixed && !spec.Command && spec.Length != len(objData) {
		return
	}

	// Scale == 0 означает "без масштабирования" (множитель 1).
	scale := spec.Scale
	if scale == 0 {
		scale = 1
	}

	var value interface{}
	switch {
	case spec.Binary:
		value = objData[0] != 0
	case spec.EventKind != "" || spec.Command:
		var ok bool
		value, ok = parseBTHomeEvent(spec, objData)
		if !ok {
			return
		}
	case spec.Text:
		if !utf8.Valid(objData) {
			return
		}
		value = string(objData)
	case spec.Raw:
		value = hex.EncodeToString(objData)
	case spec.Timestamp:
		value = time.Unix(int64(readUnsigned(objData, 1)), 0).UTC()
	case spec.FirmwareVersion:
		parts := make([]string, len(objData))
		for i, part := range objData {
			parts[len(objData)-i-1] = strconv.Itoa(int(part))
		}
		value = strings.Join(parts, ".")
	case spec.Signed:
		value = roundScaled(readSigned(objData, scale), scale)
	default:
		f := readUnsigned(objData, scale)
		if spec.Type == "battery" {
			// Исторически battery публикуется как int
			// (publisher ожидает val.Value.(int)).
			value = int(f)
		} else {
			// Округляем, чтобы не публиковать 24.240000000000002 вместо 24.24.
			value = roundScaled(f, scale)
		}
	}

	key := spec.Key
	for index := 2; ; index++ {
		if _, exists := result[key]; !exists {
			break
		}
		key = fmt.Sprintf("%s_%d", spec.Key, index)
	}

	result[key] = types.ParsedValue{
		Value:     value,
		Unit:      spec.Unit,
		Type:      spec.Type,
		Source:    "BTHome v2",
		Timestamp: now,
	}
}

func parseBTHomeEvent(spec bthomeObject, data []byte) (string, bool) {
	if spec.Command {
		if len(data) < 2 || len(data) != int(data[0]&0x1F)+2 {
			return "", false
		}
		opcode := data[1]
		arguments := data[2:]
		switch opcode {
		case 0:
			return "off", true
		case 1:
			return "on", true
		case 2:
			return "toggle", true
		case 3:
			return formatStepEvent("step up", arguments), true
		case 4:
			return formatStepEvent("step down", arguments), true
		default:
			return fmt.Sprintf("command 0x%02x (%s)", opcode, hex.EncodeToString(arguments)), true
		}
	}

	eventID := data[0]
	if eventID == 0 {
		return "", false
	}

	if spec.EventKind == "button" {
		if event, ok := map[byte]string{
			0x01: "press",
			0x02: "double_press",
			0x03: "triple_press",
			0x04: "long_press",
			0x05: "long_double_press",
			0x06: "long_triple_press",
			0x80: "hold_press",
			0x81: "hold_release",
		}[eventID]; ok {
			return event, true
		}
		return fmt.Sprintf("button event 0x%02x", eventID), true
	}

	if spec.EventKind == "dimmer" {
		if len(data) != 2 {
			return "", false
		}
		direction := ""
		switch eventID {
		case 0x01:
			direction = "rotate left"
		case 0x02:
			direction = "rotate right"
		default:
			return fmt.Sprintf("dimmer event 0x%02x", eventID), true
		}
		return formatStepEvent(direction, data[1:]), true
	}

	return "", false
}

func formatStepEvent(action string, steps []byte) string {
	if len(steps) == 0 {
		return action
	}
	unit := "steps"
	if steps[0] == 1 {
		unit = "step"
	}
	return fmt.Sprintf("%s %d %s", action, steps[0], unit)
}

// readSigned — знаковое little-endian целое с масштабированием.
// Поддерживает ширину 1, 2, 3, 4 байта.
func readSigned(data []byte, scale float64) float64 {
	if len(data) == 0 {
		return 0
	}
	// Собираем в int64 (для 3 байт — до 0xFFFFFF, в int64 влезает свободно)
	var raw int64
	for i := len(data) - 1; i >= 0; i-- {
		raw <<= 8
		raw |= int64(data[i])
	}
	// Арифметический сдвиг вправо для знака относительно фактической ширины
	bits := uint(len(data) * 8)
	if bits < 64 && raw&(1<<(bits-1)) != 0 {
		raw |= -1 << bits
	}
	return float64(raw) * scale
}

// readUnsigned — беззнаковое little-endian целое с масштабированием.
// Поддерживает ширину 1, 2, 3, 4 байта.
func readUnsigned(data []byte, scale float64) float64 {
	var raw uint64
	for i := len(data) - 1; i >= 0; i-- {
		raw <<= 8
		raw |= uint64(data[i])
	}
	return float64(raw) * scale
}

// roundScaled округляет значение до количества десятичных знаков, заданного
// масштабом (scale 0.01 → 2 знака, 0.001 → 3, 0.1 → 1). Для scale >= 1
// (целочисленные датчики, счётчики) округление не нужно.
//
// Это убирает артефакты двоичной плавающей точки вида
// 24.240000000000002 вместо 24.24 (как round(raw*factor, places) в
// эталонном парсере bthome-ble).
func roundScaled(v, scale float64) float64 {
	if scale <= 0 || scale >= 1 {
		return v
	}
	places := 0
	for s := scale; s < 1 && places < 6; s *= 10 {
		places++
	}
	p := math.Pow10(places)
	return math.Round(v*p) / p
}
