package teamspeak

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/honeybbq/teamspeak-go/commands"
)

var (
	errFileTransferFailed   = errors.New("file transfer failed")
	errUnexpectedRespType   = errors.New("unexpected response type")
	errFileTransferTimedOut = errors.New("timeout waiting for file transfer notification")
)

// fileTransferTracker correlates clientftfid with notifystart* responses.
type fileTransferTracker struct {
	pending map[uint16]chan any
	mu      sync.Mutex
	nextID  uint16
}

func newFileTransferTracker() *fileTransferTracker {
	return &fileTransferTracker{
		pending: make(map[uint16]chan any),
	}
}

func (t *fileTransferTracker) register() (uint16, <-chan any) {
	t.mu.Lock()
	t.nextID++
	if t.nextID == 0 {
		t.nextID++
	}
	cftid := t.nextID
	ch := make(chan any, 1)
	t.pending[cftid] = ch
	t.mu.Unlock()

	return cftid, ch
}

func (t *fileTransferTracker) unregister(cftid uint16) {
	t.mu.Lock()
	delete(t.pending, cftid)
	t.mu.Unlock()
}

func (t *fileTransferTracker) notify(cftid uint16, v any) {
	t.mu.Lock()
	if ch, ok := t.pending[cftid]; ok {
		ch <- v
	}
	t.mu.Unlock()
}

func (t *fileTransferTracker) reset() {
	t.mu.Lock()
	t.pending = make(map[uint16]chan any)
	t.mu.Unlock()
}

// FileTransferInitUpload sends ftinitupload to the server and waits for the
// notifystartupload response containing the TCP port and transfer key.
func (c *Client) FileTransferInitUpload(
	channelID uint64, path string, password string, size uint64, overwrite bool,
) (*FileUploadInfo, error) {
	cftid, ch := c.ftTrack.register()
	defer c.ftTrack.unregister(cftid)

	targetPath := path
	if !strings.HasPrefix(targetPath, "/") {
		targetPath = "/" + targetPath
	}

	overwriteVal := "0"
	if overwrite {
		overwriteVal = "1"
	}

	cmd := commands.BuildCommand("ftinitupload", map[string]string{
		"cid":         strconv.FormatUint(channelID, 10),
		"name":        targetPath,
		"cpw":         password,
		"size":        strconv.FormatUint(size, 10),
		"clientftfid": strconv.Itoa(int(cftid)),
		"overwrite":   overwriteVal,
		"resume":      "0",
	})

	err := c.ExecCommand(cmd, 10*time.Second)
	if err != nil {
		return nil, err
	}

	select {
	case res := <-ch:
		switch v := res.(type) {
		case FileUploadInfo:
			return &v, nil
		case FileTransferStatusInfo:
			return nil, fmt.Errorf("%w: %s (status=%d)", errFileTransferFailed, v.Message, v.Status)
		default:
			return nil, fmt.Errorf("%w: %T", errUnexpectedRespType, v)
		}
	case <-time.After(10 * time.Second):
		return nil, errFileTransferTimedOut
	}
}

// FileTransferInitDownload sends ftinitdownload to the server and waits for the
// notifystartdownload response containing the TCP port and transfer key.
func (c *Client) FileTransferInitDownload(channelID uint64, path string, password string) (*FileDownloadInfo, error) {
	cftid, ch := c.ftTrack.register()
	defer c.ftTrack.unregister(cftid)

	targetPath := path
	if !strings.HasPrefix(targetPath, "/") {
		targetPath = "/" + targetPath
	}

	cmd := commands.BuildCommand("ftinitdownload", map[string]string{
		"cid":         strconv.FormatUint(channelID, 10),
		"name":        targetPath,
		"cpw":         password,
		"clientftfid": strconv.Itoa(int(cftid)),
		"seekpos":     "0",
	})

	err := c.ExecCommand(cmd, 10*time.Second)
	if err != nil {
		return nil, err
	}

	select {
	case res := <-ch:
		switch v := res.(type) {
		case FileDownloadInfo:
			return &v, nil
		case FileTransferStatusInfo:
			return nil, fmt.Errorf("%w: %s (status=%d)", errFileTransferFailed, v.Message, v.Status)
		default:
			return nil, fmt.Errorf("%w: %T", errUnexpectedRespType, v)
		}
	case <-time.After(10 * time.Second):
		return nil, errFileTransferTimedOut
	}
}

// FileTransferDeleteFile sends ftdeletefile to delete files on the server.
func (c *Client) FileTransferDeleteFile(channelID uint64, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	pathStr := strings.Join(paths, "|")
	cmd := commands.BuildCommand("ftdeletefile", map[string]string{
		"cid":  strconv.FormatUint(channelID, 10),
		"cpw":  "",
		"name": pathStr,
	})

	return c.ExecCommand(cmd, 10*time.Second)
}

// DialFileTransfer opens TCP to the TeamSpeak file-transfer port
// and performs the ftkey handshake. The caller is responsible for closing the
// returned connection.
func DialFileTransfer(host string, port uint16, key string) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to file transfer server %s: %w", addr, err)
	}

	_, err = conn.Write([]byte(key))
	if err != nil {
		_ = conn.Close()

		return nil, fmt.Errorf("failed to send transfer key: %w", err)
	}

	return conn, nil
}

// UploadFileData transfers data to the server using credentials from FileTransferInitUpload.
func UploadFileData(host string, info *FileUploadInfo, data io.Reader) error {
	conn, err := DialFileTransfer(host, info.Port, info.FileTransferKey)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	_, err = io.Copy(conn, data)
	if err != nil {
		return fmt.Errorf("failed to upload file data: %w", err)
	}

	return nil
}

// DownloadFileData receives data from the server using credentials from FileTransferInitDownload.
func DownloadFileData(host string, info *FileDownloadInfo, dest io.Writer) error {
	conn, err := DialFileTransfer(host, info.Port, info.FileTransferKey)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	_, err = io.Copy(dest, conn)
	if err != nil {
		return fmt.Errorf("failed to download file data: %w", err)
	}

	return nil
}
