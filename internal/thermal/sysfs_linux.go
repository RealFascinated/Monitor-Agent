//go:build linux

package thermal

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"fascinated.cc/monitor/agent/internal/linux"
)

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
	for _, pattern := range hwmonTemperatureGlobPatterns() {
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
	seenPath := make(map[string]struct{}, len(inputs))
	seenPhysical := make(map[string]struct{})
	for _, inputPath := range inputs {
		resolved := inputPath
		if abs, err := filepath.EvalSymlinks(inputPath); err == nil {
			resolved = abs
		}
		if _, ok := seenPath[resolved]; ok {
			continue
		}
		seenPath[resolved] = struct{}{}

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

		hwmonDir := hwmonRootDir(dir)
		hwName := readTrimmedFile(filepath.Join(hwmonDir, "name"))
		if !shouldReportHwmonTemperature(hwName, label) {
			continue
		}

		celsius, ok := readTemperatureFile(inputPath)
		if !ok {
			continue
		}

		physicalKey := hwmonPhysicalDedupKey(hwName, dir, basename, label)
		if _, ok := seenPhysical[physicalKey]; ok {
			continue
		}
		seenPhysical[physicalKey] = struct{}{}
		sensor := hwmonSensorKey(hwName, dir, basename, label)
		readings = append(readings, TemperatureReading{Sensor: sensor, Celsius: celsius})
	}
	return readings
}

func hwmonTemperatureGlobPatterns() []string {
	return []string{
		"/sys/class/hwmon/hwmon*/temp*_input",
		"/sys/class/hwmon/hwmon*/device/temp*_input",
		"/sys/class/nvme/nvme*/device/hwmon/hwmon*/temp*_input",
		"/sys/class/nvme/nvme*/device/hwmon/hwmon*/device/temp*_input",
	}
}

func hwmonPhysicalDedupKey(hwName, dir, basename, label string) string {
	key := hwmonPhysicalKey(hwName, dir, basename, label)
	if hwName == "nvme" {
		return filepath.Base(hwmonRootDir(dir)) + "/" + key
	}
	return key
}

func hwmonPhysicalKey(hwName, dir, basename, label string) string {
	if label == "" {
		label = basename
	}
	deviceID := hwmonDeviceID(dir)
	if usesDeviceScopedHwmonName(hwName) || deviceID != filepath.Base(hwmonRootDir(dir)) {
		return deviceID + "/" + label
	}
	return hwName + "/" + label
}

func hwmonSensorKey(hwName, dir, basename, label string) string {
	if label == "" {
		label = basename
	}
	prefix := hwName
	if prefix == "" {
		prefix = filepath.Base(hwmonRootDir(dir))
	}
	if prefix == "nvme" {
		if id := nvmeControllerID(dir); id != "" {
			prefix = id
		} else if deviceID := hwmonDeviceID(dir); deviceID != "" {
			prefix = deviceID
		}
	} else if usesDeviceScopedHwmonName(prefix) {
		if deviceID := hwmonDeviceID(dir); deviceID != "" {
			prefix = deviceID
		}
	}
	return prefix + "/" + label
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
		if !temperaturesClose(candidate.Celsius, other.Celsius) {
			continue
		}
		// Only drop redundant thermal_zone / ACPI-style readings when hwmon reports the same temp.
		if isRedundantThermalReading(candidate.Sensor, other.Sensor) {
			return false
		}
	}
	return true
}

// isRedundantThermalReading reports whether zoneSensor duplicates hwmonSensor.
func isRedundantThermalReading(zoneSensor, hwmonSensor string) bool {
	if strings.Contains(zoneSensor, "/") {
		return false
	}
	return strings.Contains(hwmonSensor, "/")
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

// shouldReportHwmonTemperature filters redundant NVMe die sensors. The dashboard
// only surfaces a handful of temperature series; Composite is the standard drive temp.
func shouldReportHwmonTemperature(hwName, label string) bool {
	if hwName != "nvme" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(label), "Composite")
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
	return linux.HostPath(filepath.Join(append([]string{"/sys"}, elem...)...))
}
