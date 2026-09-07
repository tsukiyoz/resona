package teamspeak

// IncomingCommand contains an already-unescaped command and independently owned parameters.
type IncomingCommand struct {
	Name   string
	Params map[string]string
}

// CommandObserver runs synchronously in receive order, before normal dispatch.
// Observers must return promptly and must not wait for network commands.
type CommandObserver func(IncomingCommand)

// TextMessage is an incoming notifytextmessage payload.
type TextMessage struct {
	InvokerName   string
	InvokerUID    string
	Message       string
	InvokerGroups []string
	TargetMode    int
	TargetID      uint64
	InvokerID     uint16
}

// ClientMovedEvent is emitted when a client changes channel (notifyclientmoved).
type ClientMovedEvent struct {
	InvokerName     string
	InvokerUID      string
	TargetChannelID uint64
	ReasonID        int
	ID              uint16
	InvokerID       uint16
}

// PokeEvent is emitted when this client is poked (notifyclientpoke).
type PokeEvent struct {
	InvokerName string
	InvokerUID  string
	Message     string
	InvokerID   uint16
}

// ClientLeftViewEvent is emitted when a client leaves view (notifyclientleftview).
type ClientLeftViewEvent struct {
	ReasonMsg string
	ReasonID  int
	ID        uint16
	TargetID  uint16
}

// ClientInfo holds fields from clientlist / notifycliententerview.
type ClientInfo struct {
	Nickname     string
	UID          string
	ServerGroups []string
	ChannelID    uint64
	Type         int
	ID           uint16
}

// ChannelInfo is one row from channellist.
type ChannelInfo struct {
	Name        string
	Description string
	ID          uint64
	ParentID    uint64
}

// FileUploadInfo represents the information received when an upload is initialized.
type FileUploadInfo struct {
	FileTransferKey      string
	SeekPosition         uint64
	ClientFileTransferID uint16
	ServerFileTransferID uint16
	Port                 uint16
}

// FileDownloadInfo represents the information received when a download is initialized.
type FileDownloadInfo struct {
	FileTransferKey      string
	Size                 uint64
	ClientFileTransferID uint16
	ServerFileTransferID uint16
	Port                 uint16
}

// FileTransferStatusInfo represents status notifications for file transfers.
type FileTransferStatusInfo struct {
	Message              string
	Status               int
	ClientFileTransferID uint16
}
