package ts3

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/honeybbq/teamspeak-go/commands"
	"github.com/tsukiyoz/resona/internal/client"
)

type detailCacheEntry struct {
	version uint64
	expires time.Time
	row     map[string]string
	err     error
}

func (s *connection) ReadChannelDetails(ctx context.Context, id string) (client.ChannelDetails, error) {
	row, version, err := s.readDetails(ctx, "channel", id)
	if err != nil {
		return client.ChannelDetails{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.detailTargetValid("channel", id, version) || ctx.Err() != nil {
		return client.ChannelDetails{}, context.Canceled
	}
	details := s.state.channelDetails[id]
	channel := s.state.channels[id]
	details.ID, details.Name = id, channel.Name
	mergeChannelDetails(&details, row)
	details.Members = 0
	for _, user := range s.state.users {
		if user.ChannelID == id {
			details.Members++
		}
	}
	details.MemberSyncState = s.state.state.MemberSyncState
	return details, nil
}

func (s *connection) ReadUserDetails(ctx context.Context, id string) (client.UserDetails, error) {
	row, version, err := s.readDetails(ctx, "user", id)
	if err != nil {
		return client.UserDetails{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.detailTargetValid("user", id, version) || ctx.Err() != nil {
		return client.UserDetails{}, context.Canceled
	}
	details := s.state.userDetails[id]
	mergeUserDetails(&details, row)
	user := s.state.users[id]
	details.ID, details.Nickname, details.ChannelID = id, user.Nickname, user.ChannelID
	return details, nil
}

func (s *connection) detailTargetValid(kind, id string, version uint64) bool {
	if s.closing || s.state.state.Closed || s.state.entityVersions[kind+":"+id] != version {
		return false
	}
	if kind == "channel" {
		_, ok := s.state.channels[id]
		return ok
	}
	_, ok := s.state.users[id]
	return ok
}

func (s *connection) readDetails(ctx context.Context, kind, id string) (map[string]string, uint64, error) {
	if !validID(id) {
		return nil, 0, errors.New("详情目标无效")
	}
	s.mu.Lock()
	version := s.state.entityVersions[kind+":"+id]
	valid := s.detailTargetValid(kind, id, version)
	s.mu.Unlock()
	if !valid {
		return nil, 0, errors.New("详情目标已离开或不可见")
	}
	if s.execCommandResponse == nil {
		return nil, 0, errors.New("当前连接不支持详情查询")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	select {
	case s.commandGate <- struct{}{}:
		defer func() { <-s.commandGate }()
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	s.mu.Lock()
	valid = s.detailTargetValid(kind, id, version)
	s.mu.Unlock()
	if !valid || ctx.Err() != nil {
		return nil, 0, context.Canceled
	}
	cacheKey := kind + ":" + id
	s.mu.Lock()
	cached, found := s.detailCache[cacheKey]
	s.mu.Unlock()
	if found && cached.version == version && time.Now().Before(cached.expires) {
		return cached.row, version, cached.err
	}
	command, key := "channelinfo", "cid"
	if kind == "user" {
		command, key = "clientinfo", "clid"
	}
	rows, err := s.execCommandResponse(ctx, commands.BuildCommand(command, map[string]string{key: id}))
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		var commandError *teamspeak.CommandError
		if errors.As(err, &commandError) {
			err = fmt.Errorf("服务器拒绝读取详情（错误码 %d）", commandError.ID)
			s.cacheDetails(cacheKey, version, nil, err, 2*time.Second)
			return nil, 0, err
		}
		return nil, 0, errors.New("无法读取服务器详情")
	}
	if len(rows) != 1 || (rows[0][key] != "" && rows[0][key] != id) {
		return nil, 0, errors.New("服务器未返回有效详情")
	}
	bytes := 0
	for key, value := range rows[0] {
		bytes += len(key) + len(value)
	}
	if bytes > 64<<10 {
		return nil, 0, errors.New("服务器详情超过大小限制")
	}
	s.cacheDetails(cacheKey, version, rows[0], nil, 10*time.Second)
	return rows[0], version, nil
}

func (s *connection) cacheDetails(key string, version uint64, row map[string]string, err error, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.detailCache == nil || len(s.detailCache) >= 64 {
		s.detailCache = make(map[string]detailCacheEntry)
	}
	s.detailCache[key] = detailCacheEntry{version: version, expires: time.Now().Add(ttl), row: row, err: err}
}

func mergeChannelDetails(d *client.ChannelDetails, p map[string]string) {
	readString(p, "channel_topic", &d.Topic)
	readString(p, "channel_description", &d.Description)
	readInt(p, "channel_codec_quality", &d.CodecQuality, 0, 10)
	if value, ok := p["channel_codec"]; ok {
		labels := map[string]string{"0": "Speex narrowband", "1": "Speex wideband", "2": "Speex ultra-wideband", "3": "CELT mono", "4": "Opus Voice", "5": "Opus Music"}
		if name, ok := labels[value]; ok {
			d.Codec = &name
		} else {
			d.Codec = nil
		}
	}
	readBool(p, "channel_flag_permanent", &d.Permanent)
	readBool(p, "channel_flag_semi_permanent", &d.SemiPermanent)
	readBool(p, "channel_flag_default", &d.Default)
	readBool(p, "channel_flag_password", &d.PasswordRequired)
	readInt(p, "channel_maxclients", &d.MaxClients, -1, 1<<31-1)
	readBool(p, "channel_flag_maxclients_unlimited", &d.MaxClientsUnlimited)
	readInt(p, "channel_maxfamilyclients", &d.MaxFamilyClients, -1, 1<<31-1)
	readBool(p, "channel_flag_maxfamilyclients_unlimited", &d.MaxFamilyClientsUnlimited)
	readBool(p, "channel_flag_maxfamilyclients_inherited", &d.MaxFamilyClientsInherited)
}

func mergeUserDetails(d *client.UserDetails, p map[string]string) {
	readString(p, "client_description", &d.Description)
	readString(p, "client_unique_identifier", &d.IdentityUID)
	readString(p, "client_away_message", &d.AwayMessage)
	readBool(p, "client_away", &d.Away)
	readBool(p, "client_input_muted", &d.InputMuted)
	readBool(p, "client_output_muted", &d.OutputMuted)
}

func readString(p map[string]string, key string, target **string) {
	if value, ok := p[key]; ok {
		*target = &value
	}
}

func readBool(p map[string]string, key string, target **bool) {
	if value, ok := p[key]; ok {
		*target = nil
		if value == "0" || value == "1" {
			parsed := value == "1"
			*target = &parsed
		}
	}
}

func readInt(p map[string]string, key string, target **int, min, max int) {
	if value, ok := p[key]; ok {
		*target = nil
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= min && parsed <= max {
			*target = &parsed
		}
	}
}
