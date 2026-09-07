package teamspeak

import (
	"log/slog"
	"strings"

	"github.com/honeybbq/teamspeak-go/commands"
)

func (c *Client) handleNotification(cmd *commands.Command) {
	switch cmd.Name {
	case "notifycliententerview":
		c.handleClientEnterView(cmd)
	case "notifyclientleftview":
		c.handleClientLeftView(cmd)
	case "notifyclientmoved":
		c.handleClientMoved(cmd)
	case "notifytextmessage":
		c.handleTextMessage(cmd)
	case "notifyclientpoke":
		c.handleClientPoke(cmd)
	case "notifyclientneededpermissions":
		c.logger.Debug("insufficient permissions",
			slog.String("permid", cmd.Params["permid"]),
			slog.String("permvalue", cmd.Params["permvalue"]))
	case "notifystartupload":
		c.handleStartUpload(cmd)
	case "notifystartdownload":
		c.handleStartDownload(cmd)
	case "notifystatusfiletransfer":
		c.handleFileTransferStatus(cmd)
	default:
		c.logger.Debug("unhandled notification", slog.String("name", cmd.Name))
	}
}

func (c *Client) handleClientEnterView(cmd *commands.Command) {
	nick := cmd.Params["client_nickname"]
	uid := cmd.Params["client_unique_identifier"]
	groupsStr := cmd.Params["client_servergroups"]

	clid, _ := parseUint16Value(cmd.Params["clid"])
	ctid, _ := parseUint64Value(cmd.Params["ctid"])
	clientType, _ := parseIntValue(cmd.Params["client_type"])

	groups := make([]string, 0)
	if groupsStr != "" {
		groups = strings.Split(groupsStr, ",")
	}

	if clid != 0 {
		info := ClientInfo{
			ID:           clid,
			Nickname:     nick,
			UID:          uid,
			ChannelID:    ctid,
			Type:         clientType,
			ServerGroups: groups,
		}

		c.mu.Lock()
		c.clients[clid] = info
		unescapedNick := commands.Unescape(nick)
		// initserver's authenticated client ID takes precedence over nickname guesses.
		if c.clid == 0 && isAutoNicknameMatch(c.nickname, unescapedNick) {
			c.clid = clid
			c.handler.SetClientID(clid)
		}
		c.mu.Unlock()

		c.finalEvtHandler(info)
	}
}

func (c *Client) handleClientLeftView(cmd *commands.Command) {
	reasonMsg := cmd.Params["reasonmsg"]

	clid, _ := parseUint16Value(cmd.Params["clid"])
	reasonID, _ := parseIntValue(cmd.Params["reasonid"]) // 4=channel kick, 5=server kick

	if clid != 0 {
		c.mu.Lock()
		isSelf := (clid == c.clid)
		delete(c.clients, clid)
		kickHandlers := c.kickedHandlers
		c.mu.Unlock()

		evt := ClientLeftViewEvent{
			ID:        clid,
			ReasonID:  reasonID,
			ReasonMsg: reasonMsg,
		}

		c.finalEvtHandler(evt)

		if isSelf && (reasonID == 4 || reasonID == 5) {
			for _, h := range kickHandlers {
				go h(reasonMsg)
			}
		}
	}
}

func (c *Client) handleClientMoved(cmd *commands.Command) {
	clid, _ := parseUint16Value(cmd.Params["clid"])
	ctid, _ := parseUint64Value(cmd.Params["ctid"])
	reasonID, _ := parseIntValue(cmd.Params["reasonid"])
	invokerID, _ := parseUint16Value(cmd.Params["invokerid"])

	if clid != 0 {
		c.mu.Lock()
		if info, ok := c.clients[clid]; ok {
			info.ChannelID = ctid
			c.clients[clid] = info
		}
		c.mu.Unlock()

		evt := ClientMovedEvent{
			ID:              clid,
			TargetChannelID: ctid,
			ReasonID:        reasonID,
			InvokerID:       invokerID,
			InvokerName:     cmd.Params["invokername"],
			InvokerUID:      cmd.Params["invokeruid"],
		}

		c.finalEvtHandler(evt)
	}
}

func (c *Client) handleTextMessage(cmd *commands.Command) {
	targetMode, _ := parseIntValue(cmd.Params["targetmode"])
	targetID, _ := parseUint64Value(cmd.Params["target"])
	invokerID, _ := parseUint16Value(cmd.Params["invokerid"])
	msg := TextMessage{
		TargetMode:  targetMode,
		TargetID:    targetID,
		InvokerID:   invokerID,
		InvokerName: cmd.Params["invokername"],
		InvokerUID:  cmd.Params["invokeruid"],
		Message:     cmd.Params["msg"],
	}

	c.mu.Lock()
	if info, ok := c.clients[invokerID]; ok {
		if msg.InvokerUID == "" {
			msg.InvokerUID = info.UID
		}
		msg.InvokerGroups = info.ServerGroups
	}
	c.mu.Unlock()

	c.logger.Debug("text message received",
		slog.Int("target_mode", targetMode),
		slog.String("invoker_id", cmd.Params["invokerid"]),
		slog.String("invoker_name", cmd.Params["invokername"]),
		slog.String("invoker_uid", msg.InvokerUID),
		slog.String("message", cmd.Params["msg"]))

	c.finalEvtHandler(msg)
}

func (c *Client) handleClientPoke(cmd *commands.Command) {
	invokerID, _ := parseUint16Value(cmd.Params["invokerid"])
	evt := PokeEvent{
		InvokerID:   invokerID,
		InvokerName: commands.Unescape(cmd.Params["invokername"]),
		InvokerUID:  cmd.Params["invokeruid"],
		Message:     commands.Unescape(cmd.Params["msg"]),
	}

	c.logger.Debug("client poked",
		slog.String("invoker_name", evt.InvokerName),
		slog.String("invoker_uid", evt.InvokerUID),
		slog.String("message", evt.Message))

	c.finalEvtHandler(evt)
}

func (c *Client) handleStartUpload(cmd *commands.Command) {
	clientftfid, _ := parseUint16Value(cmd.Params["clientftfid"])
	serverftfid, _ := parseUint16Value(cmd.Params["serverftfid"])
	port, _ := parseUint16Value(cmd.Params["port"])
	seekpos, _ := parseUint64Value(cmd.Params["seekpos"])
	info := FileUploadInfo{
		ClientFileTransferID: clientftfid,
		ServerFileTransferID: serverftfid,
		FileTransferKey:      cmd.Params["ftkey"],
		Port:                 port,
		SeekPosition:         seekpos,
	}
	c.ftTrack.notify(info.ClientFileTransferID, info)
}

func (c *Client) handleStartDownload(cmd *commands.Command) {
	clientftfid, _ := parseUint16Value(cmd.Params["clientftfid"])
	serverftfid, _ := parseUint16Value(cmd.Params["serverftfid"])
	port, _ := parseUint16Value(cmd.Params["port"])
	size, _ := parseUint64Value(cmd.Params["size"])
	info := FileDownloadInfo{
		ClientFileTransferID: clientftfid,
		ServerFileTransferID: serverftfid,
		FileTransferKey:      cmd.Params["ftkey"],
		Port:                 port,
		Size:                 size,
	}
	c.ftTrack.notify(info.ClientFileTransferID, info)
}

func (c *Client) handleFileTransferStatus(cmd *commands.Command) {
	clientftfid, _ := parseUint16Value(cmd.Params["clientftfid"])
	status, _ := parseIntValue(cmd.Params["status"])
	info := FileTransferStatusInfo{
		ClientFileTransferID: clientftfid,
		Status:               status,
		Message:              cmd.Params["msg"],
	}
	c.ftTrack.notify(info.ClientFileTransferID, info)
}
