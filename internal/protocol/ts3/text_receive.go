package ts3

import (
	"strings"
	"unicode/utf8"

	"github.com/tsukiyoz/resona/internal/client"
)

func (r *reducer) applyChannelText(params map[string]string) bool {
	r.state.Messages = nil
	if params["targetmode"] != "2" || !r.ready() || r.state.Closed {
		return false
	}
	channelID := r.users[r.state.SelfID].ChannelID
	target := params["target"]
	if target != "" && target != "0" && target != channelID {
		return false
	}
	userID := params["invokerid"]
	// The send operation owns the local row and its acknowledgment status.
	// Ignoring authenticated self echoes avoids duplicating that row.
	if !validID(userID) || userID == r.state.SelfID {
		return false
	}
	text := params["msg"]
	if text == "" || len(text) > client.MaxChannelMessageBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return false
	}
	author := params["invokername"]
	if author == "" {
		author = r.users[userID].Nickname
	}
	if author == "" {
		author = "用户 " + userID
	}
	r.state.Messages = []client.RemoteMessage{{
		ChannelID: channelID, UserID: userID, Author: author, Text: text,
	}}
	return true
}
