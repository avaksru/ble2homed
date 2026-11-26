package types

import (
	"testing"
)

func TestDeviceGetExposeListITAG(t *testing.T) {
	device := NewDevice("aa:bb:cc:dd:ee:ff")
	device.IsITAG = true

	exposes := device.GetExposeList()
	if len(exposes) != 2 {
		t.Fatalf("expected 2 exposes for iTAG, got %d: %+v", len(exposes), exposes)
	}

	if exposes[0].Property != "rssi" {
		t.Errorf("expected first expose property=rssi, got %s", exposes[0].Property)
	}
	if exposes[0].Unit != "dBm" {
		t.Errorf("expected rssi unit=dBm, got %s", exposes[0].Unit)
	}

	if exposes[1].Property != "last" {
		t.Errorf("expected second expose property=last, got %s", exposes[1].Property)
	}
}

func TestDeviceGetExposeListRegular(t *testing.T) {
	device := NewDevice("aa:bb:cc:dd:ee:ff")
	device.SetParsedValue("temp", ParsedValue{Value: 25.0, Type: "temp", Unit: "°C"})

	exposes := device.GetExposeList()
	if len(exposes) != 2 {
		t.Fatalf("expected 2 exposes for regular device, got %d: %+v", len(exposes), exposes)
	}

	if exposes[0].Property != "rssi" {
		t.Errorf("expected first expose property=rssi, got %s", exposes[0].Property)
	}
	if exposes[1].Property != "temperature" {
		t.Errorf("expected second expose property=temperature, got %s", exposes[1].Property)
	}
}

func TestDeviceGetExposeListIncludesBTHomeMetrics(t *testing.T) {
	device := NewDevice("aa:bb:cc:dd:ee:ff")
	device.SetParsedValue("co2", ParsedValue{Value: 800, Type: "co2", Source: "BTHome v2"})
	device.SetParsedValue("tvoc", ParsedValue{Value: 40, Type: "tvoc", Source: "BTHome v2"})
	device.SetParsedValue("illuminance", ParsedValue{Value: 100.0, Type: "illuminance", Unit: "lx", Source: "BTHome v2"})
	device.SetParsedValue("motion", ParsedValue{Value: true, Type: "binary", Source: "BTHome v2"})

	exposes := device.GetExposeList()
	got := make(map[string]string, len(exposes))
	for _, expose := range exposes {
		got[expose.Property] = expose.Unit
	}

	for property, wantUnit := range map[string]string{
		"co2":         "ppm",
		"tvoc":        "µg/m³",
		"illuminance": "lx",
	} {
		if got[property] != wantUnit {
			t.Errorf("%s unit = %q, want %q", property, got[property], wantUnit)
		}
	}
	if _, ok := got["motion"]; ok {
		t.Error("binary BTHome attributes should not be exposed as numeric metrics")
	}
}
