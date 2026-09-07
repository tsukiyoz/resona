package ts3

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	teamspeak "github.com/honeybbq/teamspeak-go"
)

func testIcon(t *testing.T, format string, size int) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, size, size))
	im.Set(0, 0, color.NRGBA{R: 180, G: 70, B: 210, A: 255})
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, im)
	case "gif":
		err = gif.Encode(&buf, im, nil)
	case "jpeg":
		err = jpeg.Encode(&buf, im, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRasterIconsAreNormalizedToBoundedPNG(t *testing.T) {
	for _, format := range []string{"png", "gif", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			dataURL, err := rasterIconDataURL(testIcon(t, format, 16))
			if err != nil {
				t.Fatal(err)
			}
			encoded := strings.TrimPrefix(dataURL, "data:image/png;base64,")
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			config, err := png.DecodeConfig(bytes.NewReader(data))
			if err != nil || config.Width != 16 || config.Height != 16 {
				t.Fatalf("invalid normalized image: %+v, %v", config, err)
			}
		})
	}
	for _, data := range [][]byte{nil, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), make([]byte, maxIconBytes+1), testIcon(t, "png", 1025)} {
		if _, err := rasterIconDataURL(data); err == nil {
			t.Fatal("unsafe or oversized icon accepted")
		}
	}
}

func TestRasterIconThumbnailsPreserveAspectRatioAndDoNotUpscale(t *testing.T) {
	for _, size := range []struct{ width, height, wantWidth, wantHeight int }{
		{512, 512, 256, 256}, {1024, 1024, 256, 256},
		{1024, 512, 256, 128}, {512, 1024, 128, 256},
		{300, 150, 256, 128}, {257, 1, 256, 1},
		{16, 16, 16, 16}, {64, 32, 64, 32}, {256, 256, 256, 256},
	} {
		t.Run(strconv.Itoa(size.width)+"x"+strconv.Itoa(size.height), func(t *testing.T) {
			source := image.NewNRGBA(image.Rect(0, 0, size.width, size.height))
			for y := range size.height {
				for x := range size.width {
					source.SetNRGBA(x, y, color.NRGBA{R: 180, G: 70, B: 210, A: 200})
				}
			}
			var input bytes.Buffer
			if err := png.Encode(&input, source); err != nil {
				t.Fatal(err)
			}
			dataURL, err := rasterIconDataURL(input.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, "data:image/png;base64,"))
			if err != nil || len(data) > maxIconBytes {
				t.Fatalf("invalid output bytes: %d %v", len(data), err)
			}
			decoded, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if bounds := decoded.Bounds(); bounds.Dx() != size.wantWidth || bounds.Dy() != size.wantHeight {
				t.Fatalf("thumbnail bounds = %v", bounds)
			}
			_, _, _, alpha := decoded.At(0, 0).RGBA()
			if alpha == 0 || alpha == 65535 {
				t.Fatal("thumbnail lost source transparency")
			}
		})
	}
}

func TestRasterIconSizeErrorIncludesDimensions(t *testing.T) {
	_, err := rasterIconDataURL(testIcon(t, "png", 1025))
	if err == nil || !strings.Contains(err.Error(), "1025x1025") {
		t.Fatalf("missing rejected dimensions: %v", err)
	}
}

func TestDownloadIconValidatesMetadataBeforeDial(t *testing.T) {
	for _, info := range []*teamspeak.FileDownloadInfo{
		nil, {}, {Size: maxIconBytes + 1, Port: 30033, FileTransferKey: "key"},
		{Size: 64, Port: 30033, FileTransferKey: strings.Repeat("k", 129)},
	} {
		_, err := downloadChannelIcon(context.Background(), "127.0.0.1", "1234", func(ctx context.Context, cid uint64, path, password string) (*teamspeak.FileDownloadInfo, error) {
			if cid != 0 || path != "/icon_1234" || password != "" {
				t.Fatalf("invalid transfer target: %d %s", cid, path)
			}
			return info, nil
		})
		if err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
	for _, host := range []string{"example.com", "http://127.0.0.1", "127.0.0.1:30033"} {
		_, err := downloadChannelIcon(context.Background(), host, "1234", func(context.Context, uint64, string, string) (*teamspeak.FileDownloadInfo, error) {
			t.Fatal("non-numeric host reached transfer initialization")
			return nil, nil
		})
		if err == nil {
			t.Fatal("non-numeric host accepted")
		}
	}
}

func TestDownloadIconReadsExactAdvertisedBytesAndCancelsStall(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(strconv.FormatBool(stalled), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			data := testIcon(t, "png", 16)
			accepted := make(chan struct{})
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				key := make([]byte, 3)
				if _, err := io.ReadFull(conn, key); err != nil || string(key) != "key" {
					return
				}
				close(accepted)
				if !stalled {
					_, _ = conn.Write(data)
				}
				_, _ = io.Copy(io.Discard, conn)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if stalled {
				go func() { <-accepted; cancel() }()
			}
			result, err := downloadChannelIcon(ctx, "127.0.0.1", "1234", func(context.Context, uint64, string, string) (*teamspeak.FileDownloadInfo, error) {
				return &teamspeak.FileDownloadInfo{Size: uint64(len(data)), FileTransferKey: "key", Port: uint16(listener.Addr().(*net.TCPAddr).Port)}, nil
			})
			if stalled && err == nil || !stalled && (err != nil || result == "") {
				t.Fatalf("stalled=%v result=%q err=%v", stalled, result, err)
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("TCP connection leaked")
			}
		})
	}
}

func TestIconLoaderDeduplicatesAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	done := make(chan string, 2)
	l := newIconLoader(ctx, func(ctx context.Context, id string) (string, error) {
		calls.Add(1)
		if id == "1234" {
			return "image", nil
		}
		return "", errors.New("missing icon")
	}, func(id, data string) { done <- id + data })
	for range 10 {
		l.Request("1234")
		l.Request("100")
	}
	select {
	case got := <-done:
		if got != "1234image" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("icon was not published")
	}
	cancel()
	l.Close()
	l.Request("2345")
	if calls.Load() != 1 {
		t.Fatalf("fetches = %d", calls.Load())
	}
}

func TestIconLoaderBoundsRequestsAndCancelsActiveFetch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var calls atomic.Int32
	l := newIconLoader(ctx, func(ctx context.Context, id string) (string, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}, func(string, string) { t.Error("canceled download was published") })
	l.Request("1000")
	<-started
	for n := 1000; n < 1000+maxSessionIcons*2; n++ {
		l.Request(strconv.Itoa(n))
	}
	l.mu.Lock()
	requested := len(l.requested)
	l.mu.Unlock()
	if requested != maxSessionIcons {
		t.Fatalf("queued requests = %d", requested)
	}
	cancel()
	l.Close()
	if calls.Load() != 1 {
		t.Fatalf("queued requests started after cancellation: %d", calls.Load())
	}
}

func TestIconLoaderDoesNotRetryMissingIcons(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	fetched := make(chan struct{})
	l := newIconLoader(ctx, func(context.Context, string) (string, error) {
		calls.Add(1)
		close(fetched)
		return "", errors.New("missing icon")
	}, func(string, string) { t.Error("missing icon was published") })
	l.Request("1000")
	<-fetched
	for range 10 {
		l.Request("1000")
	}
	cancel()
	l.Close()
	if calls.Load() != 1 {
		t.Fatalf("missing icon fetches = %d", calls.Load())
	}
}
