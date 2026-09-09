package audio

import "time"

// SetPushToTalk releases the gate synchronously with any in-flight send.
func (e *Engine) SetPushToTalk(pressed bool) error {
	e.mu.RLock()
	run, closed := e.run, e.closed
	e.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	if run == nil {
		return nil
	}
	run.sendMu.Lock()
	run.ptt.Store(pressed && run.config.ActivationMode == "ptt" && run.allowSend.Load())
	if !run.ptt.Load() {
		run.captureMu.Lock()
		run.captureEpoch.Add(1)
		run.capturePCM.Reset()
		run.captureMu.Unlock()
	}
	run.sendMu.Unlock()
	e.mu.Lock()
	if e.run == run && !e.closed {
		state := e.state
		state.PushToTalkPressed = run.ptt.Load()
		if !state.PushToTalkPressed && run.config.ActivationMode == "ptt" {
			state.LocalSpeaking = false
		}
		e.storeStateLocked(state)
	}
	e.mu.Unlock()
	return nil
}

func (r *engineRun) activationOpen(level int, now time.Time) bool {
	switch r.config.ActivationMode {
	case "ptt":
		return r.ptt.Load()
	case "vad":
		if level >= r.currentConfig().VADThresholdDB {
			r.vadUntil = now.Add(250 * time.Millisecond)
		}
		return r.vadUntil.After(now)
	default:
		return true
	}
}

func (e *Engine) setInputLevel(run *engineRun, level int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.run != run || e.closed || !e.state.Active {
		return
	}
	state := e.state
	if state.InputLevelDB == level {
		return
	}
	state.InputLevelDB = level
	e.storeStateLocked(state)
}
