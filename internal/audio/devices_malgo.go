//go:build cgo && (darwin || windows)

package audio

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/gen2brain/malgo"
)

type nativeDeviceFactory struct{}

func (nativeDeviceFactory) Devices() ([]Device, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{ThreadPriority: malgo.ThreadPriorityNormal}, nil)
	if err != nil {
		return nil, fmt.Errorf("无法初始化音频系统: %w", err)
	}
	defer func() { _ = ctx.Uninit(); ctx.Free() }()
	var result []Device
	for _, entry := range []struct {
		kind       malgo.DeviceType
		publicKind DeviceKind
	}{{malgo.Playback, DeviceOutput}, {malgo.Capture, DeviceInput}} {
		devices, listErr := ctx.Devices(entry.kind)
		if listErr != nil {
			return nil, fmt.Errorf("无法读取音频设备: %w", listErr)
		}
		for i := range devices {
			result = append(result, Device{ID: devices[i].ID.String(), Name: devices[i].Name(), Kind: entry.publicKind, Default: devices[i].IsDefault != 0})
		}
	}
	return result, nil
}

type malgoSession struct {
	ctx      *malgo.AllocatedContext
	playback *malgo.Device
	capture  *malgo.Device
	once     sync.Once
	err      error
}

func (nativeDeviceFactory) Open(config VoiceConfig, playback, capture bool, callbacks deviceCallbacks) (deviceSession, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{ThreadPriority: malgo.ThreadPriorityHighest}, nil)
	if err != nil {
		return nil, fmt.Errorf("无法初始化音频系统: %w", err)
	}
	session := &malgoSession{ctx: ctx}
	fail := func(openErr error) (deviceSession, error) {
		_ = session.Close()
		return nil, openErr
	}
	if playback {
		device, openErr := openMalgoDevice(ctx, malgo.Playback, config.OutputDeviceID, callbacks)
		if openErr != nil {
			return fail(fmt.Errorf("无法打开扬声器设备: %w", openErr))
		}
		session.playback = device
		if startErr := device.Start(); startErr != nil {
			return fail(fmt.Errorf("无法启动扬声器设备: %w", startErr))
		}
	}
	if capture {
		device, openErr := openMalgoDevice(ctx, malgo.Capture, config.InputDeviceID, callbacks)
		if openErr != nil {
			return fail(captureError(openErr))
		}
		session.capture = device
		if startErr := device.Start(); startErr != nil {
			return fail(captureError(startErr))
		}
	}
	return session, nil
}

func openMalgoDevice(ctx *malgo.AllocatedContext, kind malgo.DeviceType, selected string, callbacks deviceCallbacks) (*malgo.Device, error) {
	config := malgo.DefaultDeviceConfig(kind)
	config.SampleRate = SampleRate
	config.PeriodSizeInFrames = FrameSamples
	config.PerformanceProfile = malgo.LowLatency
	if kind == malgo.Playback {
		config.Playback.Format = malgo.FormatF32
		config.Playback.Channels = 2
	} else {
		config.Capture.Format = malgo.FormatF32
		config.Capture.Channels = 1
	}
	var selectedID malgo.DeviceID
	var selectedIDPin runtime.Pinner
	if selected != "" {
		devices, err := ctx.Devices(kind)
		if err != nil {
			return nil, err
		}
		found := false
		for i := range devices {
			if devices[i].ID.String() == selected {
				selectedID, found = devices[i].ID, true
				break
			}
		}
		if !found {
			return nil, errors.New("所选音频设备不存在")
		}
		selectedIDPin.Pin(&selectedID)
		defer selectedIDPin.Unpin()
		if kind == malgo.Playback {
			config.Playback.DeviceID = unsafe.Pointer(&selectedID)
		} else {
			config.Capture.DeviceID = unsafe.Pointer(&selectedID)
		}
	}
	device, err := malgo.InitDevice(ctx.Context, config, malgo.DeviceCallbacks{Data: func(output, input []byte, _ uint32) {
		if len(input) >= 4 && callbacks.capture != nil {
			callbacks.capture(float32Bytes(input))
		}
		if len(output) >= 4 && callbacks.playback != nil {
			out := float32Bytes(output)
			clear(out)
			callbacks.playback(out)
		}
	}})
	return device, err
}

func float32Bytes(data []byte) []float32 {
	return unsafe.Slice((*float32)(unsafe.Pointer(&data[0])), len(data)/4)
}

func captureError(err error) error {
	if runtime.GOOS == "darwin" {
		return fmt.Errorf("无法访问麦克风，请在系统设置中允许 Resona 使用麦克风: %w", err)
	}
	return fmt.Errorf("无法打开麦克风设备: %w", err)
}

func (s *malgoSession) Close() error {
	s.once.Do(func() {
		if s.capture != nil {
			if err := s.capture.Stop(); err != nil {
				s.err = err
			}
			s.capture.Uninit()
		}
		if s.playback != nil {
			if err := s.playback.Stop(); err != nil && s.err == nil {
				s.err = err
			}
			s.playback.Uninit()
		}
		if s.ctx != nil {
			if err := s.ctx.Uninit(); err != nil && s.err == nil {
				s.err = err
			}
			s.ctx.Free()
		}
	})
	return s.err
}
