package audio

import "time"

// Receive processing is owned by the existing mixer worker, never device callbacks.
// Gain histories outlive individual talk bursts, but not member instances/sessions.
type receiveChain struct {
	peers map[uint16]*receiveProcessor
}

type receiveProcessor struct {
	instance  string
	lastUsed  time.Time
	processor speechProcessor
}

func (c *receiveChain) close() {
	for id, p := range c.peers {
		p.processor.Close()
		delete(c.peers, id)
	}
}

func (c *receiveChain) prune(now time.Time, current func(uint16, string) bool) {
	for id, p := range c.peers {
		if !current(id, p.instance) || now.Sub(p.lastUsed) > 30*time.Second {
			p.processor.Close()
			delete(c.peers, id)
		}
	}
}

func (c *receiveChain) peer(id uint16, instance string, config ProcessingConfig, now time.Time) (*receiveProcessor, error) {
	if p := c.peers[id]; p != nil {
		if p.instance == instance {
			p.lastUsed = now
			return p, nil
		}
		p.processor.Close()
		delete(c.peers, id)
	}
	p, err := newReceiveProcessor(config)
	if err != nil {
		return nil, err
	}
	if c.peers == nil {
		c.peers = make(map[uint16]*receiveProcessor, maxSpeakers)
	}
	if len(c.peers) >= maxSpeakers {
		var oldest uint16
		var at time.Time
		for id, p := range c.peers {
			if at.IsZero() || p.lastUsed.Before(at) {
				oldest, at = id, p.lastUsed
			}
		}
		c.peers[oldest].processor.Close()
		delete(c.peers, oldest)
	}
	r := &receiveProcessor{instance: instance, lastUsed: now, processor: p}
	c.peers[id] = r
	return r, nil
}

func (p *receiveProcessor) process(samples []float32, concealed bool) {
	// Do not teach the AGC from PLC, silence or sub-threshold background. Native
	// voice frames are 20 ms; leave other frame sizes unchanged rather than pad.
	if p == nil || concealed || len(samples)%FrameSamples != 0 {
		return
	}
	for start := 0; start < len(samples); start += FrameSamples {
		frame := samples[start : start+FrameSamples]
		if pcmHasActivity(frame) {
			p.processor.Process(frame, nil)
		}
	}
}
