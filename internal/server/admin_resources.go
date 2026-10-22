package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	w "github.com/tsukiyoz/resona/internal/nativewire"
)

type AdminMember struct {
	ID       uint16 `json:"id"`
	Instance string `json:"instance"`
	Identity string `json:"identity"`
	Nickname string `json:"nickname"`
	Channel  uint16 `json:"channel"`
	Role     string `json:"role"`
	Muted    bool   `json:"muted"`
}

type AdminSnapshot struct {
	Revision       uint64            `json:"revision"`
	Identity       string            `json:"identity"`
	Role           string            `json:"role"`
	Capabilities   map[string]bool   `json:"capabilities"`
	Name           string            `json:"name"`
	MaxClients     int               `json:"max_clients"`
	DefaultChannel uint16            `json:"default_channel"`
	Channels       []w.Channel       `json:"channels"`
	Members        []AdminMember     `json:"members"`
	Owner          string            `json:"owner"`
	Admins         []string          `json:"admins"`
	Credentials    []AdminCredential `json:"credentials,omitempty"`
	Bans           []Ban             `json:"bans"`
}

func (s *Server) adminSnapshot(identity string) AdminSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, revision := s.config.Store.state()
	state := AdminSnapshot{Revision: revision, Identity: identity, Role: roleOf(r, identity), Name: s.config.Name, MaxClients: s.config.MaxClients, DefaultChannel: r.Channels.Default, Channels: r.Channels.Channels, Members: []AdminMember{}, Owner: r.Owner, Admins: r.Admins, Capabilities: make(map[string]bool)}
	state.Bans = r.Bans
	for _, object := range []string{"channels", "members", "roles", "settings", "tokens"} {
		state.Capabilities[object] = authorized(r, identity, object, "write", "member")
	}
	for _, p := range s.peers {
		if p.removed {
			continue
		}
		state.Members = append(state.Members, AdminMember{ID: p.member.ID, Instance: p.member.Instance, Identity: p.identity, Nickname: p.member.Nickname, Channel: p.member.Channel, Role: roleOf(r, p.identity), Muted: p.member.Muted})
	}
	slices.SortFunc(state.Members, func(a, b AdminMember) int { return int(a.ID) - int(b.ID) })
	if state.Capabilities["tokens"] {
		for _, c := range r.Credentials {
			c.Digest = ""
			state.Credentials = append(state.Credentials, c)
		}
	}
	return state
}

func (a *AdminConsole) registerResources() {
	a.mux.HandleFunc("GET "+adminBase+"/state", a.state)
	a.mux.HandleFunc("GET "+adminBase+"/events", a.events)
	a.mux.HandleFunc("POST "+adminBase+"/channels", a.channel)
	a.mux.HandleFunc("PATCH "+adminBase+"/channels/{id}", a.channel)
	a.mux.HandleFunc("DELETE "+adminBase+"/channels/{id}", a.channel)
	a.mux.HandleFunc("POST "+adminBase+"/channels/{id}/order", a.channel)
}

func (a *AdminConsole) state(wr http.ResponseWriter, r *http.Request) {
	if session, ok := a.authenticate(wr, r); ok {
		adminJSON(wr, 200, a.server.adminSnapshot(session.Identity))
	}
}

// Register before reading the snapshot; coalescing wakeups always fetch current
// state. Slow browsers cannot accumulate history or block voice delivery.
func (a *AdminConsole) events(wr http.ResponseWriter, r *http.Request) {
	session, ok := a.authenticate(wr, r)
	if !ok {
		return
	}
	s := a.server
	wake := make(chan struct{}, 1)
	s.mu.Lock()
	if len(s.adminObservers) >= 64 {
		s.mu.Unlock()
		adminError(wr, 503, "资源订阅已满")
		return
	}
	if s.adminObservers == nil {
		s.adminObservers = make(map[chan struct{}]struct{})
	}
	s.adminObservers[wake] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.adminObservers, wake); s.mu.Unlock() }()
	wr.Header().Set("Content-Type", "text/event-stream")
	wr.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(wr)
	write := func(event string, value any) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		data, err := json.Marshal(value)
		if err != nil {
			return false
		}
		if _, err = fmt.Fprintf(wr, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	if !write("state", s.adminSnapshot(session.Identity)) {
		return
	}
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	expires := time.NewTimer(time.Until(session.Expires))
	defer expires.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-session.Done:
			write("expired", map[string]string{"error": "已退出登录"})
			return
		case <-expires.C:
			write("expired", map[string]string{"error": "登录已过期"})
			return
		case <-wake:
			if !s.config.Store.adminSessionValid(session.Identity, session.Credential) {
				write("expired", map[string]string{"error": "权限或凭据已撤销"})
				return
			}
			if !write("state", s.adminSnapshot(session.Identity)) {
				return
			}
		case <-heartbeat.C:
			if !s.config.Store.adminSessionValid(session.Identity, session.Credential) {
				write("expired", map[string]string{"error": "权限或凭据已撤销"})
				return
			}
			if !write("heartbeat", struct{}{}) {
				return
			}
		}
	}
}

func adminFailure(wr http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrPermission):
		adminError(wr, 403, "权限不足或已撤销")
	case errors.Is(err, ErrConflict):
		adminError(wr, 409, "配置已被其他操作修改，请刷新后重新核对")
	case errors.Is(err, ErrInvalid):
		adminError(wr, 400, "配置无效或已达到数量限制")
	case errors.Is(err, ErrNotFound):
		adminError(wr, 404, "对象已离线或不再存在")
	default:
		adminError(wr, 503, "操作结果尚未确认，请刷新状态后核对")
	}
}

func (a *AdminConsole) channel(wr http.ResponseWriter, r *http.Request) {
	session, ok := a.authenticate(wr, r)
	if !ok {
		return
	}
	var v struct {
		Revision    uint64 `json:"revision"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Bitrate     uint32 `json:"bitrate"`
		BeforeID    uint16 `json:"before_id"`
	}
	if !adminDecode(wr, r, &v) {
		return
	}
	if v.Revision == 0 {
		adminFailure(wr, ErrInvalid)
		return
	}
	var id uint64
	var err error
	kind := uint8(w.CreateChannelKind)
	if path := r.PathValue("id"); path != "" {
		id, err = strconv.ParseUint(path, 10, 16)
		if err != nil || id == 0 {
			adminFailure(wr, ErrInvalid)
			return
		}
		kind = w.UpdateChannelKind
		if r.Method == "DELETE" {
			kind = w.DeleteChannelKind
		}
		if r.Method == "POST" {
			kind = w.ReorderChannelKind
		}
	}
	code, err := a.server.mutateChannelAt(r.Context(), session.Identity, v.Revision, kind, uint16(id), v.BeforeID, v.Name, v.Description, v.Bitrate)
	if code == w.OK {
		adminJSON(wr, 200, map[string]bool{"ok": true})
		return
	}
	if errors.Is(err, ErrConflict) {
		adminFailure(wr, err)
		return
	}
	switch code {
	case w.PermissionDenied:
		adminFailure(wr, ErrPermission)
	case w.DefaultChannel:
		adminError(wr, 409, "默认频道不能删除")
	case w.ChannelNotEmpty:
		adminError(wr, 409, "频道内仍有成员，不能删除")
	case w.WrongChannel:
		adminFailure(wr, ErrNotFound)
	case w.ChannelLimit, w.Rejected:
		adminFailure(wr, ErrInvalid)
	default:
		adminFailure(wr, err)
	}
}
