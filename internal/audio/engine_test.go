package audio

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
)

type fakeTransport struct {
	mu      sync.Mutex
	handler func(Packet)
	codec   Codec
	err     error
	sendErr error
	mutes   [][2]bool
	sends   [][]byte
}

func (f *fakeTransport) SetVoiceHandler(handler func(Packet)) {
	f.mu.Lock()
	f.handler = handler
	f.mu.Unlock()
}
func (f *fakeTransport) VoiceCodec() (Codec, error) { return f.codec, f.err }
func (f *fakeTransport) SetVoiceMuted(_ context.Context, input, output bool) error {
	f.mu.Lock()
	f.mutes = append(f.mutes, [2]bool{input, output})
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) SendVoice(data []byte, _ Codec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sends = append(f.sends, append([]byte(nil), data...))
	return nil
}

func (f *fakeTransport) emit(packet Packet) {
	f.mu.Lock()
	h := f.handler
	f.mu.Unlock()
	if h != nil {
		h(packet)
	}
}

type fakeDeviceSession struct{ closed bool }

func (s *fakeDeviceSession) Close() error { s.closed = true; return nil }

type openCall struct {
	playback, capture bool
	callbacks         deviceCallbacks
	session           *fakeDeviceSession
}

type fakeDeviceFactory struct {
	mu      sync.Mutex
	opens   []*openCall
	entered chan struct{}
	release chan struct{}
}

func (f *fakeDeviceFactory) Devices() ([]Device, error) { return nil, nil }
func (f *fakeDeviceFactory) Open(_ VoiceConfig, playback, capture bool, callbacks deviceCallbacks) (deviceSession, error) {
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
		<-f.release
	}
	call := &openCall{playback: playback, capture: capture, callbacks: callbacks, session: &fakeDeviceSession{}}
	f.mu.Lock()
	f.opens = append(f.opens, call)
	f.mu.Unlock()
	return call.session, nil
}

func (f *fakeDeviceFactory) last() *openCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens[len(f.opens)-1]
}

func TestEngineMuteDeafenAndLifecycle(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	engine := newWithFactory(transport, nil, devices)
	if len(devices.opens) != 0 || engine.Status().Active {
		t.Fatal("New started audio")
	}
	config := VoiceConfig{Enabled: true, Muted: true, Volume: 75}
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if call := devices.last(); !call.playback || call.capture {
		t.Fatalf("muted open = %+v", call)
	}
	if !engine.Status().Active {
		t.Fatal("enabled engine is inactive")
	}

	config.Muted = false
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if call := devices.last(); !call.playback || !call.capture {
		t.Fatalf("unmuted open = %+v", call)
	}

	config.Deafened = true
	previousOpens := len(devices.opens)
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if len(devices.opens) != previousOpens || !engine.Status().Active || engine.Status().Config.Muted {
		t.Fatal("deafen did not preserve unmuted choice")
	}
	transport.mu.Lock()
	lastMute := transport.mutes[len(transport.mutes)-1]
	transport.mu.Unlock()
	if lastMute != [2]bool{true, true} {
		t.Fatalf("deafen mute = %v", lastMute)
	}

	config.Deafened = false
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if !devices.last().capture {
		t.Fatal("undeafen did not restore capture")
	}
	config.Enabled = false
	if err := engine.Configure(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if engine.Status().Active {
		t.Fatal("disabled engine is active")
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Configure(context.Background(), config); !errors.Is(err, ErrClosed) {
		t.Fatalf("Configure after Close = %v", err)
	}
}

func TestConfigureWaitHonorsCancellation(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{entered: make(chan struct{}, 1), release: make(chan struct{})}
	engine := newWithFactory(transport, nil, devices)
	done := make(chan error, 1)
	go func() {
		done <- engine.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: true, Volume: 100})
	}()
	<-devices.entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := engine.Configure(ctx, VoiceConfig{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting Configure = %v", err)
	}
	close(devices.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = engine.Close()
}

func TestEngineEncodesOpusVoiceAndMusic(t *testing.T) {
	for _, codec := range []Codec{CodecOpusVoice, CodecOpusMusic} {
		t.Run(codec.String(), func(t *testing.T) {
			transport := &fakeTransport{codec: codec}
			devices := &fakeDeviceFactory{}
			engine := newWithFactory(transport, nil, devices)
			if err := engine.Configure(context.Background(), VoiceConfig{Enabled: true, Volume: 100}); err != nil {
				t.Fatal(err)
			}
			input := make([]float32, FrameSamples)
			for i := range input {
				input[i] = float32(math.Sin(2*math.Pi*440*float64(i)/SampleRate)) * 0.25
			}
			devices.last().callbacks.capture(input)
			waitUntil(t, func() bool { transport.mu.Lock(); defer transport.mu.Unlock(); return len(transport.sends) == 1 })
			transport.mu.Lock()
			packet := append([]byte(nil), transport.sends[0]...)
			transport.mu.Unlock()
			channels := 1
			if codec == CodecOpusMusic {
				channels = 2
			}
			decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(SampleRate, channels))
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, 5760*channels)
			n, err := decoder.Decode(packet, pcm)
			if err != nil || n != FrameSamples {
				t.Fatalf("decode n=%d err=%v packet=%d", n, err, len(packet))
			}
			_ = engine.Close()
		})
	}
}

func TestEngineJittersDecodesAndMixesIncomingVoice(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	engine := newWithFactory(transport, nil, devices)
	if err := engine.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: true, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	encoder, _ := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: SampleRate, Channels: 1, Application: gopus.ApplicationVoIP})
	pcm, encoded := make([]float32, FrameSamples), make([]byte, MaxOpusPacketSize)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2*math.Pi*330*float64(i)/SampleRate)) * 0.3
	}
	n, err := encoder.Encode(pcm, encoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []uint16{40, 42, 41} {
		transport.emit(Packet{Sequence: sequence, SenderID: 9, Codec: CodecOpusVoice, Data: encoded[:n], ReceivedAt: time.Now()})
	}
	out := make([]float32, FrameSamples*2)
	waitUntil(t, func() bool {
		clear(out)
		devices.last().callbacks.playback(out)
		for _, sample := range out {
			if sample != 0 {
				return true
			}
		}
		return false
	})
	_ = engine.Close()
}

func TestSpeakerPLCConcealsOneMixerFrame(t *testing.T) {
	speaker, err := newSpeaker(CodecOpusVoice)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: SampleRate, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		t.Fatal(err)
	}
	packetData := make([]byte, MaxOpusPacketSize)
	n, err := encoder.Encode(make([]float32, FrameSamples), packetData)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	speaker.lastReceived = now
	for _, sequence := range []uint16{1, 2, 4} {
		speaker.jitter.Push(Packet{Sequence: sequence, SenderID: 1, Codec: CodecOpusVoice, Data: packetData[:n], ReceivedAt: now})
	}
	mix := make([]float32, FrameSamples*2)
	for range 3 {
		if _, _, _, _, err := speaker.render(mix, 1, now); err != nil {
			t.Fatal(err)
		}
	}
	if duration := speaker.decoder.LastPacketDuration(); duration != FrameSamples {
		t.Fatalf("PLC duration = %d samples, want %d (20ms)", duration, FrameSamples)
	}
	if len(speaker.pcm) != 0 {
		t.Fatalf("one lost packet queued %d excess concealed samples", len(speaker.pcm))
	}
}

func TestSpeakerEndFlushesShortBurstWithoutPLC(t *testing.T) {
	speaker, err := newSpeaker(CodecOpusVoice)
	if err != nil {
		t.Fatal(err)
	}
	encoder, _ := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: SampleRate, Channels: 1, Application: gopus.ApplicationVoIP})
	pcm, encoded := make([]float32, FrameSamples), make([]byte, MaxOpusPacketSize)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2*math.Pi*220*float64(i)/SampleRate)) * 0.2
	}
	n, err := encoder.Encode(pcm, encoded)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	speaker.lastReceived = now
	speaker.jitter.Push(Packet{Sequence: 1, Data: encoded[:n]})
	speaker.jitter.Push(Packet{Sequence: 2, End: true})
	mix := make([]float32, FrameSamples*2)
	mixed, decoded, active, finished, err := speaker.render(mix, 1, now)
	if err != nil || !mixed || !decoded || !active || finished {
		t.Fatalf("voice render mixed=%v decoded=%v active=%v finished=%v err=%v", mixed, decoded, active, finished, err)
	}
	clear(mix)
	mixed, decoded, active, finished, err = speaker.render(mix, 1, now.Add(20*time.Millisecond))
	if err != nil || mixed || decoded || active || !finished {
		t.Fatalf("end render mixed=%v decoded=%v active=%v finished=%v err=%v", mixed, decoded, active, finished, err)
	}
}

func TestPersistentMalformedOpusIsPerSenderAndRecovers(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	engine := newWithFactory(transport, nil, devices)
	if err := engine.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: true, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()

	transport.emit(Packet{Sequence: 1, SenderID: 7, Codec: CodecOpusVoice, Data: []byte{0x02}})
	time.Sleep(50 * time.Millisecond)
	if errText := engine.Status().Error; errText != "" {
		t.Fatalf("isolated malformed packet became visible: %q", errText)
	}
	for sequence := uint16(2); sequence <= 3; sequence++ {
		transport.emit(Packet{Sequence: sequence, SenderID: 7, Codec: CodecOpusVoice, Data: []byte{0x02}})
	}
	waitUntil(t, func() bool {
		return strings.Contains(engine.Status().Error, "发送者=7") && strings.Contains(engine.Status().Error, "帧长度=1")
	})

	valid := encodedTonePacket(t, CodecOpusVoice)
	for sequence := uint16(1); sequence <= 3; sequence++ {
		transport.emit(Packet{Sequence: sequence, SenderID: 8, Codec: CodecOpusVoice, Data: valid})
	}
	time.Sleep(100 * time.Millisecond)
	if !strings.Contains(engine.Status().Error, "发送者=7") {
		t.Fatalf("another sender cleared the decode error: %q", engine.Status().Error)
	}
	for sequence := uint16(4); sequence <= 6; sequence++ {
		transport.emit(Packet{Sequence: sequence, SenderID: 7, Codec: CodecOpusVoice, Data: valid})
	}
	waitUntil(t, func() bool { return engine.Status().Error == "" })
}

func TestSpeakingActivityUsesDecodedAndSuccessfullySentAudio(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	notified := make(chan VoiceState, 16)
	engine := newWithFactory(transport, func(state VoiceState) {
		if len(state.SpeakingClientIDs) > 0 {
			state.SpeakingClientIDs[0] = 999
		}
		notified <- state
	}, devices)
	if err := engine.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: false, Volume: 0, InputGain: 100}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()

	valid := encodedTonePacket(t, CodecOpusVoice)
	for sequence := uint16(1); sequence <= 3; sequence++ {
		transport.emit(Packet{Sequence: sequence, SenderID: 9, Codec: CodecOpusVoice, Data: valid})
	}
	waitUntil(t, func() bool {
		state := engine.Status()
		return len(state.SpeakingClientIDs) == 1 && state.SpeakingClientIDs[0] == 9
	})
	state := engine.Status()
	state.SpeakingClientIDs[0] = 123
	if got := engine.Status().SpeakingClientIDs; len(got) != 1 || got[0] != 9 {
		t.Fatalf("Status shared speaking slice: %v", got)
	}
	waitUntil(t, func() bool { return len(engine.Status().SpeakingClientIDs) == 0 })

	tone := make([]float32, FrameSamples)
	for i := range tone {
		tone[i] = float32(math.Sin(2*math.Pi*440*float64(i)/SampleRate)) * 0.2
	}
	devices.last().callbacks.capture(tone)
	waitUntil(t, func() bool { return engine.Status().LocalSpeaking })
	waitUntil(t, func() bool { return !engine.Status().LocalSpeaking })
	select {
	case <-notified:
	default:
	}
}

func TestFatalRunCannotRestoreSpeakingActivity(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	engine := newWithFactory(transport, nil, &fakeDeviceFactory{})
	if err := engine.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: true, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	engine.mu.RLock()
	run := engine.run
	engine.mu.RUnlock()
	engine.runError(run, errors.New("fatal"), true)
	run.markLocalActivity(time.Now())
	run.markRemoteActivity(7, time.Now())
	state := engine.Status()
	if state.Active || state.LocalSpeaking || len(state.SpeakingClientIDs) != 0 {
		t.Fatalf("fatal run restored activity: %+v", state)
	}
	_ = engine.Close()
}

func encodedTonePacket(t *testing.T, codec Codec) []byte {
	t.Helper()
	channels, application := 1, gopus.ApplicationVoIP
	if codec == CodecOpusMusic {
		channels, application = 2, gopus.ApplicationAudio
	}
	encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: SampleRate, Channels: channels, Application: application})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, FrameSamples*channels)
	for i := 0; i < FrameSamples; i++ {
		sample := float32(math.Sin(2*math.Pi*330*float64(i)/SampleRate)) * 0.2
		for channel := 0; channel < channels; channel++ {
			pcm[i*channels+channel] = sample
		}
	}
	encoded := make([]byte, MaxOpusPacketSize)
	n, err := encoder.Encode(pcm, encoded)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), encoded[:n]...)
}

func TestIncomingSpeakerCountIsBounded(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice}
	devices := &fakeDeviceFactory{}
	engine := newWithFactory(transport, nil, devices)
	if err := engine.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: true, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	for sender := 1; sender <= maxSpeakers+1; sender++ {
		transport.emit(Packet{Sequence: 1, SenderID: uint16(sender), Codec: CodecOpusVoice, Data: []byte{1}, ReceivedAt: time.Now()})
	}
	waitUntil(t, func() bool { return engine.Status().Error == "同时发言人数超过音频上限 64" })
}

func TestFatalSendErrorAfterNonfatalPacketErrorStopsRun(t *testing.T) {
	transport := &fakeTransport{codec: CodecOpusVoice, sendErr: errors.New("send failed")}
	devices := &fakeDeviceFactory{}
	engine := newWithFactory(transport, nil, devices)
	if err := engine.Configure(context.Background(), VoiceConfig{Enabled: true, Muted: false, Volume: 100}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()
	transport.emit(Packet{Sequence: 1, SenderID: 1, Codec: Codec(3), Data: []byte{1}, ReceivedAt: time.Now()})
	waitUntil(t, func() bool { return engine.Status().Error == "收到不支持的语音编码: unsupported(3)" })
	devices.last().callbacks.capture(make([]float32, FrameSamples))
	waitUntil(t, func() bool {
		state := engine.Status()
		return !state.Active && state.Error == "语音发送失败: send failed"
	})
}

func waitUntil(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition did not become ready")
}
