package ts3

import (
	"fmt"
	"sort"
	"strconv"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"github.com/tsukiyoz/resona/internal/client"
)

// A reducer belongs to one connection and runs in the incoming command order.
type reducer struct {
	state       client.RemoteState
	channels    map[string]client.Channel
	users       map[string]client.User
	initialized bool
	listed      bool
}

func newReducer(uid string) *reducer {
	return &reducer{state: client.RemoteState{IdentityUID: uid},
		channels: make(map[string]client.Channel), users: make(map[string]client.User)}
}

func (r *reducer) apply(command teamspeak.IncomingCommand) bool {
	p := command.Params
	switch command.Name {
	case "initserver":
		r.initialized = true
		r.state.ServerName = p["virtualserver_name"]
		r.state.SelfID = p["aclid"]
		if r.state.SelfID == "" {
			r.state.SelfID = p["clid"]
		}
	case "channellist", "notifychannelcreated", "notifychanneledited", "notifychannelmoved":
		id := p["cid"]
		if !validID(id) {
			return false
		}
		ch := r.channels[id]
		ch.ID = id
		if v, ok := p["channel_name"]; ok {
			ch.Name = v
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
		r.channels[id] = ch
	case "channellistfinished":
		r.listed = true
	case "notifychanneldeleted":
		delete(r.channels, p["cid"])
	case "notifycliententerview", "notifyclientupdated", "notifyclientmoved":
		id := p["clid"]
		if !validID(id) {
			return false
		}
		u := r.users[id]
		u.ID = id
		if v, ok := p["client_nickname"]; ok {
			u.Nickname = v
		}
		if v, ok := p["ctid"]; ok {
			u.ChannelID = v
		}
		r.users[id] = u
	case "notifyclientleftview":
		delete(r.users, p["clid"])
		if p["clid"] == r.state.SelfID && p["reasonid"] == "5" {
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
