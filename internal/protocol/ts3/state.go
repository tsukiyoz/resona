package ts3

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/tsukiyoz/resona/internal/client"
)

// A reducer belongs to one connection and runs in the incoming command order.
type reducer struct {
	state                      client.RemoteState
	channels                   map[string]client.Channel
	users                      map[string]client.User
	initialized                bool
	listed                     bool
	presentation               map[string]channelMetadata
	iconCache                  map[string]string
	serverUID                  string
	voice                      map[string]voiceChannel
	channelDetails             map[string]client.ChannelDetails
	userDetails                map[string]client.UserDetails
	entityVersions             map[string]uint64
	entitySequence             uint64
	serverVoiceEncryptionMode  uint8
	serverVoiceEncryptionKnown bool
}

type voiceChannel struct {
	codec           uint8
	codecKnown      bool
	unencrypted     bool
	encryptionKnown bool
}

type channelMetadata struct {
	name      string
	permanent bool
}

func newReducer(uid string) *reducer {
	return &reducer{state: client.RemoteState{IdentityUID: uid},
		channels: make(map[string]client.Channel), users: make(map[string]client.User), presentation: make(map[string]channelMetadata), iconCache: make(map[string]string), voice: make(map[string]voiceChannel),
		channelDetails: make(map[string]client.ChannelDetails), userDetails: make(map[string]client.UserDetails), entityVersions: make(map[string]uint64)}
}

func (r *reducer) apply(command teamspeak.IncomingCommand) bool {
	p := command.Params
	r.state.Events = nil
	r.state.Messages = nil
	wasReady := r.ready()
	currentChannel := r.users[r.state.SelfID].ChannelID
	previousUser, knownUser := r.users[p["clid"]]
	switch command.Name {
	case "initserver":
		r.initialized = true
		r.serverUID = p["virtualserver_unique_identifier"]
		r.state.ServerName = p["virtualserver_name"]
		r.state.SelfID = p["aclid"]
		if r.state.SelfID == "" {
			r.state.SelfID = p["clid"]
		}
		if v, ok := p["virtualserver_codec_encryption_mode"]; ok {
			mode, err := strconv.ParseUint(v, 10, 8)
			r.serverVoiceEncryptionMode = uint8(mode)
			r.serverVoiceEncryptionKnown = err == nil && mode <= 2
		}
	case "notifyserveredited":
		if v, ok := p["virtualserver_codec_encryption_mode"]; ok {
			mode, err := strconv.ParseUint(v, 10, 8)
			r.serverVoiceEncryptionMode = uint8(mode)
			r.serverVoiceEncryptionKnown = err == nil && mode <= 2
		}
	case "channellist", "notifychannelcreated", "notifychanneledited", "notifychannelmoved":
		id := p["cid"]
		if !validID(id) {
			return false
		}
		ch := r.channels[id]
		if ch.ID == "" {
			r.entitySequence++
			r.entityVersions["channel:"+id] = r.entitySequence
		}
		meta, exists := r.presentation[id]
		if !exists {
			meta.name = ch.Name
		}
		ch.ID = id
		if v, ok := p["channel_name"]; ok {
			meta.name = v
		}
		if v, ok := p["cpid"]; ok {
			ch.ParentID = v
		}
		if v, ok := p["channel_order"]; ok {
			ch.Order = v
		}
		if v, ok := p["order"]; ok {
			ch.Order = v
		}
		if v, ok := p["channel_topic"]; ok {
			ch.Description = v
		}
		if v, ok := p["channel_flag_password"]; ok {
			ch.PasswordRequired = v == "1"
		}
		if v, ok := p["channel_flag_permanent"]; ok {
			meta.permanent = v == "1"
		}
		r.presentation[id] = meta
		presentation := parseChannelPresentation(meta.name, ch.ParentID, meta.permanent)
		ch.Kind, ch.Name, ch.Align, ch.Repeat = presentation.Kind, presentation.DisplayName, presentation.Align, presentation.Repeat
		if v, ok := p["channel_icon_id"]; ok {
			ch.IconID = normalizeIconID(v)
		}
		ch.IconRef = r.iconCache[ch.IconID]
		r.channels[id] = ch
		details := r.channelDetails[id]
		details.ID, details.Name = id, ch.Name
		mergeChannelDetails(&details, p)
		r.channelDetails[id] = details
		voice := r.voice[id]
		if v, ok := p["channel_codec"]; ok {
			codec, err := strconv.ParseUint(v, 10, 8)
			voice.codec, voice.codecKnown = uint8(codec), err == nil
		}
		if v, ok := p["channel_codec_is_unencrypted"]; ok {
			voice.unencrypted, voice.encryptionKnown = v == "1", v == "0" || v == "1"
		}
		r.voice[id] = voice
	case "channellistfinished":
		r.listed = true
	case "notifychanneldeleted":
		delete(r.entityVersions, "channel:"+p["cid"])
		delete(r.channelDetails, p["cid"])
		delete(r.channels, p["cid"])
		delete(r.presentation, p["cid"])
		delete(r.voice, p["cid"])
	case "notifytextmessage":
		return r.applyChannelText(p)
	case "notifycliententerview", "notifyclientupdated", "notifyclientmoved":
		id := p["clid"]
		if !validID(id) {
			return false
		}
		u := r.users[id]
		if u.ID == "" || command.Name == "notifycliententerview" {
			r.entitySequence++
			r.entityVersions["user:"+id] = r.entitySequence
			u.Instance = strconv.FormatUint(r.entitySequence, 10)
		}
		u.ID = id
		if v, ok := p["client_nickname"]; ok {
			u.Nickname = v
		}
		if v, ok := p["ctid"]; ok {
			u.ChannelID = v
		}
		r.users[id] = u
		details := r.userDetails[id]
		details.ID, details.Nickname, details.ChannelID = id, u.Nickname, u.ChannelID
		mergeUserDetails(&details, p)
		r.userDetails[id] = details
		if wasReady && id != r.state.SelfID && command.Name != "notifyclientupdated" && memberChangeReason(p["reasonid"]) {
			if knownUser && previousUser.ChannelID == currentChannel && u.ChannelID != currentChannel {
				r.memberEvent("member_left", id, currentChannel)
			} else if u.ChannelID == currentChannel && (!knownUser || previousUser.ChannelID != currentChannel) {
				r.memberEvent("member_joined", id, currentChannel)
			}
		}
	case "notifyclientleftview":
		delete(r.entityVersions, "user:"+p["clid"])
		delete(r.userDetails, p["clid"])
		delete(r.users, p["clid"])
		if wasReady && p["clid"] != r.state.SelfID && knownUser && previousUser.ChannelID == currentChannel && memberChangeReason(p["reasonid"]) {
			r.memberEvent("member_left", p["clid"], currentChannel)
		}
		if p["clid"] == r.state.SelfID && disconnectReason(p["reasonid"]) {
			r.state.Closed = true
			r.state.Error = "服务器已结束本次连接"
		}
	case "error":
		if id, err := strconv.ParseUint(p["id"], 10, 32); err == nil && id != 0 && !r.initialized {
			r.state.Error = fmt.Sprintf("服务器拒绝连接（错误码 %d）", id)
			r.state.Closed = true
		}
	default:
		return false
	}
	return true
}

func (r *reducer) currentVoiceChannel() (voiceChannel, error) {
	self, ok := r.users[r.state.SelfID]
	if !ok || !validID(self.ChannelID) {
		return voiceChannel{}, errors.New("当前语音频道不可用")
	}
	voice, ok := r.voice[self.ChannelID]
	if !ok || !voice.codecKnown {
		return voiceChannel{}, errors.New("服务器未提供当前频道的语音编码")
	}
	return voice, nil
}

func (r *reducer) voiceEncryption(voice voiceChannel) (bool, error) {
	if r.serverVoiceEncryptionKnown {
		switch r.serverVoiceEncryptionMode {
		case 1:
			voice.unencrypted, voice.encryptionKnown = true, true
		case 2:
			voice.unencrypted, voice.encryptionKnown = false, true
		}
	}
	if !voice.encryptionKnown {
		return false, errors.New("服务器未提供当前频道的语音加密状态")
	}
	return voice.unencrypted, nil
}

// Subscription snapshots are not live joins, even after our own login is ready.
func memberChangeReason(reason string) bool {
	switch reason {
	case "0", "1", "3", "4", "5", "6", "8":
		return true
	default:
		return false
	}
}

func disconnectReason(reason string) bool {
	switch reason {
	case "3", "5", "6", "7", "8", "11":
		return true
	default:
		return false
	}
}

func (r *reducer) memberEvent(kind, userID, channelID string) {
	r.state.Events = append(r.state.Events, client.RemoteEvent{Kind: kind, UserID: userID, ChannelID: channelID})
}

func validID(id string) bool {
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && n != 0
}

func (r *reducer) ready() bool {
	self, exists := r.users[r.state.SelfID]
	return r.initialized && r.listed && exists && validID(self.ChannelID)
}

func (r *reducer) snapshot() client.RemoteState {
	s := r.state
	s.Events = append([]client.RemoteEvent(nil), r.state.Events...)
	s.Messages = append([]client.RemoteMessage(nil), r.state.Messages...)
	s.Channels = make([]client.Channel, 0, len(r.channels))
	s.Users = make([]client.User, 0, len(r.users))
	counts := make(map[string]int)
	for _, user := range r.users {
		user.Self = user.ID == s.SelfID
		if user.Self {
			s.ChannelID = user.ChannelID
		}
		counts[user.ChannelID]++
		s.Users = append(s.Users, user)
	}
	for _, channel := range r.channels {
		channel.Members = counts[channel.ID]
		s.Channels = append(s.Channels, channel)
	}
	sort.Slice(s.Channels, func(i, j int) bool { return idLess(s.Channels[i].ID, s.Channels[j].ID) })
	sort.Slice(s.Users, func(i, j int) bool { return idLess(s.Users[i].ID, s.Users[j].ID) })
	return s
}

func idLess(a, b string) bool {
	x, _ := strconv.ParseUint(a, 10, 64)
	y, _ := strconv.ParseUint(b, 10, 64)
	return x < y
}
