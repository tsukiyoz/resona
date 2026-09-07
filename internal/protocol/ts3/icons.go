package ts3

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
	"golang.org/x/image/draw"
)

const (
	maxIconBytes           = 256 << 10
	maxIconDimension       = 1024
	maxIconPixels          = maxIconDimension * maxIconDimension
	iconThumbnailDimension = 256
	maxSessionIcons        = 128
	iconTimeout            = 10 * time.Second
)

type iconDownloadInit func(context.Context, uint64, string, string) (*teamspeak.FileDownloadInfo, error)

func downloadChannelIcon(ctx context.Context, connectedHost, id string, init iconDownloadInit) (string, error) {
	if !customIconID(id) || net.ParseIP(connectedHost) == nil {
		return "", errors.New("invalid icon request")
	}
	ctx, cancel := context.WithTimeout(ctx, iconTimeout)
	defer cancel()
	info, err := init(ctx, 0, "/icon_"+id, "")
	if err != nil {
		return "", err
	}
	if info == nil || info.Port == 0 || info.Size == 0 || info.Size > maxIconBytes || len(info.FileTransferKey) == 0 || len(info.FileTransferKey) > 128 {
		return "", errors.New("invalid icon transfer metadata")
	}
	// Never follow an IP supplied by a file-transfer notification or resolve DNS again.
	address := net.JoinHostPort(connectedHost, strconv.Itoa(int(info.Port)))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return "", err
	}
	if _, err := io.WriteString(conn, info.FileTransferKey); err != nil {
		return "", err
	}
	data := make([]byte, int(info.Size))
	if _, err := io.ReadFull(conn, data); err != nil {
		return "", err
	}
	return rasterIconDataURL(data)
}

func rasterIconDataURL(data []byte) (string, error) {
	if len(data) == 0 || len(data) > maxIconBytes {
		return "", errors.New("icon exceeds encoded size limit")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "gif") {
		return "", errors.New("unsupported icon image")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxIconDimension || config.Height > maxIconDimension || config.Width*config.Height > maxIconPixels {
		return "", fmt.Errorf("icon dimensions %dx%d exceed pixel limit %dx%d", config.Width, config.Height, maxIconDimension, maxIconDimension)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("cannot decode icon image")
	}
	if longest := max(config.Width, config.Height); longest > iconThumbnailDimension {
		width := max(1, config.Width*iconThumbnailDimension/longest)
		height := max(1, config.Height*iconThumbnailDimension/longest)
		thumbnail := image.NewNRGBA(image.Rect(0, 0, width, height))
		draw.CatmullRom.Scale(thumbnail, thumbnail.Bounds(), decoded, decoded.Bounds(), draw.Src, nil)
		decoded = thumbnail
	}
	var out bytes.Buffer
	if err := png.Encode(&out, decoded); err != nil {
		return "", errors.New("cannot encode icon image")
	}
	if out.Len() > maxIconBytes {
		return "", errors.New("icon exceeds encoded size limit")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(out.Bytes()), nil
}

// A single session worker bounds concurrency and prevents repeated missing-icon requests.
type iconLoader struct {
	ctx       context.Context
	mu        sync.Mutex
	requested map[string]struct{}
	queue     chan string
	done      chan struct{}
}

func newIconLoader(ctx context.Context, fetch func(context.Context, string) (string, error), publish func(string, string)) *iconLoader {
	l := &iconLoader{ctx: ctx, requested: make(map[string]struct{}), queue: make(chan string, maxSessionIcons), done: make(chan struct{})}
	go func() {
		defer close(l.done)
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-l.queue:
				if ctx.Err() != nil {
					return
				}
				data, err := fetch(ctx, id)
				if err == nil && ctx.Err() == nil {
					publish(id, data)
				}
			}
		}
	}()
	return l
}

func (l *iconLoader) Request(id string) {
	if !customIconID(id) || l.ctx.Err() != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.requested[id]; ok || len(l.requested) >= maxSessionIcons {
		return
	}
	l.requested[id] = struct{}{}
	l.queue <- id
}

// Close waits after the owner has canceled the connection lifetime.
func (l *iconLoader) Close() { <-l.done }
