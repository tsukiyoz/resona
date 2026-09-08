package main

import (
	"context"
	"sync"

	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/config"
	"github.com/tsukiyoz/resona/internal/credentials"
	"github.com/tsukiyoz/resona/internal/iconcache"
	"github.com/tsukiyoz/resona/internal/protocol/ts3"
)

type App struct {
	mu         sync.Mutex
	service    *client.Service
	newService func() (*client.Service, error)
}

func NewApp() *App {
	return &App{newService: loadService}
}

func loadService() (*client.Service, error) {
	store, err := config.NewDefault()
	if err != nil {
		return nil, err
	}
	connector, err := ts3.NewDefault(iconcache.NewDefault())
	if err != nil {
		return nil, err
	}
	return client.NewWithPasswordStore(store, connector, credentials.New())
}

func (a *App) GetServerCredentialStatus(id string) (client.CredentialStatus, error) {
	service, err := a.backend()
	if err != nil {
		return client.CredentialStatus{}, err
	}
	return service.GetServerCredentialStatus(id)
}

func (a *App) GetIconResource(sessionID, ref string) (client.IconResource, error) {
	service, err := a.backend()
	if err != nil {
		return client.IconResource{}, err
	}
	return service.GetIconResource(context.Background(), sessionID, ref)
}

func (a *App) ConnectSavedServer(id string) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.ConnectSavedServer(id)
}

func (a *App) ConnectServerWithPassword(id, password string, remember bool) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.ConnectServerWithPassword(id, password, remember)
}

func (a *App) ForgetServerPassword(id string) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.ForgetServerPassword(id)
}

func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	service := a.service
	a.mu.Unlock()
	if service != nil {
		service.Shutdown()
	}
}

func (a *App) ConnectServer(id, password string) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.ConnectServer(id, password)
}

func (a *App) DisconnectServer() (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.DisconnectServer()
}

func (a *App) backend() (*client.Service, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.service == nil {
		service, err := a.newService()
		if err != nil {
			return nil, err
		}
		a.service = service
	}
	return a.service, nil
}

func (a *App) GetWorkspace() (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.GetWorkspace()
}

func (a *App) SaveServer(profile client.ServerProfile) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.SaveServer(profile)
}

func (a *App) DeleteServer(id string) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.DeleteServer(id)
}

func (a *App) OpenPreview() (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.OpenPreview()
}

func (a *App) LeavePreview() (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.LeavePreview()
}

func (a *App) SelectChannel(id string) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.SelectChannel(id)
}

func (a *App) SendMessage(text string) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.SendMessage(text)
}

func (a *App) SendChannelMessage(sessionID, channelID, text string) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.SendChannelMessage(sessionID, channelID, text)
}

func (a *App) RetryMessage(id string, allowDuplicate bool) (client.Workspace, error) {
	service, err := a.backend()
	if err != nil {
		return client.Workspace{}, err
	}
	return service.RetryMessage(id, allowDuplicate)
}
