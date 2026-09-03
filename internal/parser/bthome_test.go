package parser

import (
	"math"
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
		0x00, 0x07, // packet id = 7 (не публикуется)
		0x00, 0x00, // packet id = 0
	}
	if len(data) != 10 {
		t.Fatalf("test packet must be 10 bytes, got %d", len(data))
	}

	got := parseBTHomeData(data, time.Now())
	if len(got) != 2 {
		t.Fatalf("expected 2 values, got %d: %+v", len(got), got)
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
		{0x40, 0x00, 0x07},             // только packet id — не публикуется
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
