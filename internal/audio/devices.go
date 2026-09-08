package audio

type deviceCallbacks struct {
	capture  func([]float32)
	playback func([]float32)
}

type deviceSession interface {
	Close() error
}

type deviceFactory interface {
	Devices() ([]Device, error)
	Open(VoiceConfig, bool, bool, deviceCallbacks) (deviceSession, error)
}

var defaultDevices deviceFactory = nativeDeviceFactory{}

func Devices() ([]Device, error) { return defaultDevices.Devices() }
