package audio

import "errors"

// SetOutputGain changes only the immutable mixer configuration, never devices.
func (e *Engine) SetOutputGain(volume int) error {
	if volume < 0 || volume > MaxPlaybackVolume {
		return errors.New("收听增益超出范围")
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
	state.Config.Volume = volume
	if e.run != nil {
		config := e.run.currentConfig()
		config.Volume = volume
		e.run.liveConfig.Store(&config)
	}
	e.storeStateLocked(state)
	return nil
}
