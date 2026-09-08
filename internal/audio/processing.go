package audio

import "math"

type speechProcessor interface {
	Process([]float32, []float32)
	Close()
}

func inputLevelDB(samples []float32) int {
	var energy float64
	for _, sample := range samples {
		energy += float64(sample) * float64(sample)
	}
	if energy == 0 || len(samples) == 0 {
		return -60
	}
	return max(-60, min(0, int(10*math.Log10(energy/float64(len(samples))))))
}

func noiseAttenuation(level string) int {
	switch level {
	case "low":
		return -10
	case "medium":
		return -20
	case "high":
		return -30
	}
	return 0
}
