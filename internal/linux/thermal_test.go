//go:build linux

package linux

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSysfsMilliCelsius(t *testing.T) {
	c, ok := sysfsMilliCelsius(46800)
	if !ok || c != 46.8 {
		t.Fatalf("millidegrees: got %v ok=%v", c, ok)
	}
	c, ok = sysfsMilliCelsius(46)
	if !ok || c != 46 {
		t.Fatalf("whole degrees: got %v ok=%v", c, ok)
	}
	if _, ok := sysfsMilliCelsius(0); ok {
		t.Fatal("expected invalid for zero")
	}
}

func TestReadHwmonTemperatures(t *testing.T) {
	root := t.TempDir()
	hwmon := filepath.Join(root, "sys", "class", "hwmon", "hwmon2")
	if err := os.MkdirAll(hwmon, 0o755); err != nil {
		t.Fatal(err)
	}
	writeThermalFile(t, filepath.Join(hwmon, "name"), "k10temp\n")
	writeThermalFile(t, filepath.Join(hwmon, "temp1_label"), "Tctl\n")
	writeThermalFile(t, filepath.Join(hwmon, "temp1_input"), "68500\n")

	t.Setenv("MONITOR_HOST_ROOT", root)
	readings := readHwmonTemperatures()
	if len(readings) != 1 {
		t.Fatalf("readings = %+v", readings)
	}
	if readings[0].Sensor != "k10temp/Tctl" {
		t.Fatalf("sensor = %q, want k10temp/Tctl", readings[0].Sensor)
	}
	if readings[0].Celsius < 68 || readings[0].Celsius > 69 {
		t.Fatalf("celsius = %v, want ~68.5", readings[0].Celsius)
	}
}

func TestReadThermalZoneTemperatures(t *testing.T) {
	root := t.TempDir()
	zone := filepath.Join(root, "sys", "class", "thermal", "thermal_zone0")
	if err := os.MkdirAll(zone, 0o755); err != nil {
		t.Fatal(err)
	}
	writeThermalFile(t, filepath.Join(zone, "type"), "x86_pkg_temp\n")
	writeThermalFile(t, filepath.Join(zone, "temp"), "52000\n")

	t.Setenv("MONITOR_HOST_ROOT", root)
	readings := readThermalZoneTemperatures()
	if len(readings) != 1 || readings[0].Sensor != "x86_pkg_temp" {
		t.Fatalf("readings = %+v", readings)
	}
	if readings[0].Celsius != 52 {
		t.Fatalf("celsius = %v", readings[0].Celsius)
	}
}

func TestDedupeTemperatureReadings(t *testing.T) {
	readings := dedupeTemperatureReadings([]TemperatureReading{
		{Sensor: "thermal_zone0", Celsius: 52},
		{Sensor: "coretemp/Package id 0", Celsius: 52.1},
	})
	if len(readings) != 1 || readings[0].Sensor != "coretemp/Package id 0" {
		t.Fatalf("deduped = %+v", readings)
	}
}

func TestIsReportedTemperatureLabel(t *testing.T) {
	if isReportedTemperatureLabel("Critical Temperature") {
		t.Fatal("expected false for critical threshold label")
	}
}

func writeThermalFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
