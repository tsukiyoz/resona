package audio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thesyncim/gopus"
)

const (
	incomingQueueSize = 256
	pcmBufferFrames   = 8
	maxSpeakers       = 64
	speakerIdle       = 500 * time.Millisecond
	speakingHold      = 300 * time.Millisecond
	decodeErrorWindow = 2 * time.Second
	decodeErrorLimit  = 3
	speakingEnergy    = 0.000001
)

type Engine struct {
	peers     atomic.Pointer[map[uint16]PeerPlayback]
	ops       chan struct{}
	mu        sync.RWMutex
	transport Transport
	factory   deviceFactory
	run       *engineRun
	state     VoiceState
	closed    bool
	monitor   bool
	notify    func(VoiceState)
	notifyCh  chan VoiceState
	notifyWG  sync.WaitGroup
}

func New(transport Transport, notify func(VoiceState)) *Engine {
	e := &Engine{
		ops:       make(chan struct{}, 1),
		transport: transport,
		factory:   defaultDevices,
		state:     VoiceState{Config: VoiceConfig{Muted: true, Volume: 100, InputGain: 100}},
		notify:    notify,
	}
	if notify != nil {
		e.notifyCh = make(chan VoiceState, 1)
		e.notifyWG.Add(1)
		go e.notifyLoop()
	}
	return e
}

func newWithFactory(transport Transport, notify func(VoiceState), factory deviceFactory) *Engine {
	e := New(transport, notify)
	e.factory = factory
	return e
}

func (e *Engine) Configure(ctx context.Context, config VoiceConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()
	if err := validateConfig(config); err != nil {
		return err
	}
	e.mu.RLock()
	run, active, closed := e.run, e.state.Active, e.closed
	e.mu.RUnlock()
	if !closed && active && run != nil && liveConfigCompatible(run.currentConfig(), config) {
		return e.updateLiveConfig(ctx, run, config)
	}
	return e.configureLocked(ctx, config, true)
}

// Device and activation transitions retain the full stop/restart path.
func liveConfigCompatible(a, b VoiceConfig) bool {
	return a.Enabled == b.Enabled && a.Muted == b.Muted && a.Deafened == b.Deafened &&
		a.LocalMonitor == b.LocalMonitor &&
		a.InputDeviceID == b.InputDeviceID && a.OutputDeviceID == b.OutputDeviceID && a.ActivationMode == b.ActivationMode
}

func (e *Engine) updateLiveConfig(ctx context.Context, run *engineRun, config VoiceConfig) error {
	previous := run.currentConfig()
	var replacement speechProcessor
	rebuild := previous.Processing.Preprocess != config.Processing.Preprocess
	postChanged := previous.Processing.Postprocess != config.Processing.Postprocess
	if rebuild {
		var err error
		replacement, err = newSpeechProcessor(config)
		if err != nil {
			return err
		}
	}
	if postChanged {
		probe, err := newReceiveProcessor(config.Processing)
		if err != nil {
			if replacement != nil {
				replacement.Close()
			}
			return err
		}
		if probe != nil {
			probe.Close()
		}
	}
	if err := ctx.Err(); err != nil {
		if replacement != nil {
			replacement.Close()
		}
		return err
	}
	if postChanged && !run.engine.monitor {
		ack := make(chan struct{}, 1)
		select {
		case run.mixControl <- mixUpdate{specs: config.Processing.Postprocess, ack: ack}:
		case <-run.ctx.Done():
			if replacement != nil {
				replacement.Close()
			}
			return run.ctx.Err()
		}
		select {
		case <-ack:
		case <-run.ctx.Done():
			if replacement != nil {
				replacement.Close()
			}
			return run.ctx.Err()
		}
	}
	if rebuild {
		ack := make(chan struct{}, 1)
		select {
		case run.encodeControl <- encodeUpdate{processor: replacement, ack: ack}:
		case <-run.ctx.Done():
			if replacement != nil {
				replacement.Close()
			}
			return run.ctx.Err()
		}
		select {
		case <-ack:
		case <-run.ctx.Done():
			return run.ctx.Err()
		}
	}
	run.liveConfig.Store(&config)
	e.mu.Lock()
	state := e.state
	state.Config = config
	e.storeStateLocked(state)
	e.mu.Unlock()
	return nil
}

// ChannelChanged drops all old-channel audio and revalidates the channel codec
// and encryption settings while preserving the user's voice controls.
func (e *Engine) ChannelChanged(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()
	e.mu.RLock()
	config := e.state.Config
	updateServer := !e.state.Active
	e.mu.RUnlock()
	return e.configureLocked(ctx, config, updateServer)
}

func (e *Engine) configureLocked(ctx context.Context, config VoiceConfig, updateServer bool) error {
	if err := validateConfig(config); err != nil {
		return err
	}
	e.mu.RLock()
	closed, current := e.closed, e.run
	e.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	if current != nil {
		current.allowSend.Store(false)
	}
	e.stopCurrent()

	if !config.Enabled {
		_ = e.transport.SetVoiceMuted(ctx, true, true)
		e.setState(VoiceState{Config: config})
		return nil
	}
	codec, err := e.transport.VoiceCodec()
	if err != nil {
		return e.configurationFailed(ctx, config, 0, err)
	}
	if !codec.Supported() {
		return e.configurationFailed(ctx, config, codec, fmt.Errorf("%w: %s", ErrUnsupportedCodec, codec))
	}
	if err := ctx.Err(); err != nil {
		return e.configurationFailed(ctx, config, codec, err)
	}
	if config.Deafened {
		if err := e.updateServerMute(ctx, true, true, updateServer); err != nil {
			return e.configurationFailed(ctx, config, codec, fmt.Errorf("无法更新服务器语音状态: %w", err))
		}
		e.setState(VoiceState{Config: config, Active: true, ChannelCodec: codec})
		return nil
	}

	run, err := newEngineRun(e, codec, config.Volume)
	if err != nil {
		return e.configurationFailed(ctx, config, codec, err)
	}
	run.config = config
	if config.LocalMonitor {
		run.monitorPCM = newSampleRing(FrameSamples * 2 * pcmBufferFrames)
	}
	run.processor, err = newSpeechProcessor(config)
	if err != nil {
		run.stop()
		return e.configurationFailed(ctx, config, codec, err)
	}
	needPlayback := true
	needCapture := !config.Muted || config.LocalMonitor
	run.devices, err = e.factory.Open(config, needPlayback, needCapture, deviceCallbacks{capture: run.capture, playback: run.playback})
	if err != nil {
		run.stop()
		return e.configurationFailed(ctx, config, codec, err)
	}
	if err := ctx.Err(); err != nil {
		run.stop()
		return e.configurationFailed(ctx, config, codec, err)
	}
	if err := e.updateServerMute(ctx, config.Muted || config.Deafened, config.Deafened, updateServer); err != nil {
		run.stop()
		return e.configurationFailed(ctx, config, codec, fmt.Errorf("无法更新服务器语音状态: %w", err))
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		e.transport.SetVoiceHandler(nil)
		run.stop()
		return ErrClosed
	}
	e.run = run
	e.mu.Unlock()
	e.transport.SetVoiceHandler(run.enqueue)
	e.setState(VoiceState{Config: config, Active: true, ChannelCodec: codec, InputLevelDB: -60})
	run.start()
	run.allowSend.Store(needCapture)
	slog.Info("voice devices ready", "capture", needCapture, "deafened", config.Deafened, "activation", config.ActivationMode, "local_monitor", config.LocalMonitor)
	return nil
}

func (e *Engine) updateServerMute(ctx context.Context, input, output, needed bool) error {
	if !needed {
		return ctx.Err()
	}
	return e.transport.SetVoiceMuted(ctx, input, output)
}

func (e *Engine) configurationFailed(ctx context.Context, config VoiceConfig, codec Codec, err error) error {
	slog.Warn("voice configuration failed", "cancelled", errors.Is(err, context.Canceled), "timeout", errors.Is(err, context.DeadlineExceeded))
	_ = e.transport.SetVoiceMuted(ctx, true, true)
	e.setState(VoiceState{Config: config, ChannelCodec: codec, Error: voiceErrorMessage(err)})
	return err
}

func voiceErrorMessage(err error) string {
	message := err.Error()
	if errors.Is(err, context.Canceled) {
		message = "语音操作已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		message = "语音操作超时，请检查设备或网络后重试"
	}
	return message
}

func (e *Engine) Status() VoiceState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneVoiceState(e.state)
}

func (e *Engine) Close() error {
	e.ops <- struct{}{}
	defer e.release()
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()
	e.stopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = e.transport.SetVoiceMuted(ctx, true, true)
	e.setState(VoiceState{Config: VoiceConfig{Muted: true, Volume: e.Status().Config.Volume}})
	if e.notifyCh != nil {
		close(e.notifyCh)
		e.notifyWG.Wait()
	}
	return nil
}

func (e *Engine) stopCurrent() {
	e.transport.SetVoiceHandler(nil)
	e.mu.Lock()
	run := e.run
	e.run = nil
	e.mu.Unlock()
	if run != nil {
		run.stop()
	}
}

func (e *Engine) acquire(ctx context.Context) error {
	select {
	case e.ops <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) release() { <-e.ops }

func (e *Engine) setState(state VoiceState) {
	e.mu.Lock()
	e.storeStateLocked(state)
	e.mu.Unlock()
}

func cloneVoiceState(state VoiceState) VoiceState {
	state.SpeakingClientIDs = append([]uint16(nil), state.SpeakingClientIDs...)
	return state
}

func (e *Engine) storeStateLocked(state VoiceState) {
	e.state = cloneVoiceState(state)
	e.publishState(cloneVoiceState(state))
}

// publishState is called with e.mu held so state and callback ordering agree.
func (e *Engine) publishState(state VoiceState) {
	if e.notifyCh == nil {
		return
	}
	select {
	case e.notifyCh <- state:
	default:
		select {
		case <-e.notifyCh:
		default:
		}
		select {
		case e.notifyCh <- state:
		default:
		}
	}
}

func (e *Engine) notifyLoop() {
	defer e.notifyWG.Done()
	for state := range e.notifyCh {
		func() {
			defer func() { _ = recover() }()
			e.notify(state)
		}()
	}
}

func (e *Engine) runError(run *engineRun, err error, fatal bool) {
	if err == nil {
		return
	}
	e.mu.Lock()
	if e.run != run || e.closed {
		e.mu.Unlock()
		return
	}
	state := e.state
	state.Error = voiceErrorMessage(err)
	if fatal {
		state.Active = false
		state.SpeakingClientIDs = nil
		state.LocalSpeaking = false
		run.allowSend.Store(false)
		run.cancel()
	}
	e.storeStateLocked(state)
	e.mu.Unlock()
	if fatal {
		go e.cleanupFailedRun(run)
	}
}

func (e *Engine) applyRunActivity(run *engineRun, version uint64, local bool, clients []uint16) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.run != run || e.closed || run.ctx.Err() != nil || !e.state.Active || e.state.Config.Deafened || version <= run.appliedActivityVersion {
		return
	}
	run.appliedActivityVersion = version
	state := e.state
	if run.config.ActivationMode == "ptt" && !run.ptt.Load() {
		local = false
	}
	if state.LocalSpeaking == local && uint16SlicesEqual(state.SpeakingClientIDs, clients) {
		return
	}
	state.LocalSpeaking = local
	state.SpeakingClientIDs = clients
	e.storeStateLocked(state)
}

func (e *Engine) setRunDecodeError(run *engineRun, senderID uint16, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.run != run || e.closed {
		return
	}
	message := err.Error()
	previous := run.decodeError
	if previous == message && run.decodeErrorSender == senderID && e.state.Error == message {
		return
	}
	run.decodeError = message
	run.decodeErrorSender = senderID
	if e.state.Error != "" && e.state.Error != previous {
		return
	}
	state := e.state
	state.Error = message
	e.storeStateLocked(state)
}

func (e *Engine) clearRunDecodeError(run *engineRun, senderID uint16) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.run != run || e.closed || run.decodeError == "" || run.decodeErrorSender != senderID {
		return
	}
	previous := run.decodeError
	run.decodeError = ""
	run.decodeErrorSender = 0
	if e.state.Error != previous {
		return
	}
	state := e.state
	state.Error = ""
	e.storeStateLocked(state)
}

func uint16SlicesEqual(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (e *Engine) cleanupFailedRun(failed *engineRun) {
	e.ops <- struct{}{}
	defer e.release()
	e.mu.RLock()
	current, closed := e.run, e.closed
	e.mu.RUnlock()
	if closed || current != failed {
		return
	}
	e.stopCurrent()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = e.transport.SetVoiceMuted(ctx, true, true)
}

type engineRun struct {
	diagnostics            voiceCounters
	engine                 *Engine
	ctx                    context.Context
	cancel                 context.CancelFunc
	codec                  Codec
	volume                 float32
	limiter                outputLimiter
	incoming               chan Packet
	captureWake            chan struct{}
	capturePCM             *sampleRing
	playbackPCM            *sampleRing
	monitorPCM             *sampleRing
	devices                deviceSession
	wg                     sync.WaitGroup
	allowSend              atomic.Bool
	encoder                *gopus.Encoder
	fatalOnce              sync.Once
	nonfatalOnce           sync.Once
	stopOnce               sync.Once
	activityMu             sync.Mutex
	localUntil             time.Time
	remoteUntil            map[uint16]time.Time
	activityVersion        uint64
	appliedActivityVersion uint64
	decodeError            string
	decodeErrorSender      uint16
	decodeFailures         map[uint16]decodeFailure
	config                 VoiceConfig
	liveConfig             atomic.Pointer[VoiceConfig]
	encodeControl          chan encodeUpdate
	mixControl             chan mixUpdate
	processor              speechProcessor
	referencePCM           *sampleRing
	ptt                    atomic.Bool
	sendMu                 sync.Mutex
	captureMu              sync.Mutex
	captureEpoch           atomic.Uint64
	lastMeter              time.Time
	bitrate                int
	bitrateSource          ChannelBitrateSource
	vadUntil               time.Time
}

type encodeUpdate struct {
	processor speechProcessor
	ack       chan struct{}
}

type mixUpdate struct {
	specs [1]ProcessorSpec
	ack   chan struct{}
}

func newEngineRun(engine *Engine, codec Codec, volume int) (*engineRun, error) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &engineRun{
		engine: engine, ctx: ctx, cancel: cancel, codec: codec, volume: float32(volume) / 100,
		incoming: make(chan Packet, incomingQueueSize), captureWake: make(chan struct{}, 1),
		encodeControl: make(chan encodeUpdate), mixControl: make(chan mixUpdate),
		capturePCM: newSampleRing(FrameSamples * pcmBufferFrames), playbackPCM: newSampleRing(FrameSamples * 2 * pcmBufferFrames),
		remoteUntil:    make(map[uint16]time.Time),
		decodeFailures: make(map[uint16]decodeFailure),
		referencePCM:   newSampleRing(FrameSamples * 2 * pcmBufferFrames),
	}
	if engine.monitor {
		return r, nil
	}
	channels := 1
	application := gopus.ApplicationVoIP
	bitrate := 48000
	if codec == CodecOpusMusic {
		channels, application, bitrate = 2, gopus.ApplicationAudio, 96000
	}
	if codec == CodecOpusVoice {
		r.bitrateSource, _ = engine.transport.(ChannelBitrateSource)
		if r.bitrateSource != nil {
			bitrate = r.bitrateSource.VoiceBitrate()
			if bitrate < 16000 || bitrate > 64000 {
				cancel()
				return nil, errors.New("invalid channel bitrate")
			}
		}
	}
	encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: SampleRate, Channels: channels, Application: application})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("无法初始化 Opus 编码器: %w", err)
	}
	if err := encoder.SetBitrate(bitrate); err != nil {
		cancel()
		return nil, fmt.Errorf("无法配置 Opus 编码器: %w", err)
	}
	_ = encoder.SetComplexity(5)
	r.encoder = encoder
	r.bitrate = bitrate
	return r, nil
}

// Called exclusively by the encoder owner, never by control/UI goroutines.
func (r *engineRun) applyChannelBitrate() error {
	if r.bitrateSource == nil {
		return nil
	}
	bitrate := r.bitrateSource.VoiceBitrate()
	if bitrate == r.bitrate {
		return nil
	}
	if bitrate < 16000 || bitrate > 64000 {
		return errors.New("invalid channel bitrate")
	}
	if err := r.encoder.SetBitrate(bitrate); err != nil {
		return err
	}
	r.bitrate = bitrate
	return nil
}

func (r *engineRun) start() {
	if r.engine.monitor {
		r.wg.Add(1)
		go func() { defer r.wg.Done(); r.encodeLoop() }()
		return
	}
	r.wg.Add(2)
	go func() { defer r.wg.Done(); r.encodeLoop() }()
	go func() { defer r.wg.Done(); r.mixLoop() }()
}

func (r *engineRun) stop() {
	r.stopOnce.Do(func() {
		r.allowSend.Store(false)
		r.cancel()
		if r.devices != nil {
			_ = r.devices.Close()
		}
		r.captureMu.Lock()
		r.capturePCM.Reset()
		r.captureMu.Unlock()
		r.playbackPCM.Reset()
		r.wg.Wait()
		if r.processor != nil {
			r.processor.Close()
		}
	})
}

func (r *engineRun) enqueue(packet Packet) {
	if r.ctx.Err() != nil || packet.SenderID == 0 || (!packet.End && len(packet.Data) == 0) || len(packet.Data) > MaxOpusPacketSize {
		return
	}
	if packet.End {
		packet.Data = nil
	} else {
		packet.Data = append([]byte(nil), packet.Data...)
	}
	if packet.ReceivedAt.IsZero() {
		packet.ReceivedAt = time.Now()
	}
	select {
	case r.incoming <- packet:
		r.diagnostics.received.Add(1)
	default:
		r.diagnostics.queueDrops.Add(1)
	}
}

func (r *engineRun) capture(samples []float32) {
	if !r.allowSend.Load() || r.ctx.Err() != nil {
		return
	}
	if !r.isMonitor() && r.config.ActivationMode == "ptt" && !r.ptt.Load() {
		return
	}
	if !r.captureMu.TryLock() {
		return
	}
	if !r.allowSend.Load() || (!r.isMonitor() && r.config.ActivationMode == "ptt" && !r.ptt.Load()) {
		r.captureMu.Unlock()
		return
	}
	n := r.capturePCM.Push(samples)
	if n < len(samples) {
		r.diagnostics.captureDrops.Add(uint64(len(samples) - n))
	}
	r.captureMu.Unlock()
	select {
	case r.captureWake <- struct{}{}:
	default:
	}
}

func (r *engineRun) playback(samples []float32) {
	clear(samples)
	if r.ctx.Err() != nil {
		return
	}
	r.playbackPCM.Pop(samples)
	if r.config.LocalMonitor && r.allowSend.Load() {
		r.monitorPCM.MixInto(samples)
	}
	config := r.currentConfig()
	if backend := config.Processing.Preprocess[0].Backend; backend == "speex" || backend == "webrtc" {
		r.referencePCM.Push(samples)
	}
}

func (r *engineRun) currentConfig() VoiceConfig {
	if config := r.liveConfig.Load(); config != nil {
		return *config
	}
	return r.config
}

func (r *engineRun) isMonitor() bool { return r.engine.monitor || r.config.LocalMonitor }

// SuspendCapture closes both monitoring and network gates before queued device work.
func (e *Engine) SuspendCapture() {
	e.mu.RLock()
	run := e.run
	e.mu.RUnlock()
	if run != nil {
		run.allowSend.Store(false)
		run.ptt.Store(false)
	}
}

func (r *engineRun) encodeLoop() {
	channels := 1
	if r.codec == CodecOpusMusic {
		channels = 2
	}
	mono := make([]float32, FrameSamples)
	pcm := make([]float32, FrameSamples*channels)
	encoded := make([]byte, MaxOpusPacketSize)
	referenceStereo := make([]float32, FrameSamples*2)
	reference := make([]float32, FrameSamples)
	for {
		select {
		case <-r.ctx.Done():
			return
		case update := <-r.encodeControl:
			old := r.processor
			r.processor = update.processor
			r.referencePCM.Reset()
			if old != nil {
				old.Close()
			}
			update.ack <- struct{}{}
		case <-r.captureWake:
		}
		for r.capturePCM.Available() >= FrameSamples {
			if r.ctx.Err() != nil || !r.allowSend.Load() {
				r.captureMu.Lock()
				r.capturePCM.Reset()
				r.captureMu.Unlock()
				break
			}
			r.captureMu.Lock()
			if r.capturePCM.Available() < FrameSamples {
				r.captureMu.Unlock()
				break
			}
			epoch := r.captureEpoch.Load()
			r.capturePCM.Pop(mono)
			r.captureMu.Unlock()
			if r.processor != nil {
				clear(referenceStereo)
				r.referencePCM.Pop(referenceStereo)
				for i := range reference {
					reference[i] = (referenceStereo[i*2] + referenceStereo[i*2+1]) / 2
				}
				r.processor.Process(mono, reference)
			}
			now := time.Now()
			level := inputLevelDB(mono)
			applyInputGain(mono, r.currentConfig().InputGain)
			if r.isMonitor() && now.Sub(r.lastMeter) >= 100*time.Millisecond {
				r.lastMeter = now
				r.engine.setInputLevel(r, inputLevelDB(mono))
			}
			if r.isMonitor() {
				for i, sample := range mono {
					volume := float32(r.currentConfig().Volume) / 100
					referenceStereo[i*2], referenceStereo[i*2+1] = sample*volume, sample*volume
				}
				if r.config.LocalMonitor {
					r.monitorPCM.Push(referenceStereo)
				} else {
					r.playbackPCM.Push(referenceStereo)
				}
				continue
			}
			if !r.activationOpen(level, now) {
				continue
			}
			active := pcmHasActivity(mono)
			if channels == 1 {
				copy(pcm, mono)
			} else {
				for i, sample := range mono {
					pcm[i*2], pcm[i*2+1] = sample, sample
				}
			}
			if err := r.applyChannelBitrate(); err != nil {
				r.fail(fmt.Errorf("无法更新频道码率: %w", err), true)
				return
			}
			n, encodeErr := r.encoder.Encode(pcm, encoded)
			if encodeErr != nil {
				r.fail(fmt.Errorf("Opus 编码失败: %w", encodeErr), true)
				return
			}
			r.sendMu.Lock()
			if r.ctx.Err() != nil || !r.allowSend.Load() || (r.config.ActivationMode == "ptt" && (!r.ptt.Load() || epoch != r.captureEpoch.Load())) {
				r.sendMu.Unlock()
				break
			}
			sendErr := r.engine.transport.SendVoice(encoded[:n], r.codec)
			r.sendMu.Unlock()
			if sendErr != nil {
				r.diagnostics.sendErrors.Add(1)
				r.fail(fmt.Errorf("语音发送失败: %w", sendErr), true)
				return
			}
			r.diagnostics.sent.Add(1)
			if active {
				r.markLocalActivity(time.Now())
			}
		}
	}
}

func (r *engineRun) mixLoop() {
	receive := receiveChain{}
	defer receive.close()
	postprocess := r.config.Processing.Postprocess
	diagnostics := voiceDiagnostics{last: time.Now(), peers: make(map[uint16]*peerCounters, maxSpeakers)}
	defer func() { diagnostics.report(&r.diagnostics, time.Now(), true) }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	speakers := make(map[uint16]*speaker)
	mix := make([]float32, FrameSamples*2)
	for {
		select {
		case <-r.ctx.Done():
			return
		case update := <-r.mixControl:
			receive.close()
			postprocess = update.specs
			update.ack <- struct{}{}
		case packet := <-r.incoming:
			stats := diagnostics.peer(packet.SenderID)
			if stats != nil {
				stats.received++
			}
			if _, current := peerGain(r.engine.peers.Load(), packet.SenderID, packet.Instance); !current {
				if stats != nil {
					stats.unknownPeer++
				}
				continue
			}
			if previous := speakers[packet.SenderID]; previous != nil && previous.instance != packet.Instance {
				delete(speakers, packet.SenderID)
			}
			if packet.End {
				s := speakers[packet.SenderID]
				if s != nil && s.codec == packet.Codec && s.jitter.Push(packet) {
					s.lastReceived = packet.ReceivedAt
				}
				continue
			}
			if !packet.Codec.Supported() {
				r.fail(fmt.Errorf("收到不支持的语音编码: %s", packet.Codec), false)
				continue
			}
			if _, err := gopus.ParsePacket(packet.Data); err != nil {
				if stats != nil {
					stats.decodeErrors++
				}
				r.recordDecodeFailure(packet.ReceivedAt, packet.SenderID, packet.Codec, len(packet.Data), err)
				if s := speakers[packet.SenderID]; s != nil && s.codec == packet.Codec {
					packet.invalid = true
					s.jitter.Push(packet)
				}
				continue
			}
			s := speakers[packet.SenderID]
			if s == nil || s.codec != packet.Codec {
				if s == nil && len(speakers) >= maxSpeakers {
					r.fail(fmt.Errorf("同时发言人数超过音频上限 %d", maxSpeakers), false)
					continue
				}
				var err error
				s, err = newSpeaker(packet.Codec)
				if err != nil {
					r.fail(err, false)
					continue
				}
				speakers[packet.SenderID] = s
				s.instance = packet.Instance
			}
			if s.jitter.Push(packet) {
				s.lastReceived = packet.ReceivedAt
			} else if stats != nil {
				stats.jitterDrops++
			}
		case now := <-ticker.C:
			diagnostics.report(&r.diagnostics, now, false)
			r.expireActivity(now)
			clear(mix)
			mixed := false
			peers := r.engine.peers.Load()
			config := r.currentConfig()
			if postprocess[0].Backend == "none" || postprocess[0].Backend == "" {
				receive.close()
			} else {
				receive.prune(now, func(id uint16, instance string) bool {
					_, current := peerGain(peers, id, instance)
					return current
				})
			}
			volume := float32(config.Volume) / 100
			for id, speaker := range speakers {
				gain, current := peerGain(peers, id, speaker.instance)
				if !current {
					delete(speakers, id)
					continue
				}
				// A partial output frame must not pin stale decoder/sequence state.
				if now.Sub(speaker.lastReceived) > speakerIdle {
					delete(speakers, id)
					continue
				}
				speaker.receive = nil
				if postprocess[0].Backend != "none" && postprocess[0].Backend != "" && speaker.codec == CodecOpusVoice && gain != 0 && volume != 0 {
					var err error
					speaker.receive, err = receive.peer(id, speaker.instance, ProcessingConfig{Postprocess: postprocess}, now)
					if err != nil {
						r.fail(fmt.Errorf("无法初始化收听自动增益: %w", err), false)
					}
				}
				ok, decoded, active, finished, err := speaker.render(mix, volume*gain, now)
				stats := diagnostics.peer(id)
				if err != nil {
					if stats != nil {
						stats.decodeErrors++
					}
					delete(speakers, id)
					r.recordDecodeFailure(now, id, speaker.codec, speaker.lastPacketBytes, err)
					continue
				}
				if decoded {
					if stats != nil {
						stats.decoded++
						if gain == 0 || volume == 0 {
							stats.mutedFrames++
						}
					}
					r.recordDecodeSuccess(id)
				}
				if active {
					r.markRemoteActivity(id, now)
				}
				if finished {
					delete(speakers, id)
				}
				mixed = mixed || ok
			}
			if mixed {
				gain := float32(1)
				if r.currentConfig().Ducking {
					r.activityMu.Lock()
					if r.localUntil.After(now) {
						gain = 0.25
					}
					r.activityMu.Unlock()
				}
				r.limiter.apply(mix, gain)
				r.playbackPCM.Push(mix)
			}
		}
	}
}

type decodeFailure struct {
	windowStart time.Time
	count       int
}

func (r *engineRun) recordDecodeFailure(now time.Time, senderID uint16, codec Codec, packetBytes int, err error) {
	failure, exists := r.decodeFailures[senderID]
	if !exists && len(r.decodeFailures) >= maxSpeakers {
		for id, candidate := range r.decodeFailures {
			if now.Before(candidate.windowStart) || now.Sub(candidate.windowStart) > decodeErrorWindow {
				delete(r.decodeFailures, id)
			}
		}
		if len(r.decodeFailures) >= maxSpeakers {
			return
		}
	}
	if !exists || failure.windowStart.IsZero() || now.Before(failure.windowStart) || now.Sub(failure.windowStart) > decodeErrorWindow {
		failure = decodeFailure{windowStart: now}
	}
	failure.count++
	if failure.count > decodeErrorLimit {
		failure.count = decodeErrorLimit
	}
	r.decodeFailures[senderID] = failure
	if failure.count >= decodeErrorLimit {
		r.engine.setRunDecodeError(r, senderID, fmt.Errorf("Opus 解码持续失败（发送者=%d，codec=%s，帧长度=%d）: %w", senderID, codec, packetBytes, err))
	}
}

func (r *engineRun) recordDecodeSuccess(senderID uint16) {
	delete(r.decodeFailures, senderID)
	r.engine.clearRunDecodeError(r, senderID)
}

func (r *engineRun) markLocalActivity(now time.Time) {
	r.activityMu.Lock()
	changed := r.pruneActivityLocked(now)
	if !r.localUntil.After(now) {
		changed = true
	}
	r.localUntil = now.Add(speakingHold)
	r.publishActivityLocked(changed)
	r.activityMu.Unlock()
}

func (r *engineRun) markRemoteActivity(id uint16, now time.Time) {
	r.activityMu.Lock()
	changed := r.pruneActivityLocked(now)
	if !r.remoteUntil[id].After(now) {
		changed = true
	}
	r.remoteUntil[id] = now.Add(speakingHold)
	r.publishActivityLocked(changed)
	r.activityMu.Unlock()
}

func (r *engineRun) expireActivity(now time.Time) {
	r.activityMu.Lock()
	changed := r.pruneActivityLocked(now)
	r.publishActivityLocked(changed)
	r.activityMu.Unlock()
}

func (r *engineRun) pruneActivityLocked(now time.Time) bool {
	changed := false
	if !r.localUntil.IsZero() && !r.localUntil.After(now) {
		r.localUntil = time.Time{}
		changed = true
	}
	for id, until := range r.remoteUntil {
		if !until.After(now) {
			delete(r.remoteUntil, id)
			changed = true
		}
	}
	return changed
}

func (r *engineRun) publishActivityLocked(changed bool) {
	if !changed {
		return
	}
	r.activityVersion++
	clients := make([]uint16, 0, len(r.remoteUntil))
	for id := range r.remoteUntil {
		clients = append(clients, id)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i] < clients[j] })
	r.engine.applyRunActivity(r, r.activityVersion, !r.localUntil.IsZero(), clients)
}

func pcmHasActivity(samples []float32) bool {
	if len(samples) == 0 {
		return false
	}
	var sum float64
	for _, sample := range samples {
		sum += float64(sample * sample)
	}
	return sum/float64(len(samples)) >= speakingEnergy
}

func (r *engineRun) fail(err error, fatal bool) {
	if fatal {
		r.fatalOnce.Do(func() { r.engine.runError(r, err, true) })
		return
	}
	r.nonfatalOnce.Do(func() { r.engine.runError(r, err, false) })
}

type speaker struct {
	receive         *receiveProcessor
	instance        string
	codec           Codec
	channels        int
	decoder         *gopus.Decoder
	jitter          *jitterBuffer
	pcm             []float32
	decodeBuffer    []float32
	lastReceived    time.Time
	lastPacketBytes int
	ended           bool
}

func newSpeaker(codec Codec) (*speaker, error) {
	channels := 1
	if codec == CodecOpusMusic {
		channels = 2
	}
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(SampleRate, channels))
	if err != nil {
		return nil, fmt.Errorf("无法初始化 Opus 解码器: %w", err)
	}
	return &speaker{codec: codec, channels: channels, decoder: decoder, jitter: newJitterBuffer(), pcm: make([]float32, 0, FrameSamples*channels*6), decodeBuffer: make([]float32, 5760*channels)}, nil
}

func (s *speaker) render(mix []float32, volume float32, now time.Time) (mixed, decoded, active, finished bool, err error) {
	needed := FrameSamples * s.channels
	for len(s.pcm) < needed && !s.ended {
		packet, started, lost := s.jitter.Pop()
		if !started {
			break
		}
		if packet.End {
			s.ended = true
			break
		}
		if packet.invalid {
			break
		}
		if lost && now.Sub(s.lastReceived) > 120*time.Millisecond {
			break
		}
		data := packet.Data
		decodeBuffer := s.decodeBuffer
		if lost {
			data = nil
			decodeBuffer = decodeBuffer[:needed]
		}
		s.lastPacketBytes = len(data)
		n, err := s.decoder.Decode(data, decodeBuffer)
		if err != nil {
			return false, decoded, active, false, err
		}
		decodedSamples := s.decodeBuffer[:n*s.channels]
		if !lost {
			decoded = true
			active = active || pcmHasActivity(decodedSamples)
		}
		s.receive.process(decodedSamples, lost)
		s.pcm = append(s.pcm, decodedSamples...)
	}
	if len(s.pcm) < needed {
		if !s.ended || len(s.pcm) == 0 {
			return false, decoded, active, s.ended, nil
		}
		s.pcm = append(s.pcm, make([]float32, needed-len(s.pcm))...)
	}
	if s.channels == 1 {
		for i, sample := range s.pcm[:FrameSamples] {
			mix[i*2] += sample * volume
			mix[i*2+1] += sample * volume
		}
	} else {
		for i, sample := range s.pcm[:needed] {
			mix[i] += sample * volume
		}
	}
	remaining := copy(s.pcm, s.pcm[needed:])
	s.pcm = s.pcm[:remaining]
	return true, decoded, active, s.ended && len(s.pcm) == 0, nil
}
