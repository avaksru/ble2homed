package parser

import (
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/avaksru/ble2homed/pkg/types"
)

func bthomeADV(data []byte) types.Advertisement {
	return types.Advertisement{
		ServiceData: []types.ServiceData{{UUID: "FCD2", Data: data}},
	}
}

func checkFloat(t *testing.T, name string, got interface{}, want float64) {
	t.Helper()
	f, ok := toFloatHelper(got)
	if !ok {
		t.Fatalf("%s: unexpected value type %T", name, got)
	}
	if math.Abs(f-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, f, want)
	}
}

func toFloatHelper(v interface{}) (float64, bool) {
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
	case uint8:
		return float64(val), true
	default:
		return 0, false
	}
}

// TestParseBTHomeCrashPacket — регрессия краша:
// slice bounds out of range [:11] with capacity 10 на внутреннем/parser/parser.go:89.
// Формат BTHome v2 TLV: заголовок 0x40 (version 2), объекты температура (0x02),
// батарея (0x01), packet id (0x00). Пакет ровно 10 байт — раньше ронял демон.
func TestParseBTHomeCrashPacket(t *testing.T) {
	data := []byte{
		0x40,
		0x02, 0xC4, 0x09, // temp = 0x09C4 = 2500 → 25.00 °C
		0x01, 0x63, // battery = 99 %
		0x00, 0x07, // packet id = 7
		0x00, 0x00, // packet id = 0
	}
	if len(data) != 10 {
		t.Fatalf("test packet must be 10 bytes, got %d", len(data))
	}

	got := parseBTHomeData(data, time.Now())
	if len(got) != 4 {
		t.Fatalf("expected 4 values including packet ids, got %d: %+v", len(got), got)
	}
	checkFloat(t, "temp", got["temp"].Value, 25.0)
	if b, ok := got["battery"].Value.(int); !ok || b != 99 {
		t.Errorf("battery = %v (%T), want int(99)", got["battery"].Value, got["battery"].Value)
	}
}

// TestParseBTHomeSpecExample — пример из спецификации bthome.io:
// 40 02C409 03BF13 → temp=25.00 °C, humidity=50.55 %.
func TestParseBTHomeSpecExample(t *testing.T) {
	data := []byte{0x40,
		0x02, 0xC4, 0x09, // temp 2500 → 25.00
		0x03, 0xBF, 0x13, // humidity 5055 → 50.55
	}
	got := parseBTHomeData(data, time.Now())
	if len(got) != 2 {
		t.Fatalf("expected 2 values, got %d: %+v", len(got), got)
	}
	checkFloat(t, "temp", got["temp"].Value, 25.0)
	checkFloat(t, "humidity", got["humidity"].Value, 50.55)
}

// TestParseBTHomeIntegration — вызов через ParseBLEDataWithBindKey (диспетчер),
// тот самый пакет, который ронял демон.
func TestParseBTHomeIntegration(t *testing.T) {
	data := []byte{
		0x40,
		0x02, 0xC4, 0x09,
		0x01, 0x63,
		0x00, 0x07,
		0x00, 0x00,
	}
	res := ParseBLEDataWithBindKey(bthomeADV(data), &types.BLEConfig{}, nil)
	checkFloat(t, "temp", res["temp"].Value, 25.0)
	if b, ok := res["battery"].Value.(int); !ok || b != 99 {
		t.Errorf("battery = %v (%T), want int(99)", res["battery"].Value, res["battery"].Value)
	}
}

// TestParseBTHomeVoltage — объект 0x0C (voltage, 2 байта, ×0.001).
func TestParseBTHomeVoltage(t *testing.T) {
	data := []byte{0x40,
		0x02, 0xC4, 0x09,
		0x03, 0xBF, 0x13,
		0x0C, 0xBD, 0x0B, // voltage 0x0BBD = 3005 → 3.005 V
	}
	got := parseBTHomeData(data, time.Now())
	checkFloat(t, "temp", got["temp"].Value, 25.0)
	checkFloat(t, "humidity", got["humidity"].Value, 50.55)
	checkFloat(t, "voltage", got["voltage"].Value, 3.005)
}

// TestParseBTHomePressureLux — давление (0x04) и освещённость (0x05).
func TestParseBTHomePressureLux(t *testing.T) {
	data := []byte{0x40,
		0x04, 0x26, 0x13, 0x01, // pressure 0x011326 = 70438 → 704.38 hPa
		0x05, 0x10, 0x27, 0x00, // illuminance 0x002710 = 10000 → 100.00 lx
	}
	got := parseBTHomeData(data, time.Now())
	checkFloat(t, "pressure", got["pressure"].Value, 704.38)
	checkFloat(t, "illuminance", got["illuminance"].Value, 100.0)
}

func TestParseBTHomeAirQualityMetrics(t *testing.T) {
	data := []byte{0x40,
		0x12, 0x20, 0x03, // CO2: 800 ppm
		0x13, 0xF4, 0x01, // TVOC: 500 µg/m³
	}

	got := parseBTHomeData(data, time.Now())
	for key, wantUnit := range map[string]string{
		"co2":  "ppm",
		"tvoc": "µg/m³",
	} {
		value, ok := got[key]
		if !ok {
			t.Fatalf("missing %s metric: %+v", key, got)
		}
		if value.Unit != wantUnit {
			t.Errorf("%s unit = %q, want %q", key, value.Unit, wantUnit)
		}
	}
	checkFloat(t, "co2", got["co2"].Value, 800)
	checkFloat(t, "tvoc", got["tvoc"].Value, 500)
}

func TestParseBTHomeAllSensorDataTypes(t *testing.T) {
	tests := []struct {
		id    byte
		data  []byte
		key   string
		value interface{}
		unit  string
	}{
		{0x51, []byte{0x87, 0x56}, "acceleration", float64(22.151), "m/s²"},
		{0x63, []byte{0x00, 0x57, 0xD0, 0xFF}, "acceleration_2", float64(-3.123456), "m/s²"},
		{0x01, []byte{0x61}, "battery", 97, "%"},
		{0x60, []byte{0x01}, "channel", float64(1), ""},
		{0x12, []byte{0xE2, 0x04}, "co2", float64(1250), "ppm"},
		{0x56, []byte{0xE8, 0x03}, "conductivity", float64(1000), "µS/cm"},
		{0x09, []byte{0x60}, "count", float64(96), ""},
		{0x3D, []byte{0x09, 0x60}, "count_2", float64(24585), ""},
		{0x3E, []byte{0x2A, 0x2C, 0x09, 0x60}, "count_3", float64(1611213866), ""},
		{0x59, []byte{0xEA}, "count_4", float64(-22), ""},
		{0x5A, []byte{0xEA, 0xEA}, "count_5", float64(-5398), ""},
		{0x5B, []byte{0xEA, 0x02, 0x34, 0xEA}, "count_6", float64(-365690134), ""},
		{0x43, []byte{0x4E, 0x34}, "current", float64(13.39), "A"},
		{0x5D, []byte{0x02, 0xEA}, "current_2", float64(-5.63), "A"},
		{0x08, []byte{0xCA, 0x06}, "dew_point", float64(17.38), "°C"},
		{0x5E, []byte{0x9F, 0x8C}, "direction", float64(359.99), "°"},
		{0x40, []byte{0x0C, 0x00}, "distance", float64(12), "mm"},
		{0x41, []byte{0x4E, 0x00}, "distance_2", float64(7.8), "m"},
		{0x42, []byte{0x4E, 0x34, 0x00}, "duration", float64(13.39), "s"},
		{0x4D, []byte{0x12, 0x13, 0x8A, 0x14}, "energy", float64(344593.17), "kWh"},
		{0x0A, []byte{0x13, 0x8A, 0x14}, "energy_2", float64(1346.067), "kWh"},
		{0x66, []byte{0x8A, 0x13}, "frequency", float64(50.02), "Hz"},
		{0x4B, []byte{0x13, 0x8A, 0x14}, "gas", float64(1346.067), "m3"},
		{0x4C, []byte{0x41, 0x01, 0x8A, 0x01}, "gas_2", float64(25821.505), "m3"},
		{0x52, []byte{0x87, 0x56}, "gyroscope", float64(22.151), "°/s"},
		{0x03, []byte{0xBF, 0x13}, "humidity", float64(50.55), "%"},
		{0x2E, []byte{0x23}, "humidity_2", float64(35), "%"},
		{0x05, []byte{0x13, 0x8A, 0x14}, "illuminance", float64(13460.67), "lx"},
		{0x64, []byte{0x02}, "light_level", float64(2), ""},
		{0x06, []byte{0x5E, 0x1F}, "weight", float64(80.3), "kg"},
		{0x07, []byte{0x3E, 0x1D}, "weight_lb", float64(74.86), "lb"},
		{0x14, []byte{0x02, 0x0C}, "moisture", float64(30.74), "%"},
		{0x2F, []byte{0x23}, "moisture_2", float64(35), "%"},
		{0x0D, []byte{0x12, 0x0C}, "pm2_5", float64(3090), "µg/m³"},
		{0x0E, []byte{0x02, 0x1C}, "pm10", float64(7170), "µg/m³"},
		{0x0B, []byte{0x02, 0x1B, 0x00}, "power", float64(69.14), "W"},
		{0x5C, []byte{0x02, 0xFB, 0xFF, 0xFF}, "power_2", float64(-12.78), "W"},
		{0x5F, []byte{0xA0, 0x0F}, "precipitation", float64(400), "mm"},
		{0x04, []byte{0x13, 0x8A, 0x01}, "pressure", float64(1008.83), "hPa"},
		{0x67, []byte{0x41}, "progress", float64(65), "%"},
		{0x54, []byte{0x0C, 'H', 'e', 'l', 'l', 'o', ' ', 'W', 'o', 'r', 'l', 'd', '!'}, "raw", "48656c6c6f20576f726c6421", ""},
		{0x3F, []byte{0x02, 0x0C}, "rotation", float64(307.4), "°"},
		{0x61, []byte{0xAC, 0x0D}, "rotational_speed", float64(3500), "rpm"},
		{0x65, []byte{0x03}, "settings_revision", float64(3), ""},
		{0x44, []byte{0x4E, 0x34}, "speed", float64(133.9), "m/s"},
		{0x62, []byte{0x40, 0x99, 0xDF, 0xFF}, "speed_2", float64(-2.123456), "m/s"},
		{0x57, []byte{0xEA}, "temp", float64(-22), "°C"},
		{0x58, []byte{0xEA}, "temp_2", float64(-7.7), "°C"},
		{0x45, []byte{0x11, 0x01}, "temp_3", float64(27.3), "°C"},
		{0x02, []byte{0xCA, 0x09}, "temp_4", float64(25.06), "°C"},
		{0x53, []byte{0x0C, 'H', 'e', 'l', 'l', 'o', ' ', 'W', 'o', 'r', 'l', 'd', '!'}, "text", "Hello World!", ""},
		{0x50, []byte{0x5D, 0x39, 0x61, 0x64}, "timestamp", time.Unix(0x6461395d, 0).UTC(), ""},
		{0x13, []byte{0x33, 0x01}, "tvoc", float64(307), "µg/m³"},
		{0x0C, []byte{0x02, 0x0C}, "voltage", float64(3.074), "V"},
		{0x4A, []byte{0x02, 0x0C}, "voltage_2", float64(307.4), "V"},
		{0x4E, []byte{0x87, 0x56, 0x2A, 0x01}, "volume", float64(19551.879), "L"},
		{0x47, []byte{0x87, 0x56}, "volume_2", float64(2215.1), "L"},
		{0x48, []byte{0xDC, 0x87}, "volume_3", float64(34780), "mL"},
		{0x55, []byte{0x87, 0x56, 0x2A, 0x01}, "volume_storage", float64(19551.879), "L"},
		{0x49, []byte{0xDC, 0x87}, "volume_flow_rate", float64(34.78), "m3/hr"},
		{0x46, []byte{0x32}, "uv_index", float64(5), ""},
		{0x4F, []byte{0x87, 0x56, 0x2A, 0x01}, "water", float64(19551.879), "L"},
	}

	packet := []byte{0x40}
	for _, test := range tests {
		packet = append(packet, test.id)
		packet = append(packet, test.data...)
	}

	got := parseBTHomeData(packet, time.Now())
	if len(got) != len(tests) {
		t.Fatalf("parsed %d values, want %d: %+v", len(got), len(tests), got)
	}
	for _, test := range tests {
		value, ok := got[test.key]
		if !ok {
			t.Errorf("missing %s (object 0x%02X)", test.key, test.id)
			continue
		}
		if value.Unit != test.unit {
			t.Errorf("%s unit = %q, want %q", test.key, value.Unit, test.unit)
		}
		if want, ok := test.value.(float64); ok {
			checkFloat(t, test.key, value.Value, want)
		} else if !reflect.DeepEqual(value.Value, test.value) {
			t.Errorf("%s = %#v, want %#v", test.key, value.Value, test.value)
		}
	}
}

func TestParseBTHomeRepeatedMeasurementsEventsAndDeviceInfo(t *testing.T) {
	data := []byte{
		0x40,
		0x02, 0xCA, 0x09,
		0x02, 0xD4, 0x09,
		0x3A, 0x01,
		0x3A, 0x80,
		0x3A, 0x00, // no event
		0x3B, 0x01, 0x03, 0x05, // step up 5 steps
		0x3B, 0x00, 0x00, // off
		0x3C, 0x01, 0x03, // rotate left 3 steps
		0x15, 0x01, // low battery binary sensor
		0x21, 0x00,
		0x21, 0x01, // repeated motion sensors use postfixes
		0x00, 0x09, // packet id
		0xF0, 0x01, 0x00, // device type id 1
		0xF1, 0x00, 0x01, 0x02, 0x04, // firmware version 4.2.1.0
		0xF2, 0x00, 0x01, 0x06, // firmware version 6.1.0
	}

	got := parseBTHomeData(data, time.Now())
	checkFloat(t, "temp", got["temp"].Value, 25.06)
	checkFloat(t, "temp_2", got["temp_2"].Value, 25.16)
	if got["button_event"].Value != "press" || got["button_event_2"].Value != "hold_press" {
		t.Errorf("button events = %v, %v", got["button_event"].Value, got["button_event_2"].Value)
	}
	if got["command_event"].Value != "step up 5 steps" || got["command_event_2"].Value != "off" {
		t.Errorf("command events = %v, %v", got["command_event"].Value, got["command_event_2"].Value)
	}
	if got["dimmer_event"].Value != "rotate left 3 steps" {
		t.Errorf("dimmer event = %v", got["dimmer_event"].Value)
	}
	if battery, ok := got["battery_binary"].Value.(bool); !ok || !battery {
		t.Errorf("battery binary = %v (%T), want true", got["battery_binary"].Value, got["battery_binary"].Value)
	}
	if got["motion"].Value != false || got["motion_2"].Value != true {
		t.Errorf("repeated motion states = %v, %v", got["motion"].Value, got["motion_2"].Value)
	}
	checkFloat(t, "packet_id", got["packet_id"].Value, 9)
	checkFloat(t, "device_type_id", got["device_type_id"].Value, 1)
	if got["firmware_version"].Value != "4.2.1.0" || got["firmware_version_2"].Value != "6.1.0" {
		t.Errorf("firmware versions = %v, %v", got["firmware_version"].Value, got["firmware_version_2"].Value)
	}
}

func TestParseBTHomeEventPayloads(t *testing.T) {
	tests := []struct {
		payload []byte
		key     string
		want    string
	}{
		{[]byte{0x3A, 0x02}, "button_event", "double_press"},
		{[]byte{0x3A, 0x03}, "button_event", "triple_press"},
		{[]byte{0x3A, 0x04}, "button_event", "long_press"},
		{[]byte{0x3A, 0x05}, "button_event", "long_double_press"},
		{[]byte{0x3A, 0x06}, "button_event", "long_triple_press"},
		{[]byte{0x3A, 0x81}, "button_event", "hold_release"},
		{[]byte{0x3B, 0x00, 0x01}, "command_event", "on"},
		{[]byte{0x3B, 0x00, 0x02}, "command_event", "toggle"},
		{[]byte{0x3B, 0x01, 0x04, 0x01}, "command_event", "step down 1 step"},
		{[]byte{0x3C, 0x02, 0x0A}, "dimmer_event", "rotate right 10 steps"},
	}
	for _, test := range tests {
		packet := append([]byte{0x40}, test.payload...)
		got := parseBTHomeData(packet, time.Now())
		if value, ok := got[test.key]; !ok || value.Value != test.want {
			t.Errorf("%x: %s = %v, want %q", test.payload, test.key, value.Value, test.want)
		}
	}
}

func TestParseBTHomeBinarySensorTypes(t *testing.T) {
	tests := []struct {
		id    byte
		key   string
		value byte
	}{
		{0x0F, "generic", 1},
		{0x10, "power_binary", 0},
		{0x11, "opening", 1},
		{0x15, "battery_binary", 0},
		{0x16, "battery_charging", 1},
		{0x17, "co_binary", 0},
		{0x18, "cold", 1},
		{0x19, "connectivity", 0},
		{0x1A, "door", 1},
		{0x1B, "garage_door", 0},
		{0x1C, "gas_binary", 1},
		{0x1D, "heat", 0},
		{0x1E, "light_binary", 1},
		{0x1F, "lock", 0},
		{0x20, "moisture_binary", 1},
		{0x21, "motion", 0},
		{0x22, "moving", 1},
		{0x23, "occupancy", 0},
		{0x24, "plug", 1},
		{0x25, "presence", 0},
		{0x26, "problem", 1},
		{0x27, "running", 0},
		{0x28, "safety", 1},
		{0x29, "smoke", 0},
		{0x2A, "sound", 1},
		{0x2B, "tamper", 0},
		{0x2C, "vibration", 1},
		{0x2D, "window", 0},
	}

	packet := []byte{0x40}
	for _, test := range tests {
		packet = append(packet, test.id, test.value)
	}
	got := parseBTHomeData(packet, time.Now())
	if len(got) != len(tests) {
		t.Fatalf("parsed %d binary values, want %d", len(got), len(tests))
	}
	for _, test := range tests {
		value, ok := got[test.key]
		if !ok {
			t.Errorf("missing binary property %s", test.key)
			continue
		}
		want := test.value != 0
		if value.Type != "binary" || value.Value != want {
			t.Errorf("%s = %v (%s), want %t (binary)", test.key, value.Value, value.Type, want)
		}
	}
}

// TestParseBTHomeMACFlag — флаг MAC: 6 байт после заголовка пропускаются.
func TestParseBTHomeMACFlag(t *testing.T) {
	data := []byte{
		0x42,                               // version 2 + MAC present
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, // MAC
		0x01, 0x63, // battery = 99 %
	}
	got := parseBTHomeData(data, time.Now())
	if len(got) != 1 {
		t.Fatalf("expected 1 value, got %d: %+v", len(got), got)
	}
	if b, ok := got["battery"].Value.(int); !ok || b != 99 {
		t.Errorf("battery = %v (%T), want int(99)", got["battery"].Value, got["battery"].Value)
	}
}

// TestParseBTHomeNegativeTemp — отрицательная температура (знаковое int16).
func TestParseBTHomeNegativeTemp(t *testing.T) {
	data := []byte{0x40,
		0x02, 0xDA, 0xFD, // -550 → -5.50 °C
	}
	got := parseBTHomeData(data, time.Now())
	checkFloat(t, "temp", got["temp"].Value, -5.5)
}

// TestParseBTHomeEdgeCases — зашифрованные, неверная версия, обрезанные,
// неизвестные типы: без паники и с пустым результатом.
func TestParseBTHomeEdgeCases(t *testing.T) {
	cases := [][]byte{
		{0x41, 0x02, 0xC4, 0x09},       // encrypted (bit 0)
		{0x20, 0x01, 0x63},             // version 1
		{0x80, 0x01, 0x63},             // version 4
		{0x40, 0x02, 0xC4},             // truncated temp (нужно 2 байта)
		{0x40, 0xFF, 0x01},             // неизвестный тип объекта
		{0x40},                         // один байт заголовка
		{},                             // пустой пакет
		{0x40, 0x0C, 0xBD},             // truncated voltage
		{0x40, 0x03, 0xBF, 0x13, 0x00}, // truncated packet_id в конце — humidity парсится, потом break
		{0x40, 0x00, 0x07},             // только packet id
		{0x40, 0x53},                   // отсутствует длина text
		{0x40, 0x54, 0x03, 0x01, 0x02}, // обрезанный raw payload
		{0x40, 0x3A},                   // обрезанное событие
		{0x40, 0x3B},                   // отсутствует длина команды
		{0x40, 0x3B, 0x02, 0x03},       // обрезанные аргументы команды
		{0x40, 0x53, 0x02, 0xFF, 0xFF}, // невалидный UTF-8 text
	}
	for i, data := range cases {
		if i == 8 {
			// humidity (BF 13 → 50.55) успевает распарситься до обрыва
			got := parseBTHomeData(data, time.Now())
			hum, ok := got["humidity"]
			if len(got) != 1 || !ok {
				t.Fatalf("case %d: expected humidity only, got %+v", i, got)
			}
			checkFloat(t, "humidity", hum.Value, 50.55)
			continue
		}
		if i == 9 {
			got := parseBTHomeData(data, time.Now())
			if len(got) != 1 {
				t.Fatalf("case %d: expected packet id only, got %+v", i, got)
			}
			checkFloat(t, "packet_id", got["packet_id"].Value, 7)
			continue
		}
		if got := parseBTHomeData(data, time.Now()); len(got) != 0 {
			t.Errorf("case %d: expected empty result, got %+v", i, got)
		}
	}
}

// TestATCParserRegression — ATC1441 (13 байт, big-endian) через manufacturer data.
func TestATCParserRegression(t *testing.T) {
	atc := []byte{
		0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, // MAC
		0x00, 0xFF, // temp BE 0x00FF = 255 → 25.5 °C
		0x32,       // humidity 50 %
		0x64,       // battery 100 %
		0x0B, 0xBD, // voltage BE 3005 mV → 3.005 V
		0x00, // flags
	}
	if len(atc) != 13 {
		t.Fatalf("ATC test data must be 13 bytes, got %d", len(atc))
	}
	mfr := make([]byte, 0, 2+len(atc))
	mfr = append(mfr, 0x01, 0x00) // company ID 0x0001 (ATC)
	mfr = append(mfr, atc...)

	adv := types.Advertisement{Manufacturer: mfr}
	res := ParseBLEDataWithBindKey(adv, &types.BLEConfig{}, nil)

	checkFloat(t, "temp", res["temp"].Value, 25.5)
	checkFloat(t, "humidity", res["humidity"].Value, 50)
	if b, ok := res["battery"].Value.(int); !ok || b != 100 {
		t.Errorf("battery = %v (%T), want int(100)", res["battery"].Value, res["battery"].Value)
	}
	checkFloat(t, "voltage", res["voltage"].Value, 3.005)
}

// TestPVVXParserRegression — PVVX (15 байт, little-endian) через service data 181A.
func TestPVVXParserRegression(t *testing.T) {
	pvvx := []byte{
		0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, // MAC
		0xC4, 0x09, // temp LE 0x09C4 = 2500 → 25.0 °C
		0xBF, 0x13, // humidity LE 0x13BF = 5055 → 50.55 %
		0xBD, 0x0B, // voltage LE 0x0BBD = 3005 → 3.005 V
		0x63, // battery 99 % (data[12])
		0x01, // counter
		0x00, // flags
	}
	if len(pvvx) != 15 {
		t.Fatalf("PVVX test data must be 15 bytes, got %d", len(pvvx))
	}
	adv := types.Advertisement{
		ServiceData: []types.ServiceData{{UUID: "181A", Data: pvvx}},
	}
	res := ParseBLEDataWithBindKey(adv, &types.BLEConfig{}, nil)

	checkFloat(t, "temp", res["temp"].Value, 25.0)
	checkFloat(t, "humidity", res["humidity"].Value, 50.55)
	if b, ok := res["battery"].Value.(int); !ok || b != 99 {
		t.Errorf("battery = %v (%T), want int(99)", res["battery"].Value, res["battery"].Value)
	}
	checkFloat(t, "voltage", res["voltage"].Value, 3.005)
}

// TestParseBTHomeRoundedValues — проверка чистого вывода значений конечному
// потребителю (веб/MQTT). Пакет соответствует реальному устройству:
// temp 24.24 °C, humidity 54.74 %, battery 54 %, voltage 2.637 V.
func TestParseBTHomeRoundedValues(t *testing.T) {
	data := []byte{0x40,
		0x02, 0x78, 0x09, // temp = 0x0978 = 2424 → 24.24 °C
		0x03, 0x62, 0x15, // humidity = 0x1562 = 5474 → 54.74 %
		0x01, 0x36, // battery = 54 %
		0x0C, 0x4D, 0x0A, // voltage = 0x0A4D = 2637 → 2.637 V
	}
	got := parseBTHomeData(data, time.Now())
	if len(got) != 4 {
		t.Fatalf("expected 4 values, got %d: %+v", len(got), got)
	}

	f, _ := toFloatHelper(got["temp"].Value)
	if s := strconv.FormatFloat(f, 'g', -1, 64); s != "24.24" {
		t.Errorf("temp string = %q, want %q", s, "24.24")
	}
	f, _ = toFloatHelper(got["humidity"].Value)
	if s := strconv.FormatFloat(f, 'g', -1, 64); s != "54.74" {
		t.Errorf("humidity string = %q, want %q", s, "54.74")
	}
	if b, ok := got["battery"].Value.(int); !ok || b != 54 {
		t.Errorf("battery = %v (%T), want int(54)", got["battery"].Value, got["battery"].Value)
	}
	f, _ = toFloatHelper(got["voltage"].Value)
	if s := strconv.FormatFloat(f, 'g', -1, 64); s != "2.637" {
		t.Errorf("voltage string = %q, want %q", s, "2.637")
	}
}
