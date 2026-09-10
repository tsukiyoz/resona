// Package credentials keeps remembered server passwords in native secure storage.
package credentials

import "errors"

var (
	ErrNotFound    = errors.New("没有已保存的服务器密码")
	ErrUnavailable = errors.New("当前系统不支持安全保存服务器密码")
	errInvalidKey  = errors.New("服务器密码存储标识无效")
	errRead        = errors.New("无法从系统凭据存储读取服务器密码")
	errWrite       = errors.New("无法在系统凭据存储中保存服务器密码")
	errDelete      = errors.New("无法从系统凭据存储删除服务器密码")
)
