// Package desktopipc exposes the local client through inherited process pipes.
// It never binds a network socket and never transports microphone samples.
package desktopipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"runtime"
	"sync"

	"github.com/tsukiyoz/resona/internal/audio"
	"github.com/tsukiyoz/resona/internal/client"
)

const maxRequestBytes = 1 << 20

type request struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type envelope struct {
	ID     uint64 `json:"id,omitempty"`
	Event  string `json:"event,omitempty"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Run owns the provided pipes until EOF, cancellation or Shutdown. Commands
// and events share one writer lock, preserving state order across responses.
func Run(ctx context.Context, service *client.Service, input io.ReadCloser, output io.WriteCloser) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer service.Shutdown()
	sounds := newSoundPlayer(ctx)
	defer func() { cancel(); sounds.stop(); <-sounds.done }()
	changes, unsubscribe := service.SubscribeChanges()
	defer unsubscribe()
	var writerMu sync.Mutex
	encoder := json.NewEncoder(output)
	resourceSync := newResourceSync()
	requests := make(chan request, 16)
	failures := make(chan error, 1)
	fail := func(err error) {
		select {
		case failures <- err:
		default:
		}
		cancel()
	}
	var workers sync.WaitGroup
	readSlots := make(chan struct{}, 4)
	adminSlots := make(chan struct{}, 1)
	var shutdownResult *shutdownStatus
	var cancelDetails context.CancelFunc
	defer func() {
		if cancelDetails != nil {
			cancelDetails()
		}
	}()
	workers.Add(4)
	go func() {
		defer workers.Done()
		resourceSync.run(ctx, service, func(interest resourceInterest, session string, syncErr error) {
			if !interest.Active {
				return
			}
			writerMu.Lock()
			defer writerMu.Unlock()
			if interest != resourceSync.current() || session != service.ResourceSessionID() {
				return
			}
			workspace, err := service.GetWorkspace()
			if err != nil {
				return
			}
			message := ""
			if syncErr != nil {
				message = "资源同步未完成，稍后重试或重新切回窗口"
			}
			if encoder.Encode(envelope{Event: "resourceSync", Result: struct {
				Generation     uint64            `json:"generation"`
				Workspace      client.Workspace  `json:"workspace"`
				Voice          client.VoiceState `json:"voice"`
				MicrophoneTest client.VoiceState `json:"microphoneTest"`
				Error          string            `json:"error"`
			}{interest.Generation, workspace, service.GetVoiceState(), service.GetMicrophoneTest(), message}}) != nil {
				fail(errors.New("desktop output unavailable"))
			}
		})
	}()
	go func() {
		defer workers.Done()
		<-ctx.Done()
		_ = input.Close()
		_ = output.Close()
	}()
	go func() {
		defer workers.Done()
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), maxRequestBytes)
		for scanner.Scan() {
			var req request
			if json.Unmarshal(scanner.Bytes(), &req) != nil || req.ID == 0 || req.Method == "" {
				fail(errors.New("invalid desktop request"))
				return
			}
			select {
			case requests <- req:
			case <-ctx.Done():
				return
			}
		}
		if scanner.Err() != nil && ctx.Err() == nil {
			fail(errors.New("desktop input unavailable"))
		} else {
			cancel()
		}
	}()
	go func() {
		defer workers.Done()
		var previous *client.Workspace
		var previousVoice *client.VoiceState
		var previousTest *client.VoiceState
		var lastNotification string
		for {
			select {
			case <-ctx.Done():
				return
			case <-changes:
			}
			writerMu.Lock()
			active := resourceSync.current().Active
			var workspace client.Workspace
			var err error
			publish := publishWorkspace(active, previous, client.Workspace{Session: service.GetSessionState()})
			if publish {
				workspace, err = service.GetWorkspace()
			}
			voice := service.GetVoiceState()
			microphoneTest := service.GetMicrophoneTest()
			if !active {
				voice = backgroundVoice(voice)
				microphoneTest = backgroundVoice(microphoneTest)
			}
			if err == nil && publish && (previous == nil || !reflect.DeepEqual(*previous, workspace)) {
				err = encoder.Encode(envelope{Event: "workspace", Result: workspace})
				previous = &workspace
				if len(workspace.Notifications) > 0 {
					lastNotification = workspace.Notifications[len(workspace.Notifications)-1].ID
				}
			}
			if !active && err == nil {
				if notifications := service.GetNotificationUpdate(lastNotification); notifications != nil {
					err = encoder.Encode(envelope{Event: "notifications", Result: notifications})
					lastNotification = notifications.Notifications[len(notifications.Notifications)-1].ID
				}
			}
			if err == nil && (previousVoice == nil || !reflect.DeepEqual(*previousVoice, voice)) {
				err = encoder.Encode(envelope{Event: "voice", Result: voice})
				previousVoice = &voice
			}
			if err == nil && (previousTest == nil || !reflect.DeepEqual(*previousTest, microphoneTest)) {
				err = encoder.Encode(envelope{Event: "microphoneTest", Result: microphoneTest})
				previousTest = &microphoneTest
			}
			writerMu.Unlock()
			if err != nil {
				fail(errors.New("desktop output unavailable"))
				return
			}
		}
	}()
	defer func() { cancel(); workers.Wait() }()
	for {
		select {
		case <-ctx.Done():
			select {
			case err := <-failures:
				return err
			default:
				return nil
			}
		case req := <-requests:
			if req.Method == "CreateChannel" || req.Method == "UpdateChannel" || req.Method == "DeleteChannel" || req.Method == "ClaimServerOwner" {
				select {
				case adminSlots <- struct{}{}:
					workers.Add(1)
					go func(req request) {
						defer workers.Done()
						defer func() { <-adminSlots }()
						result, err := dispatchWithContext(ctx, service, req)
						response := envelope{ID: req.ID, Result: result}
						if err != nil {
							response.Result = nil
							response.Error = err.Error()
						}
						writerMu.Lock()
						writeErr := encoder.Encode(response)
						writerMu.Unlock()
						if writeErr != nil {
							fail(errors.New("desktop response unavailable"))
						}
					}(req)
				default:
					writerMu.Lock()
					writeErr := encoder.Encode(envelope{ID: req.ID, Error: "服务器管理操作正在进行"})
					writerMu.Unlock()
					if writeErr != nil {
						return errors.New("desktop response unavailable")
					}
				}
				continue
			}
			isDetails := req.Method == "GetChannelDetails" || req.Method == "GetUserDetails"
			if isDetails || req.Method == "SelectChannel" || req.Method == "CancelDetails" {
				if cancelDetails != nil {
					cancelDetails()
					cancelDetails = nil
				}
			}
			if req.Method == "CancelDetails" {
				writerMu.Lock()
				err := encoder.Encode(envelope{ID: req.ID, Result: true})
				writerMu.Unlock()
				if err != nil {
					return errors.New("desktop response unavailable")
				}
				continue
			}
			if req.Method == "ExportDiagnostics" || req.Method == "GetIconResource" || req.Method == "GetChannelDetails" || req.Method == "GetUserDetails" {
				readContext := ctx
				if isDetails {
					readContext, cancelDetails = context.WithCancel(ctx)
				}
				select {
				case readSlots <- struct{}{}:
				default:
					writerMu.Lock()
					err := encoder.Encode(envelope{ID: req.ID, Error: "资源读取繁忙，请稍后重试"})
					writerMu.Unlock()
					if err != nil {
						return errors.New("desktop response unavailable")
					}
					continue
				}
				workers.Add(1)
				go func(req request) {
					defer workers.Done()
					defer func() { <-readSlots }()
					result, err := dispatchRead(readContext, service, req)
					response := envelope{ID: req.ID, Result: result}
					if err != nil {
						response.Result = nil
						response.Error = readErrorMessage(err)
					}
					writerMu.Lock()
					writeErr := encoder.Encode(response)
					writerMu.Unlock()
					if writeErr != nil {
						fail(errors.New("desktop response unavailable"))
					}
				}(req)
				continue
			}
			if req.Method == "PrepareShutdown" {
				var p struct {
					NotificationEnabled bool `json:"notificationEnabled"`
					NotificationVolume  int  `json:"notificationVolume"`
				}
				err := decodeParams(req.Params, &p)
				if err == nil && (p.NotificationVolume < 0 || p.NotificationVolume > 100) {
					err = errors.New("提示音音量无效")
				}
				if err == nil && shutdownResult == nil {
					sounds.stop()
					status := prepareShutdown(ctx, service, p.NotificationEnabled, p.NotificationVolume, audio.PlayNotification)
					shutdownResult = &status
				}
				response := envelope{ID: req.ID, Result: shutdownResult}
				if err != nil {
					response.Error = err.Error()
				}
				writerMu.Lock()
				writeErr := encoder.Encode(response)
				writerMu.Unlock()
				if writeErr != nil {
					return errors.New("desktop response unavailable")
				}
				continue
			}
			writerMu.Lock()
			var result any
			var err error
			switch req.Method {
			case "SetResourceInterest":
				var interest resourceInterest
				err = decodeParams(req.Params, &interest)
				if err == nil && interest.AllMembers && !interest.AllChannels {
					err = errors.New("成员关注需要频道范围")
				}
				if err == nil {
					resourceSync.set(interest)
				}
				result = true
			case "PlayNotification":
				var p struct {
					Kind   string `json:"kind"`
					Volume int    `json:"volume"`
				}
				err = decodeParams(req.Params, &p)
				if err == nil {
					err = sounds.play(p.Kind, p.Volume)
				}
				result = struct{}{}
			case "StopNotifications":
				sounds.stop()
				result = struct{}{}
			default:
				result, err = dispatch(service, req)
			}
			response := envelope{ID: req.ID, Result: result}
			if err != nil {
				response.Result = nil
				response.Error = err.Error()
			}
			writeErr := encoder.Encode(response)
			writerMu.Unlock()
			if writeErr != nil {
				return errors.New("desktop response unavailable")
			}
			if req.Method == "Shutdown" {
				return nil
			}
		}
	}
}

func readErrorMessage(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "详情读取已取消"
	case errors.Is(err, context.DeadlineExceeded):
		return "读取超时，可稍后重试；服务器连接仍保留"
	default:
		return err.Error()
	}
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if json.Unmarshal(raw, target) != nil {
		return errors.New("请求参数无效")
	}
	return nil
}

func dispatch(s *client.Service, req request) (any, error) {
	return dispatchWithContext(context.Background(), s, req)
}

func dispatchWithContext(ctx context.Context, s *client.Service, req request) (any, error) {
	if req.Method == "SetUserPlayback" {
		var p struct {
			SessionID string `json:"sessionID"`
			UserID    string `json:"userID"`
			Instance  string `json:"instance"`
			Volume    *int   `json:"volume"`
			Muted     *bool  `json:"muted"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		if p.Volume == nil || p.Muted == nil {
			return nil, errors.New("音量和静音参数缺失")
		}
		return s.SetUserPlayback(p.SessionID, p.UserID, p.Instance, *p.Volume, *p.Muted)
	}
	var p struct {
		ID             string               `json:"id"`
		Profile        client.ServerProfile `json:"profile"`
		Password       string               `json:"password"`
		Remember       bool                 `json:"remember"`
		SessionID      string               `json:"sessionID"`
		ChannelID      string               `json:"channelID"`
		Text           string               `json:"text"`
		Token          string               `json:"token"`
		Name           string               `json:"name"`
		Description    string               `json:"description"`
		Bitrate        uint32               `json:"bitrate"`
		AllowDuplicate bool                 `json:"allowDuplicate"`
	}
	if req.Method == "ConfigureVoice" || req.Method == "SetVoicePreferences" {
		config := audio.VoiceConfig{Muted: true, Volume: 100, InputGain: 100}
		if err := decodeParams(req.Params, &config); err != nil {
			return nil, err
		}
		if req.Method == "SetVoicePreferences" {
			return s.SetVoicePreferences(config)
		}
		return s.ConfigureVoice(config)
	}
	if req.Method == "ConfigureMicrophoneTest" {
		config := audio.VoiceConfig{Volume: 35, InputGain: 100, ActivationMode: "continuous", VADThresholdDB: -40}
		if err := decodeParams(req.Params, &config); err != nil {
			return nil, err
		}
		if !config.Enabled {
			return s.StopMicrophoneTest()
		}
		return s.StartMicrophoneTest(config)
	}
	if req.Method == "SetPushToTalk" {
		var p struct {
			Pressed bool `json:"pressed"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		return s.SetPushToTalk(p.Pressed)
	}
	if req.Method == "SetInputGain" {
		var p struct {
			InputGain *int `json:"inputGain"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		if p.InputGain == nil {
			return nil, errors.New("missing inputGain")
		}
		return s.SetInputGain(*p.InputGain)
	}
	if req.Method == "SetOutputGain" {
		var p struct {
			SessionID string `json:"sessionID"`
			Volume    *int   `json:"volume"`
		}
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, err
		}
		if p.Volume == nil {
			return nil, errors.New("缺少收听增益")
		}
		return s.SetOutputGain(p.SessionID, *p.Volume)
	}
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	switch req.Method {
	case "GetCapabilities":
		voiceAvailable := nativeCGO && (runtime.GOOS == "darwin" || runtime.GOOS == "windows")
		return map[string]any{"protocolVersion": 1, "platform": runtime.GOOS, "securePasswordStorage": runtime.GOOS == "windows" || (nativeCGO && runtime.GOOS == "darwin"), "voice": voiceAvailable, "webRtcAudio": voiceAvailable && audio.AvailableWebRTC()}, nil
	case "GetWorkspace":
		return s.GetWorkspace()
	case "GetVoiceState":
		return s.GetVoiceState(), nil
	case "GetMicrophoneTestState":
		return s.GetMicrophoneTest(), nil
	case "GetAudioDevices":
		return s.GetAudioDevices()
	case "SaveServer":
		return s.SaveServer(p.Profile)
	case "DeleteServer":
		return s.DeleteServer(p.ID)
	case "GetServerCredentialStatus":
		return s.GetServerCredentialStatus(p.ID)
	case "ConnectSavedServer":
		return s.ConnectSavedServer(p.ID)
	case "ConnectServerWithPassword":
		return s.ConnectServerWithPassword(p.ID, p.Password, p.Remember)
	case "ForgetServerPassword":
		return s.ForgetServerPassword(p.ID)
	case "ClaimServerOwner":
		return s.ClaimServerOwnerContext(ctx, p.SessionID, p.Token)
	case "CreateChannel":
		return struct{}{}, s.ManageChannelContext(ctx, p.SessionID, "create", "", p.Name, p.Description, p.Bitrate)
	case "UpdateChannel":
		return struct{}{}, s.ManageChannelContext(ctx, p.SessionID, "update", p.ChannelID, p.Name, p.Description, p.Bitrate)
	case "DeleteChannel":
		return struct{}{}, s.ManageChannelContext(ctx, p.SessionID, "delete", p.ChannelID, "", "")
	case "DisconnectServer":
		return s.DisconnectServer()
	case "SelectChannel":
		return s.SelectChannel(p.ID)
	case "SendChannelMessage":
		return s.SendChannelMessage(p.SessionID, p.ChannelID, p.Text)
	case "RetryMessage":
		return s.RetryMessage(p.ID, p.AllowDuplicate)
	case "OpenPreview":
		return s.OpenPreview()
	case "LeavePreview":
		return s.LeavePreview()
	case "SendMessage":
		return s.SendMessage(p.Text)
	case "Shutdown":
		return struct{}{}, nil
	default:
		return nil, errors.New("不支持的桌面操作")
	}
}
