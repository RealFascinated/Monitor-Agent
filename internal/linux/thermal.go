//go:build linux

package linux

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type TemperatureReading struct {
	Sensor  string
	Celsius float64
}

func ReadTemperatures() []TemperatureReading {
	var readings []TemperatureReading
	readings = append(readings, readHwmonTemperatures()...)
	readings = append(readings, readThermalZoneTemperatures()...)
	readings = dedupeTemperatureReadings(readings)
	if len(readings) == 0 {
		return nil
	}
	sort.Slice(readings, func(i, j int) bool {
		return readings[i].Sensor < readings[j].Sensor
	})
	return readings
}

func readThermalZoneTemperatures() []TemperatureReading {
	entries, err := os.ReadDir(sysPath("class", "thermal"))
	if err != nil {
		return nil
	}

	var readings []TemperatureReading
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "thermal_zone") {
			continue
		}
		zoneDir := sysPath("class", "thermal", name)
		celsius, ok := readTemperatureFile(filepath.Join(zoneDir, "temp"))
		if !ok {
			continue
		}
		sensor := name
		if zoneType := readTrimmedFile(filepath.Join(zoneDir, "type")); zoneType != "" {
			sensor = zoneType
		}
		readings = append(readings, TemperatureReading{Sensor: sensor, Celsius: celsius})
	}
	return readings
}

func readHwmonTemperatures() []TemperatureReading {
	var inputs []string
	for _, pattern := range []string{
		"/sys/class/hwmon/hwmon*/temp*_input",
		"/sys/class/hwmon/hwmon*/device/temp*_input",
	} {
		matches, err := filepath.Glob(HostPath(pattern))
		if err != nil {
			continue
		}
		inputs = append(inputs, matches...)
	}
	if len(inputs) == 0 {
		return nil
	}

	var readings []TemperatureReading
	seen := make(map[string]struct{}, len(inputs))
	for _, inputPath := range inputs {
		if _, ok := seen[inputPath]; ok {
			continue
		}
		seen[inputPath] = struct{}{}

		file := filepath.Base(inputPath)
		if !strings.HasSuffix(file, "_input") {
			continue
		}
		basename := strings.TrimSuffix(file, "_input")
		dir := filepath.Dir(inputPath)

		label := readTrimmedFile(filepath.Join(dir, basename+"_label"))
		if !isReportedTemperatureLabel(label) {
			continue
		}

		celsius, ok := readTemperatureFile(inputPath)
		if !ok {
			continue
		}

		hwName := readTrimmedFile(filepath.Join(dir, "name"))
		sensor := hwmonSensorKey(hwName, dir, basename, label)
		readings = append(readings, TemperatureReading{Sensor: sensor, Celsius: celsius})
	}
	return readings
}

func hwmonSensorKey(hwName, dir, basename, label string) string {
	if hwName == "" {
		hwName = filepath.Base(dir)
	}
	if label == "" {
		label = basename
	}
	return hwName + "/" + label
}

func dedupeTemperatureReadings(readings []TemperatureReading) []TemperatureReading {
	if len(readings) < 2 {
		return readings
	}
	out := make([]TemperatureReading, 0, len(readings))
	for _, reading := range readings {
		if keepTemperatureReading(reading, readings) {
			out = append(out, reading)
		}
	}
	return out
}

func keepTemperatureReading(candidate TemperatureReading, all []TemperatureReading) bool {
	for _, other := range all {
		if other.Sensor == candidate.Sensor {
			continue
		}
		if temperaturesClose(candidate.Celsius, other.Celsius) && preferTemperatureSensor(other.Sensor, candidate.Sensor) {
			return false
		}
	}
	return true
}

func preferTemperatureSensor(a, b string) bool {
	// Prefer hwmon-style names (coretemp/Tctl) over generic thermal_zoneN keys.
	aScore := temperatureSensorScore(a)
	bScore := temperatureSensorScore(b)
	if aScore != bScore {
		return aScore > bScore
	}
	return a < b
}

func temperatureSensorScore(sensor string) int {
	switch {
	case strings.Contains(sensor, "/"):
		return 3
	case strings.HasPrefix(sensor, "thermal_zone"):
		return 1
	default:
		return 2
	}
}

func temperaturesClose(a, b float64) bool {
	const threshold = 2.0
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= threshold
}

func isReportedTemperatureLabel(label string) bool {
	if label == "" {
		return true
	}
	lower := strings.ToLower(label)
	if strings.Contains(lower, "distance") {
		return false
	}
	if strings.Contains(lower, "warning temperature") {
		return false
	}
	if strings.Contains(lower, "critical temperature") {
		return false
	}
	return true
}

func readTemperatureFile(path string) (float64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	raw, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	celsius, ok := sysfsMilliCelsius(raw)
	if !ok {
		return 0, false
	}
	return celsius, isReportedTemperature(celsius)
}

// sysfsMilliCelsius converts a sysfs temperature reading to degrees Celsius.
// Most drivers use millidegree Celsius; some report whole degrees when raw < 1000.
func sysfsMilliCelsius(raw int64) (float64, bool) {
	if raw <= 0 {
		return 0, false
	}
	switch {
	case raw >= 1000:
		return float64(raw) / 1000, true
	case raw <= 150:
		return float64(raw), true
	default:
		return float64(raw) / 1000, true
	}
}

func isReportedTemperature(celsius float64) bool {
	return celsius > 0 && celsius <= 150
}

func readTrimmedFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func sysPath(elem ...string) string {
	return HostPath(filepath.Join(append([]string{"/sys"}, elem...)...))
}
