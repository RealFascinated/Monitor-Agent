//go:build !linux

package metric

import (
	"sort"

	"github.com/shirou/gopsutil/v4/sensors"
)

type TemperatureReading struct {
	Sensor  string
	Celsius float64
}

func ReadTemperatures() []TemperatureReading {
	stats, err := sensors.SensorsTemperatures()
	if err != nil || len(stats) == 0 {
		return nil
	}

	readings := make([]TemperatureReading, 0, len(stats))
	for _, stat := range stats {
		if stat.SensorKey == "" {
			continue
		}
		readings = append(readings, TemperatureReading{
			Sensor:  stat.SensorKey,
			Celsius: stat.Temperature,
		})
	}
	if len(readings) == 0 {
		return nil
	}
	sort.Slice(readings, func(i, j int) bool {
		return readings[i].Sensor < readings[j].Sensor
	})
	return readings
}
