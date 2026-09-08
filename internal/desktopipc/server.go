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
	workers.Add(3)
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
		for {
			select {
			case <-ctx.Done():
				return
			case <-changes:
			}
			writerMu.Lock()
			workspace, err := service.GetWorkspace()
			voice := service.GetVoiceState()
			if err == nil && (previous == nil || !reflect.DeepEqual(*previous, workspace)) {
				err = encoder.Encode(envelope{Event: "workspace", Result: workspace})
				previous = &workspace
			}
			if err == nil && (previousVoice == nil || !reflect.DeepEqual(*previousVoice, voice)) {
				err = encoder.Encode(envelope{Event: "voice", Result: voice})
				previousVoice = &voice
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
			writerMu.Lock()
			var result any
			var err error
			switch req.Method {
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
	var p struct {
		ID             string               `json:"id"`
		Profile        client.ServerProfile `json:"profile"`
		Password       string               `json:"password"`
		Remember       bool                 `json:"remember"`
		SessionID      string               `json:"sessionID"`
		ChannelID      string               `json:"channelID"`
		Text           string               `json:"text"`
		AllowDuplicate bool                 `json:"allowDuplicate"`
	}
	if req.Method == "ConfigureVoice" {
		config := audio.VoiceConfig{Muted: true, Volume: 100}
		if err := decodeParams(req.Params, &config); err != nil {
			return nil, err
		}
		return s.ConfigureVoice(config)
	}
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	switch req.Method {
	case "GetCapabilities":
		return map[string]any{"protocolVersion": 1, "platform": runtime.GOOS, "securePasswordStorage": nativeCGO && runtime.GOOS == "darwin", "voice": nativeCGO && (runtime.GOOS == "darwin" || runtime.GOOS == "windows")}, nil
	case "GetWorkspace":
		return s.GetWorkspace()
	case "GetVoiceState":
		return s.GetVoiceState(), nil
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
