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
	readings = append(readings, readThermalZoneTemperatures()...)
	readings = append(readings, readHwmonTemperatures()...)
	if len(readings) == 0 {
		return nil
	}
	sort.Slice(readings, func(i, j int) bool {
		return readings[i].Sensor < readings[j].Sensor
	})
	return readings
}

func readThermalZoneTemperatures() []TemperatureReading {
	entries, err := os.ReadDir("/sys/class/thermal")
	if err != nil {
		return nil
	}

	var readings []TemperatureReading
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "thermal_zone") {
			continue
		}
		celsius, ok := readMillidegreeCelsius(filepath.Join("/sys/class/thermal", name, "temp"))
		if !ok {
			continue
		}
		readings = append(readings, TemperatureReading{Sensor: name, Celsius: celsius})
	}
	return readings
}

func readHwmonTemperatures() []TemperatureReading {
	dirs, err := filepath.Glob("/sys/class/hwmon/hwmon*")
	if err != nil {
		return nil
	}

	var readings []TemperatureReading
	for _, dir := range dirs {
		base := filepath.Base(dir)
		index := strings.TrimPrefix(base, "hwmon")
		inputs, err := filepath.Glob(filepath.Join(dir, "temp*_input"))
		if err != nil {
			continue
		}
		for _, inputPath := range inputs {
			file := filepath.Base(inputPath)
			if !strings.HasSuffix(file, "_input") {
				continue
			}
			label := strings.TrimSuffix(file, "_input")
			celsius, ok := readMillidegreeCelsius(inputPath)
			if !ok {
				continue
			}
			readings = append(readings, TemperatureReading{
				Sensor:  "hwmon" + index + "_" + label,
				Celsius: celsius,
			})
		}
	}
	return readings
}

func readMillidegreeCelsius(path string) (float64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	milli, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || milli <= 0 {
		return 0, false
	}
	celsius := float64(milli) / 1000
	if celsius < -50 || celsius > 150 {
		return 0, false
	}
	return celsius, true
}
