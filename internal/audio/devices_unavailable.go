//go:build !cgo || (!darwin && !windows)

package audio

import "errors"

type nativeDeviceFactory struct{}

func (nativeDeviceFactory) Devices() ([]Device, error) {
	return nil, errors.New("当前平台构建未启用 CoreAudio 或 WASAPI 音频后端")
}

func (nativeDeviceFactory) Open(VoiceConfig, bool, bool, deviceCallbacks) (deviceSession, error) {
	return nil, errors.New("当前平台构建未启用 CoreAudio 或 WASAPI 音频后端")
}
