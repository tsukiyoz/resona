package audio

const (
	jitterTargetPackets = 3
	jitterMaxPackets    = 32
)

type jitterBuffer struct {
	packets  map[uint16]Packet
	expected uint16
	started  bool
	hasFirst bool
	hasEnd   bool
}

func newJitterBuffer() *jitterBuffer { return &jitterBuffer{packets: make(map[uint16]Packet)} }

func (j *jitterBuffer) Push(packet Packet) bool {
	if !j.hasFirst {
		j.expected = packet.Sequence
		j.hasFirst = true
	}
	if int16(packet.Sequence-j.expected) < 0 {
		return false
	}
	if _, duplicate := j.packets[packet.Sequence]; duplicate || len(j.packets) >= jitterMaxPackets {
		return false
	}
	j.packets[packet.Sequence] = packet
	j.hasEnd = j.hasEnd || packet.End
	return true
}

// Pop returns a packet, whether playout has started, and whether PLC is needed.
func (j *jitterBuffer) Pop() (Packet, bool, bool) {
	if !j.started {
		if len(j.packets) < jitterTargetPackets && !j.hasEnd {
			return Packet{}, false, false
		}
		j.started = true
	}
	packet, ok := j.packets[j.expected]
	delete(j.packets, j.expected)
	j.expected++
	return packet, true, !ok
}
