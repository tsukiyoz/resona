package audio

import "errors"

// SetInputGain never waits for device work or changes capture/transmit gates.
func (e *Engine) SetInputGain(gain int) error {
	if gain < 0 || gain > 200 {
		return errors.New("麦克风输入增益必须在 0 到 200 之间")
	}
	select {
	case e.ops <- struct{}{}:
		defer e.release()
	default:
		return errors.New("音频正在切换，请稍后重试")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	state := e.state
	state.Config.InputGain = gain
	if e.run != nil {
		config := e.run.currentConfig()
		config.InputGain = gain
		e.run.liveConfig.Store(&config)
	}
	e.storeStateLocked(state)
	return nil
}

func applyInputGain(samples []float32, percent int) {
	gain := float32(percent) / 100
	for i, sample := range samples {
		samples[i] = max(float32(-1), min(float32(1), sample*gain))
	}
}
