package transport

import (
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/honeybbq/teamspeak-go/crypto"
	"github.com/honeybbq/teamspeak-go/handshake"
)

const (
	MaxOutPacketSize        = 500
	ReceivePacketWindowSize = 1024
	PingInterval            = 5 * time.Second
	PacketTimeout           = 60 * time.Second
	MaxRetryInterval        = time.Second
	udpReadBufferSize       = 4096
	voicePayloadBufferSize  = 1027
	smallPacketBufferSize   = 2
	packetProcessQueueSize  = 2048
	resendBaseInterval      = 500 * time.Millisecond
	resendLoopInterval      = 100 * time.Millisecond
	headerSize              = 5
	tagSize                 = 8
	voiceHeaderSize         = 3
	ackDataSize             = 2
)

var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, udpReadBufferSize)

		return &buf
	},
}

// voicePayloadPool holds buffers for the 3-byte voice header plus Opus frame.
var voicePayloadPool = sync.Pool{
	New: func() any {
		buf := make([]byte, voicePayloadBufferSize)

		return &buf
	},
}

// smallBufPool holds 2-byte buffers for ACK/Pong payloads.
var smallBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, smallPacketBufferSize)

		return &buf
	},
}

type pooledBuffer struct {
	buf []byte
	n   int
}

type PacketHandler struct {
	lastMessageReceived  time.Time
	conn                 io.ReadWriteCloser
	commandQueue         map[uint16]*Packet
	commandLowQueue      map[uint16]*Packet
	stopCh               chan struct{}
	recvWindowCommand    *GenerationWindow
	recvWindowCommandLow *GenerationWindow
	sendWindowCommand    *GenerationWindow
	sendWindowCommandLow *GenerationWindow
	ackManager           map[uint32]*resendPacket
	initPacketCheck      *resendPacket
	packetProcessCh      chan *pooledBuffer
	OnClosed             func(err error)
	logger               *slog.Logger
	OnAck                func(id uint16)
	OnPacket             func(p *Packet)
	TsCrypt              *crypto.Crypt
	generationCounter    [9]uint32
	mu                   sync.Mutex
	closed               atomic.Bool
	packetCounter        [9]uint16
	clientID             uint16
	nextCommandLowID     uint16
	nextCommandID        uint16
}

type resendPacket struct {
	packet       *Packet
	firstSend    time.Time
	lastSend     time.Time
	retryCount   int
	nextInterval time.Duration
}

type decryptPacketResult struct {
	plaintext []byte
	dummyUsed bool
}

func NewPacketHandler(tsCrypt *crypto.Crypt, logger *slog.Logger) *PacketHandler {
	if logger == nil {
		logger = slog.Default()
	}

	return &PacketHandler{
		TsCrypt:              tsCrypt,
		logger:               logger,
		ackManager:           make(map[uint32]*resendPacket),
		packetProcessCh:      make(chan *pooledBuffer, packetProcessQueueSize),
		stopCh:               make(chan struct{}),
		recvWindowCommand:    NewGenerationWindow(1<<16, ReceivePacketWindowSize),
		recvWindowCommandLow: NewGenerationWindow(1<<16, ReceivePacketWindowSize),
		sendWindowCommand:    NewGenerationWindow(1<<16, ReceivePacketWindowSize),
		sendWindowCommandLow: NewGenerationWindow(1<<16, ReceivePacketWindowSize),
		commandQueue:         make(map[uint16]*Packet),
		commandLowQueue:      make(map[uint16]*Packet),
		lastMessageReceived:  time.Now(),
	}
}

func (h *PacketHandler) SetClientID(id uint16) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clientID = id
}

// Connect resolves addr as a UDP address, dials it, and calls Start.
func (h *PacketHandler) Connect(addr string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return err
	}

	return h.Start(conn)
}

// Start attaches conn to the handler, launches background goroutines, and sends
// the initial Init1 handshake packet. conn must implement io.ReadWriteCloser
// with datagram-style semantics (each Write produces one discrete message).
func (h *PacketHandler) Start(conn io.ReadWriteCloser) error {
	h.conn = conn

	go h.receiveLoop()
	go h.processLoop()
	go h.resendLoop()
	go h.pingLoop()

	h.packetCounter[PacketTypeCommand]++
	h.packetCounter[PacketTypeInit1] = 101

	init1Data := handshake.ProcessInit1(h.TsCrypt, nil)

	return h.SendPacket(byte(PacketTypeInit1), init1Data, 0)
}

func (h *PacketHandler) SendPacket(pType byte, data []byte, flags byte) error {
	dummy := !h.TsCrypt.CryptoInitComplete

	// Fragment non-voice command payloads larger than one UDP frame (487 B body).
	if len(data) > 487 && pType != 0 && pType != 1 {
		return h.sendSplitPacket(pType, data, flags, dummy)
	}

	return h.sendPacket(pType, data, flags, dummy)
}

func (h *PacketHandler) sendSplitPacket(pType byte, data []byte, flags byte, dummy bool) error {
	maxSize := 487 // MaxOutPacketSize(500) - Header(5) - Tag(8)
	pos := 0
	first := true

	for pos < len(data) {
		blockSize := min(len(data)-pos, maxSize)

		last := (pos + blockSize) == len(data)

		pFlags := flags
		// TeamSpeak sets Fragmented on the first and last chunk only.
		if first != last {
			pFlags |= byte(PacketFlagFragmented)
		}

		err := h.sendPacket(pType, data[pos:pos+blockSize], pFlags, dummy)
		if err != nil {
			return err
		}

		pos += blockSize
		first = false
	}

	return nil
}

func (h *PacketHandler) sendPacket(pType byte, data []byte, flags byte, dummy bool) error {
	flags = applyProtocolFlags(pType, flags)
	pID, pGen := h.nextPacketIdentity(pType)

	p := &Packet{
		TypeFlagged:  pType | flags,
		ID:           pID,
		GenerationID: pGen,
		Data:         data,
		ClientID:     h.clientID,
	}

	unencrypted := (flags&byte(PacketFlagUnencrypted) != 0)
	header := p.BuildC2SHeader()
	ciphertext, tag, err := h.TsCrypt.Encrypt(pType, p.ID, p.GenerationID, header, p.Data, dummy, unencrypted)
	if err != nil {
		return err
	}

	final := getPooledBytes(&bufPool, tagSize+headerSize+len(ciphertext))
	defer putPooledBytes(&bufPool, final)

	copy(final[0:8], tag)
	copy(final[8:13], header)
	copy(final[13:], ciphertext)

	_, err = h.conn.Write(final[:tagSize+headerSize+len(ciphertext)])

	rp := &resendPacket{
		packet:       p,
		firstSend:    time.Now(),
		lastSend:     time.Now(),
		nextInterval: resendBaseInterval,
	}
	h.trackResendPacket(pType, p, rp)

	return err
}

func (h *PacketHandler) sendPong(pID uint16, dummy bool) error {
	pongData := getPooledBytes(&smallBufPool, smallPacketBufferSize)
	binary.BigEndian.PutUint16(pongData, pID)
	err := h.sendPacket(byte(PacketTypePong), pongData, byte(PacketFlagUnencrypted), dummy)
	putPooledBytes(&smallBufPool, pongData)

	return err
}

func (h *PacketHandler) receiveLoop() {
	var finalErr error
	defer func() {
		if h.OnClosed != nil {
			h.OnClosed(finalErr)
		}
	}()

	for {
		buf := getPooledBytes(&bufPool, udpReadBufferSize)
		n, err := h.conn.Read(buf)
		if err != nil {
			putPooledBytes(&bufPool, buf)
			select {
			case <-h.stopCh:
				return
			default:
				h.logger.Error("udp read failed", slog.Any("error", err))
				finalErr = err

				return
			}
		}

		select {
		case h.packetProcessCh <- &pooledBuffer{buf: buf, n: n}:
		default:
			h.logger.Warn("packet process channel full, dropping packet")
			putPooledBytes(&bufPool, buf)
		}
	}
}

func (h *PacketHandler) processLoop() {
	for {
		select {
		case <-h.stopCh:
			return
		case pb := <-h.packetProcessCh:
			h.handleRawPacket(pb.buf[:pb.n])
			putPooledBytes(&bufPool, pb.buf)
		}
	}
}

func (h *PacketHandler) handleRawPacket(raw []byte) {
	if len(raw) < 11 {
		return
	}

	tag := raw[0:8]
	header := raw[8:11]
	ciphertext := raw[11:]
	p := parseServerPacket(header)
	p.ReceivedAt = time.Now()
	h.markMessageReceived()

	decrypted, ok := h.decryptPacketData(p, header, ciphertext, tag)
	if !ok {
		return
	}
	p.Data = decrypted.plaintext

	if p.Type() == PacketTypePing {
		_ = h.sendPong(p.ID, decrypted.dummyUsed)

		return
	}

	if !h.handleCommandWindowAndAck(p, decrypted.dummyUsed) {
		return
	}

	h.handlePacketQueue(p)
	h.updatePostReceiveState(p)
}

func (h *PacketHandler) getWinForType(pType PacketType) *GenerationWindow {
	switch pType {
	case PacketTypeCommand:
		return h.recvWindowCommand
	case PacketTypeCommandLow:
		return h.recvWindowCommandLow
	case PacketTypeVoice, PacketTypeVoiceWhisper, PacketTypePing, PacketTypePong,
		PacketTypeAck, PacketTypeAckLow, PacketTypeInit1:
		return nil
	default:
		return nil
	}
}

func (h *PacketHandler) handlePacketQueue(p *Packet) {
	pType := p.Type()
	if pType != PacketTypeCommand && pType != PacketTypeCommandLow {
		if h.OnPacket != nil {
			h.OnPacket(p)
		}

		return
	}

	h.mu.Lock()
	var queue map[uint16]*Packet
	var nextID *uint16
	if pType == PacketTypeCommand {
		queue = h.commandQueue
		nextID = &h.nextCommandID
	} else {
		queue = h.commandLowQueue
		nextID = &h.nextCommandLowID
	}

	queue[p.ID] = p

	// If the expected ID never arrives, skip it once a newer fragment has stalled long enough.
	h.fastForwardMissingPackets(pType, queue, nextID)

	for {
		packet, ok := queue[*nextID]
		if !ok {
			h.logQueueBacklog(pType, queue, *nextID)

			break
		}

		var win *GenerationWindow
		if pType == PacketTypeCommand {
			win = h.recvWindowCommand
		} else {
			win = h.recvWindowCommandLow
		}

		reassembled, complete := h.tryReassemble(packet, queue, nextID, win)
		if !complete {
			break
		}

		h.tryDecompressPacket(reassembled)

		if h.OnPacket != nil {
			h.mu.Unlock()
			h.OnPacket(reassembled)
			h.mu.Lock()
		}
	}
	h.mu.Unlock()
}

func parseServerPacket(header []byte) *Packet {
	p := &Packet{}
	p.ParseS2CHeader(header)

	return p
}

func (h *PacketHandler) markMessageReceived() {
	h.mu.Lock()
	h.lastMessageReceived = time.Now()
	h.mu.Unlock()
}

func (h *PacketHandler) resolvePacketGeneration(p *Packet) uint32 {
	var gen uint32
	h.mu.Lock()
	switch p.Type() {
	case PacketTypeCommand:
		gen = h.recvWindowCommand.GetGeneration(int(p.ID))
	case PacketTypeCommandLow:
		gen = h.recvWindowCommandLow.GetGeneration(int(p.ID))
	case PacketTypeAck:
		gen = h.sendWindowCommand.GetGeneration(int(p.ID))
	case PacketTypeAckLow:
		gen = h.sendWindowCommandLow.GetGeneration(int(p.ID))
	case PacketTypeVoice, PacketTypeVoiceWhisper, PacketTypePing, PacketTypePong, PacketTypeInit1:
		// No generation tracking for these packet types.
	default:
		// Unknown packet type, keep generation as zero.
	}
	h.mu.Unlock()

	return gen
}

func (h *PacketHandler) decryptPacketData(
	p *Packet, header, ciphertext, tag []byte,
) (*decryptPacketResult, bool) {
	unencrypted := (p.Flags() & PacketFlagUnencrypted) != 0
	dummy := !h.TsCrypt.CryptoInitComplete
	dummyUsed := dummy
	gen := h.resolvePacketGeneration(p)

	plaintext, err := h.TsCrypt.Decrypt(byte(p.Type()), p.ID, gen, header, ciphertext, tag, dummy, unencrypted)
	if err != nil && !dummy && !unencrypted {
		plaintext, gen, err = h.decryptWithGenerationGuess(p, gen, header, ciphertext, tag)
	}
	if err != nil && !dummy {
		plaintext, dummyUsed, err = h.decryptWithDummyFallback(
			p, gen, header, ciphertext, tag, unencrypted, plaintext, dummyUsed, err,
		)
	}
	if err != nil {
		return nil, false
	}

	return &decryptPacketResult{plaintext: plaintext, dummyUsed: dummyUsed}, true
}

func (h *PacketHandler) decryptWithGenerationGuess(
	p *Packet, gen uint32, header, ciphertext, tag []byte,
) ([]byte, uint32, error) {
	for _, offset := range []int{-1, 1} {
		guessGen, ok := shiftGeneration(gen, offset)
		if !ok {
			continue
		}
		plaintext, err := h.TsCrypt.Decrypt(byte(p.Type()), p.ID, guessGen, header, ciphertext, tag, false, false)
		if err == nil {
			h.logger.Debug("generation guess succeeded",
				slog.Uint64("id", uint64(p.ID)),
				slog.Int("offset", offset),
				slog.Uint64("new_gen", uint64(guessGen)))

			return plaintext, guessGen, nil
		}
	}

	return nil, gen, errDecryptFailed
}

var errDecryptFailed = errors.New("decrypt failed")

func (h *PacketHandler) decryptWithDummyFallback(
	p *Packet, gen uint32, header, ciphertext, tag []byte, unencrypted bool,
	plaintext []byte, dummyUsed bool, decryptErr error,
) ([]byte, bool, error) {
	switch p.Type() {
	case PacketTypeCommand, PacketTypeCommandLow, PacketTypeAck:
		plaintext, decryptErr = h.TsCrypt.Decrypt(byte(p.Type()), p.ID, gen, header, ciphertext, tag, true, unencrypted)
		if decryptErr == nil {
			return plaintext, true, nil
		}
	case PacketTypeVoice, PacketTypeVoiceWhisper, PacketTypePing, PacketTypePong, PacketTypeAckLow, PacketTypeInit1:
		// No dummy fallback path required.
	default:
		// Unknown packet type.
	}
	h.logger.Debug("packet decryption failed",
		slog.Uint64("type", uint64(p.Type())),
		slog.Uint64("id", uint64(p.ID)),
		slog.Uint64("gen", uint64(gen)),
		slog.Any("error", decryptErr))

	return plaintext, dummyUsed, decryptErr
}

func (h *PacketHandler) handleCommandWindowAndAck(p *Packet, dummyUsed bool) bool {
	if p.Type() != PacketTypeCommand && p.Type() != PacketTypeCommandLow {
		return true
	}
	h.mu.Lock()
	var win *GenerationWindow
	if p.Type() == PacketTypeCommand {
		win = h.recvWindowCommand
	} else {
		win = h.recvWindowCommandLow
	}
	inWindow := win.IsInWindow(int(p.ID))
	isOld := win.IsOldPacket(int(p.ID))
	h.mu.Unlock()

	ackType := PacketTypeAck
	if p.Type() == PacketTypeCommandLow {
		ackType = PacketTypeAckLow
	}

	if !inWindow {
		if isOld {
			h.logger.Debug("received old packet, sending ack only",
				slog.Uint64("type", uint64(p.Type())),
				slog.Uint64("id", uint64(p.ID)))
			h.sendAckPacket(p.ID, ackType, dummyUsed)
		} else {
			h.logger.Warn("packet too far ahead, ignoring",
				slog.Uint64("type", uint64(p.Type())),
				slog.Uint64("id", uint64(p.ID)))
		}

		return false
	}
	h.logger.Debug("sending ack for command",
		slog.Uint64("type", uint64(ackType)),
		slog.Uint64("id", uint64(p.ID)))
	h.sendAckPacket(p.ID, ackType, dummyUsed)

	return true
}

func (h *PacketHandler) sendAckPacket(packetID uint16, ackType PacketType, dummyUsed bool) {
	ackData := getPooledBytes(&smallBufPool, ackDataSize)
	binary.BigEndian.PutUint16(ackData, packetID)
	_ = h.sendPacket(byte(ackType), ackData, 0, dummyUsed)
	putPooledBytes(&smallBufPool, ackData)
}

func (h *PacketHandler) updatePostReceiveState(p *Packet) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if p.Type() == PacketTypeInit1 {
		h.logger.Debug("received init1 response, cleared init packet check")
		h.initPacketCheck = nil

		return
	}
	if (p.Type() == PacketTypeAck || p.Type() == PacketTypeAckLow) && len(p.Data) >= 2 {
		ackID := binary.BigEndian.Uint16(p.Data[0:2])
		targetType := uint32(PacketTypeCommand)
		if p.Type() == PacketTypeAckLow {
			targetType = uint32(PacketTypeCommandLow)
		}
		h.logger.Debug("received ack from server",
			slog.Uint64("target_type", uint64(targetType)),
			slog.Uint64("id", uint64(ackID)))
		delete(h.ackManager, (targetType<<16)|uint32(ackID))
	}
}

func (h *PacketHandler) fastForwardMissingPackets(
	pType PacketType, queue map[uint16]*Packet, nextID *uint16,
) {
	for {
		if _, ok := queue[*nextID]; ok {
			return
		}
		if !hasOldNewerPacket(queue, *nextID) {
			return
		}
		h.logger.Warn("skipping missing packet to unblock queue",
			slog.Uint64("type", uint64(pType)),
			slog.Uint64("missing_id", uint64(*nextID)))
		*nextID++
		if win := h.getWinForType(pType); win != nil {
			win.Advance(1)
		}
	}
}

func hasOldNewerPacket(queue map[uint16]*Packet, nextID uint16) bool {
	for id, pkg := range queue {
		if (id-nextID) < 32768 && time.Since(pkg.ReceivedAt) > 5*time.Second {
			return true
		}
	}

	return false
}

func (h *PacketHandler) logQueueBacklog(pType PacketType, queue map[uint16]*Packet, nextID uint16) {
	if len(queue) <= 10 {
		return
	}
	h.logger.Debug("packet queue backlog",
		slog.Uint64("type", uint64(pType)),
		slog.Uint64("next_id", uint64(nextID)),
		slog.Int("backlog_size", len(queue)))
}

func (h *PacketHandler) tryDecompressPacket(packet *Packet) {
	if (packet.Flags() & PacketFlagCompressed) == 0 {
		return
	}
	qlz := NewQlz()
	decompressed, err := qlz.Decompress(packet.Data)
	if err != nil {
		h.logger.Debug("decompression failed",
			slog.Uint64("id", uint64(packet.ID)),
			slog.Any("error", err))

		return
	}
	h.logger.Debug("decompressed packet successfully",
		slog.Uint64("id", uint64(packet.ID)),
		slog.Int("old_len", len(packet.Data)),
		slog.Int("new_len", len(decompressed)))
	packet.Data = decompressed
	packet.TypeFlagged &= ^byte(PacketFlagCompressed)
}

func (h *PacketHandler) tryReassemble(
	startPacket *Packet, queue map[uint16]*Packet, nextID *uint16, win *GenerationWindow,
) (*Packet, bool) {
	if (startPacket.Flags() & PacketFlagFragmented) == 0 {
		advanceQueueWindow(queue, nextID, win)

		return startPacket, true
	}

	fragments, totalSize, ok := collectFragments(queue, *nextID)
	if !ok {
		return nil, false
	}

	h.logger.Debug("reassembling fragmented packet",
		slog.Uint64("start_id", uint64(*nextID)),
		slog.Int("fragments", len(fragments)),
		slog.Int("total_size", totalSize))

	combined := make([]byte, totalSize)
	pos := 0
	for i := range fragments {
		copy(combined[pos:], fragments[i].Data)
		pos += len(fragments[i].Data)
		advanceQueueWindow(queue, nextID, win)
	}

	startPacket.Data = combined
	startPacket.TypeFlagged &= ^byte(PacketFlagFragmented)

	return startPacket, true
}

func applyProtocolFlags(pType byte, flags byte) byte {
	if pType == byte(PacketTypeCommand) || pType == byte(PacketTypeCommandLow) {
		return flags | byte(PacketFlagNewProtocol)
	}

	return flags
}

func (h *PacketHandler) nextPacketIdentity(pType byte) (uint16, uint32) {
	h.mu.Lock()
	defer h.mu.Unlock()

	pID := h.packetCounter[pType]
	pGen := h.generationCounter[pType]
	if pType == byte(PacketTypeInit1) {
		return pID, pGen
	}

	h.packetCounter[pType]++
	if h.packetCounter[pType] == 0 {
		h.generationCounter[pType]++
	}
	if pType == byte(PacketTypeCommand) {
		h.sendWindowCommand.AdvanceToExcluded(int(pID))
	} else if pType == byte(PacketTypeCommandLow) {
		h.sendWindowCommandLow.AdvanceToExcluded(int(pID))
	}

	return pID, pGen
}

func (h *PacketHandler) trackResendPacket(pType byte, p *Packet, rp *resendPacket) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if pType == byte(PacketTypeInit1) {
		h.initPacketCheck = rp

		return
	}
	if pType == byte(PacketTypeCommand) || pType == byte(PacketTypeCommandLow) {
		key := (uint32(pType) << 16) | uint32(p.ID)
		h.ackManager[key] = rp
	}
}

func collectFragments(queue map[uint16]*Packet, startID uint16) ([]*Packet, int, bool) {
	var fragments []*Packet
	currID := startID
	totalSize := 0
	startSeen := false
	for {
		p, ok := queue[currID]
		if !ok {
			return nil, 0, false
		}
		fragments = append(fragments, p)
		totalSize += len(p.Data)
		var complete bool
		startSeen, complete = updateFragmentState(startSeen, p.Flags())
		if complete {
			return fragments, totalSize, true
		}
		currID++
	}
}

func updateFragmentState(startSeen bool, flags PacketFlags) (bool, bool) {
	if (flags & PacketFlagFragmented) != 0 {
		if !startSeen {
			return true, false
		}

		return true, true
	}
	if !startSeen {
		return true, true
	}

	return startSeen, false
}

func shiftGeneration(gen uint32, offset int) (uint32, bool) {
	switch offset {
	case -1:
		if gen == 0 {
			return 0, false
		}

		return gen - 1, true
	case 1:
		if gen == ^uint32(0) {
			return 0, false
		}

		return gen + 1, true
	default:
		return gen, true
	}
}

func advanceQueueWindow(queue map[uint16]*Packet, nextID *uint16, win *GenerationWindow) {
	delete(queue, *nextID)
	*nextID++
	if win != nil {
		win.Advance(1)
	}
}

func (h *PacketHandler) pingLoop() {
	ticker := time.NewTicker(PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			if h.TsCrypt.CryptoInitComplete {
				_ = h.SendPacket(byte(PacketTypePing), []byte{}, byte(PacketFlagUnencrypted))
			}
		}
	}
}

func (h *PacketHandler) resendLoop() {
	ticker := time.NewTicker(resendLoopInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.checkResends()
		}
	}
}

func (h *PacketHandler) ReceivedFinalInitAck() {
	h.mu.Lock()
	h.initPacketCheck = nil
	h.mu.Unlock()
}

func (h *PacketHandler) checkResends() {
	h.mu.Lock()
	now := time.Now()
	needClose := false

	if now.Sub(h.lastMessageReceived) > PacketTimeout {
		h.logger.Warn("idle timeout: no packets received", slog.Duration("timeout", PacketTimeout))
		needClose = true
	}

	if h.initPacketCheck != nil {
		h.doResend(h.initPacketCheck, now)
	}
	for key, rp := range h.ackManager {
		if now.Sub(rp.firstSend) > PacketTimeout {
			delete(h.ackManager, key)
			needClose = true

			break
		}
		h.doResend(rp, now)
	}
	h.mu.Unlock()
	if needClose {
		_ = h.Close()
	}
}

func (h *PacketHandler) doResend(rp *resendPacket, now time.Time) {
	if now.Sub(rp.lastSend) >= rp.nextInterval {
		rp.lastSend = now
		rp.retryCount++
		rp.nextInterval *= 2
		if rp.nextInterval > MaxRetryInterval {
			rp.nextInterval = MaxRetryInterval
		}

		unencrypted := (rp.packet.Flags()&PacketFlagUnencrypted != 0)
		dummy := !h.TsCrypt.CryptoInitComplete
		header := rp.packet.BuildC2SHeader()
		h.logger.Debug("resending packet",
			slog.Uint64("type", uint64(rp.packet.Type())),
			slog.Uint64("id", uint64(rp.packet.ID)),
			slog.Int("retry_count", rp.retryCount),
			slog.Duration("next_interval", rp.nextInterval))
		ciphertext, tag, _ := h.TsCrypt.Encrypt(
			byte(rp.packet.Type()), rp.packet.ID, rp.packet.GenerationID, header, rp.packet.Data, dummy, unencrypted,
		)
		final := make([]byte, tagSize+headerSize+len(ciphertext))
		copy(final[0:8], tag)
		copy(final[8:13], header)
		copy(final[13:], ciphertext)
		_, err := h.conn.Write(final)
		if err != nil {
			h.logger.Warn("resend write failed", slog.Any("error", err))
		}
	}
}

func (h *PacketHandler) SendVoicePacket(data []byte, codec byte) error {
	h.mu.Lock()
	pID := h.packetCounter[PacketTypeVoice]
	pGen := h.generationCounter[PacketTypeVoice]
	h.packetCounter[PacketTypeVoice]++
	if h.packetCounter[PacketTypeVoice] == 0 {
		h.generationCounter[PacketTypeVoice]++
	}
	clid := h.clientID
	h.mu.Unlock()

	payloadLen := voiceHeaderSize + len(data)

	voicePayload := getPooledBytes(&voicePayloadPool, payloadLen)
	binary.BigEndian.PutUint16(voicePayload[0:2], pID)
	voicePayload[2] = codec
	copy(voicePayload[voiceHeaderSize:], data)

	p := &Packet{
		TypeFlagged:  byte(PacketTypeVoice) | byte(PacketFlagUnencrypted),
		ID:           pID,
		GenerationID: pGen,
		Data:         voicePayload,
		ClientID:     clid,
	}

	header := p.BuildC2SHeader()

	final := getPooledBytes(&bufPool, tagSize+headerSize+payloadLen)

	copy(final[0:8], h.TsCrypt.FakeSignature)
	copy(final[8:13], header)
	copy(final[13:], voicePayload)

	_, err := h.conn.Write(final[:tagSize+headerSize+payloadLen])

	putPooledBytes(&bufPool, final)
	putPooledBytes(&voicePayloadPool, voicePayload)

	return err
}

func (h *PacketHandler) Close() error {
	if h.closed.Swap(true) {
		return nil
	}
	close(h.stopCh)
	if h.conn != nil {
		return h.conn.Close()
	}

	return nil
}

func getPooledBytes(pool *sync.Pool, size int) []byte {
	bufPtr, ok := pool.Get().(*[]byte)
	if !ok || bufPtr == nil {
		return make([]byte, size)
	}
	buf := *bufPtr
	if cap(buf) < size {
		return make([]byte, size)
	}

	return buf[:size]
}

func putPooledBytes(pool *sync.Pool, buf []byte) {
	pool.Put(&buf)
}
