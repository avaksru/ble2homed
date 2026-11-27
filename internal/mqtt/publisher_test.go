package mqtt

import (
	"path/filepath"
	"testing"

	"github.com/avaksru/ble2homed/pkg/types"
	"github.com/rs/zerolog"
)

func TestUpdateDeviceDatabaseAddsBTHomeMetricsOnlyWhenPermitJoinEnabled(t *testing.T) {
	newDevice := func() *types.Device {
		device := types.NewDevice("aa:bb:cc:dd:ee:ff")
		device.SetParsedValue("co2", types.ParsedValue{
			Value:  800,
			Type:   "co2",
			Unit:   "ppm",
			Source: "BTHome v2",
		})
		device.SetParsedValue("distance", types.ParsedValue{
			Value:  12,
			Type:   "distance",
			Unit:   "mm",
			Source: "BTHome v2",
		})
		device.SetParsedValue("distance_2", types.ParsedValue{
			Value:  7.8,
			Type:   "distance",
			Unit:   "m",
			Source: "BTHome v2",
		})
		device.SetParsedValue("motion", types.ParsedValue{
			Value:  true,
			Type:   "binary",
			Source: "BTHome v2",
		})
		return device
	}

	t.Run("permit join disabled", func(t *testing.T) {
		publisher := &Publisher{
			config: &types.Config{
				DatabasePath: filepath.Join(t.TempDir(), "devices.json"),
				KnownDevices: map[string]types.KnownDevice{},
			},
			logger: zerolog.Nop(),
		}

		added, err := publisher.updateDeviceDatabase(newDevice())
		if err != nil {
			t.Fatalf("updateDeviceDatabase returned error: %v", err)
		}
		if added {
			t.Fatal("device added while permitJoin is disabled")
		}
	})

	t.Run("permit join enabled", func(t *testing.T) {
		publisher := &Publisher{
			config: &types.Config{
				DatabasePath: filepath.Join(t.TempDir(), "devices.json"),
				KnownDevices: map[string]types.KnownDevice{},
			},
			logger:     zerolog.Nop(),
			permitJoin: true,
		}

		added, err := publisher.updateDeviceDatabase(newDevice())
		if err != nil {
			t.Fatalf("updateDeviceDatabase returned error: %v", err)
		}
		if !added {
			t.Fatal("BTHome device with a parsed metric was not added")
		}

		db, err := publisher.loadDeviceDatabase()
		if err != nil {
			t.Fatalf("loadDeviceDatabase returned error: %v", err)
		}
		if len(db.Devices) != 1 {
			t.Fatalf("database contains %d devices, want 1", len(db.Devices))
		}

		hasCO2 := false
		for _, expose := range db.Devices[0].Exposes {
			if expose == "co2" {
				hasCO2 = true
				break
			}
		}
		if !hasCO2 {
			t.Fatalf("CO2 metric missing from device exposes: %v", db.Devices[0].Exposes)
		}
		if got := db.Devices[0].Options["distance_2"].Unit; got != "m" {
			t.Errorf("persisted second distance unit = %q, want m", got)
		}
		if got := db.Devices[0].Options["motion"].Type; got != "binary_sensor" {
			t.Errorf("persisted motion type = %q, want binary_sensor", got)
		}
	})
}
