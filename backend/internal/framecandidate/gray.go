// Package framecandidate contains shared measurements for frame selection.
// Sampling frequency, thresholds and selection policies remain caller-owned.
package framecandidate

import "fmt"

type GrayMetrics struct {
	MeanLight        float64 `json:"meanLight"`
	MeanChange       float64 `json:"meanChange"`
	NormalizedChange float64 `json:"normalizedChange"`
}

func MeasureGray(pixels, previous []byte) (GrayMetrics, error) {
	if len(pixels) == 0 || (previous != nil && len(previous) != len(pixels)) {
		return GrayMetrics{}, fmt.Errorf("灰度帧长度不一致")
	}
	light, change := 0, 0
	for index, value := range pixels {
		light += int(value)
		if previous != nil {
			difference := int(value) - int(previous[index])
			if difference < 0 {
				difference = -difference
			}
			change += difference
		}
	}
	count := float64(len(pixels))
	return GrayMetrics{MeanLight: float64(light) / count, MeanChange: float64(change) / count, NormalizedChange: float64(change) / (count * 255)}, nil
}
