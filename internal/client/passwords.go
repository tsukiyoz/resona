package client

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

type PasswordStore interface {
	Has(string) (bool, error)
	Get(string) (string, error)
	Set(string, string) error
	Delete(string) error
}

type CredentialStatus struct {
	Saved    bool `json:"saved"`
	Remember bool `json:"remember"`
}

// Bind each credential to its bookmark and destination, never just a display name.
func passwordKey(profile ServerProfile) string {
	sum := sha256.Sum256([]byte(profile.ID + "\x00" + profile.Address))
	return hex.EncodeToString(sum[:])
}

func (s *Service) GetServerCredentialStatus(id string) (CredentialStatus, error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.mu.Lock()
	profile, ok := s.profileLocked(id)
	s.mu.Unlock()
	if !ok {
		return CredentialStatus{}, errors.New("服务器书签不存在")
	}
	status := CredentialStatus{Remember: !profile.SkipPasswordStorage}
	if s.passwords == nil {
		return status, nil
	}
	saved, err := s.passwords.Has(passwordKey(profile))
	if err != nil {
		return status, errors.New("无法读取已保存密码状态，可取消记住密码后连接")
	}
	status.Saved = saved
	return status, nil
}

func (s *Service) ConnectSavedServer(id string) (Workspace, error) {
	return s.connectWithCredentials(id, "", true, true)
}

func (s *Service) ConnectServerWithPassword(id, password string, remember bool) (Workspace, error) {
	return s.connectWithCredentials(id, password, remember, false)
}

func (s *Service) connectWithCredentials(id, password string, remember, saved bool) (Workspace, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	if s.shutdown || s.connector == nil {
		s.mu.Unlock()
		return Workspace{}, errors.New("当前客户端无法连接服务器")
	}
	if s.state.Session.ServerID == id && (s.state.Session.Mode == "connected" || s.state.Session.Mode == "connecting") {
		state := s.snapshot()
		s.mu.Unlock()
		return state, nil
	}
	target, exists := s.profileLocked(id)
	s.mu.Unlock()
	if !exists {
		return Workspace{}, errors.New("服务器书签不存在")
	}
	preparedPassword, err := s.prepareCredentials(id, target.Address, password, remember, saved)
	if err != nil {
		return Workspace{}, err
	}
	// Resolve the new destination's credentials before ending the current session.
	if _, err := s.disconnectRemote(); err != nil {
		s.mu.Lock()
		preview := s.state.Session.Mode == "preview"
		s.mu.Unlock()
		if !preview {
			return Workspace{}, err
		}
		if _, err := s.LeavePreview(); err != nil {
			return Workspace{}, err
		}
	}
	s.cleanupWG.Wait()
	return s.connectServerLocked(id, preparedPassword, remember && !saved, target.Address)
}

func (s *Service) prepareCredentials(id, expectedAddress, password string, remember, saved bool) (string, error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.mu.Lock()
	profile, ok := s.profileLocked(id)
	s.mu.Unlock()
	if !ok {
		return "", errors.New("服务器书签不存在")
	}
	if profile.Address != expectedAddress {
		return "", errors.New("服务器地址已更改，请重新连接")
	}
	if saved {
		if s.passwords == nil {
			return "", errors.New("没有已保存密码，请重新输入")
		}
		value, err := s.passwords.Get(passwordKey(profile))
		if err != nil {
			return "", errors.New("无法读取已保存密码，请重新输入")
		}
		return value, nil
	}
	if remember && s.passwords == nil {
		return "", errors.New("密码存储不可用，请取消记住密码后连接")
	}
	if !remember && s.passwords != nil {
		if err := s.passwords.Delete(passwordKey(profile)); err != nil {
			return "", errors.New("无法清除已保存密码，请解锁系统钥匙串后重试")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles := append([]ServerProfile{}, s.state.Servers...)
	for i := range profiles {
		if profiles[i].ID == id {
			profiles[i].SkipPasswordStorage = !remember
		}
	}
	if err := s.store.Save(profiles); err != nil {
		return "", errors.New("无法保存密码偏好，连接尚未切换")
	}
	s.state.Servers = profiles
	return password, nil
}

func (s *Service) rememberSuccessfulPassword(profile ServerProfile, password string, generation uint64) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.mu.Lock()
	current, exists := s.profileLocked(profile.ID)
	valid := exists && current.Address == profile.Address && !current.SkipPasswordStorage && generation == s.generation && !s.shutdown
	s.mu.Unlock()
	if !valid {
		return
	}
	err := s.passwords.Set(passwordKey(profile), password)
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation == s.generation && err != nil {
		s.state.Session.CredentialError = "已连接，但密码未保存，请检查系统钥匙串"
	}
}

func (s *Service) ForgetServerPassword(id string) (Workspace, error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profileLocked(id)
	if !ok {
		return Workspace{}, errors.New("服务器书签不存在")
	}
	if s.passwords != nil {
		if err := s.passwords.Delete(passwordKey(profile)); err != nil {
			return Workspace{}, errors.New("无法清除已保存密码")
		}
	}
	profiles := append([]ServerProfile{}, s.state.Servers...)
	for i := range profiles {
		if profiles[i].ID == id {
			profiles[i].SkipPasswordStorage = true
		}
	}
	if err := s.store.Save(profiles); err != nil {
		return Workspace{}, errors.New("密码已清除，但无法保存密码偏好")
	}
	s.state.Servers = profiles
	return s.snapshot(), nil
}
