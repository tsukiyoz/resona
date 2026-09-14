package native

import (
	"context"
	"errors"
	"strconv"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

func (c *connection) CreateChannel(ctx context.Context, name, description string) error {
	return channelResult(c.command(ctx, w.CreateChannelKind, w.CreateChannel{Name: name, Description: description}))
}
func (c *connection) UpdateChannel(ctx context.Context, id, name, description string) error {
	n, err := strconv.ParseUint(id, 10, 16)
	if err != nil || n == 0 {
		return errors.New("无效频道")
	}
	return channelResult(c.command(ctx, w.UpdateChannelKind, w.UpdateChannel{ID: uint16(n), Name: name, Description: description}))
}
func (c *connection) DeleteChannel(ctx context.Context, id string) error {
	n, err := strconv.ParseUint(id, 10, 16)
	if err != nil || n == 0 {
		return errors.New("无效频道")
	}
	return channelResult(c.command(ctx, w.DeleteChannelKind, w.DeleteChannel{ID: uint16(n)}))
}

func (c *connection) ManageChannelAudio(ctx context.Context, action, id, name, description string, bitrate uint32) error {
	c.mu.Lock()
	allowed := c.state.CanConfigureChannelAudio
	c.mu.Unlock()
	if !allowed || bitrate == 0 || !w.ValidChannelBitrate(bitrate) {
		return errors.New("服务器不支持该音质设置")
	}
	switch action {
	case "create":
		return channelResult(c.command(ctx, w.CreateChannelKind, w.CreateChannel{Name: name, Description: description, Bitrate: bitrate}))
	case "update":
		n, err := strconv.ParseUint(id, 10, 16)
		if err != nil || n == 0 {
			return errors.New("无效频道")
		}
		return channelResult(c.command(ctx, w.UpdateChannelKind, w.UpdateChannel{ID: uint16(n), Name: name, Description: description, Bitrate: bitrate}))
	default:
		return errors.New("未知频道操作")
	}
}
func channelResult(err error) error {
	if err == nil {
		return nil
	}
	var rejected *channelRejection
	if errors.As(err, &rejected) {
		return err
	}
	return errors.New("频道操作结果未确认，请重新连接并检查频道列表，勿直接重复创建")
}

type channelRejection struct{ message string }

func (e *channelRejection) Error() string { return e.message }

func channelReply(code uint8) error {
	if code == w.OK {
		return nil
	}
	message := "服务器拒绝频道操作，请检查名称、描述及容量限制"
	switch code {
	case w.WrongChannel:
		message = "频道已不存在或正在删除"
	case w.RateLimited:
		message = "操作过于频繁，请稍后再试"
	case w.PermissionDenied:
		message = "没有频道管理权限"
	case w.ChannelNotEmpty:
		message = "频道内仍有成员，不能删除"
	case w.DefaultChannel:
		message = "默认频道不能删除"
	case w.StorageFailed:
		message = "服务器保存失败，结果未确认，请检查频道列表后再操作"
	case w.ChannelLimit:
		message = "频道数量或 ID 已达上限"
	}
	return &channelRejection{message: message}
}
