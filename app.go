package main

import (
	"sync"

	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/config"
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
	return client.New(store)
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
