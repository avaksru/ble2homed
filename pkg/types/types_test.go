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
	device.SetParsedValue("acceleration_2", ParsedValue{Value: -3.1, Type: "acceleration", Source: "BTHome v2"})
	device.SetParsedValue("distance_2", ParsedValue{Value: 7.8, Unit: "m", Type: "distance", Source: "BTHome v2"})
	device.SetParsedValue("text", ParsedValue{Value: "Hello", Type: "text", Source: "BTHome v2"})
	device.SetParsedValue("motion", ParsedValue{Value: true, Type: "binary", Source: "BTHome v2"})
	device.SetParsedValue("button_event", ParsedValue{Value: "press", Type: "event", Source: "BTHome v2"})
	device.SetParsedValue("packet_id", ParsedValue{Value: 7, Type: "packet_id", Source: "BTHome v2"})
	device.SetParsedValue("device_type_id", ParsedValue{Value: 1, Type: "device_info", Source: "BTHome v2"})

	exposes := device.GetExposeList()
	got := make(map[string]string, len(exposes))
	seen := make(map[string]bool, len(exposes))
	for _, expose := range exposes {
		got[expose.Property] = expose.Unit
		seen[expose.Property] = true
	}

	for property, wantUnit := range map[string]string{
		"co2":            "ppm",
		"tvoc":           "µg/m³",
		"illuminance":    "lx",
		"acceleration_2": "m/s²",
		"distance_2":     "m",
	} {
		if !seen[property] {
			t.Errorf("%s is missing from exposes", property)
			continue
		}
		if got[property] != wantUnit {
			t.Errorf("%s unit = %q, want %q", property, got[property], wantUnit)
		}
	}
	if !seen["text"] {
		t.Error("text attribute is missing from exposes")
	}
	for _, property := range []string{"button_event", "packet_id", "device_type_id"} {
		if seen[property] {
			t.Errorf("%s should not be exposed as a sensor", property)
		}
	}
	if !seen["motion"] {
		t.Fatal("binary BTHome attribute is missing from exposes")
	}
	for _, expose := range exposes {
		if expose.Property == "motion" && expose.Type != "binary_sensor" {
			t.Errorf("motion expose type = %q, want binary_sensor", expose.Type)
		}
	}
	if got := device.GetHomedExpose().Common.Options["motion"].Type; got != "binary_sensor" {
		t.Errorf("HOMEd motion type = %q, want binary_sensor", got)
	}
}

func TestDeviceFlatMapsRepeatedTemperatures(t *testing.T) {
	device := NewDevice("aa:bb:cc:dd:ee:ff")
	device.SetParsedValue("temp", ParsedValue{Value: 20.0, Type: "temp", Source: "BTHome v2"})
	device.SetParsedValue("temp_2", ParsedValue{Value: 21.0, Type: "temp", Source: "BTHome v2"})

	fd := device.GetFDFlat()
	if fd["temperature"] != 20.0 || fd["temperature_2"] != 21.0 {
		t.Errorf("temperature values = %v, %v", fd["temperature"], fd["temperature_2"])
	}

	exposeByProperty := make(map[string]bool)
	for _, expose := range device.GetExposeList() {
		exposeByProperty[expose.Property] = true
	}
	if !exposeByProperty["temperature"] || !exposeByProperty["temperature_2"] {
		t.Errorf("repeated temperatures missing from exposes: %v", exposeByProperty)
	}
}
