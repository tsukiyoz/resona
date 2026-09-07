package teamspeak

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/honeybbq/teamspeak-go/commands"
)

func TestFileTransferInitDownloadContextCancelsAllWaits(t *testing.T) {
	for _, stage := range []string{"before-send", "throttle", "ack", "notification"} {
		t.Run(stage, func(t *testing.T) {
			c := newTestClient(t)
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			if stage == "before-send" {
				cancel()
			}
			if stage == "throttle" {
				c.throttle.tokens = 0
			}
			c.finalCmdHandler = func(raw string) error {
				if stage == "before-send" || stage == "throttle" {
					t.Error("canceled transfer command sent")
				}
				if stage == "notification" {
					cmd := commands.ParseCommand(raw)
					c.handleCommand("error id=0 msg=ok return_code=" + cmd.Params["return_code"])
				}
				return nil
			}
			_, err := c.FileTransferInitDownloadContext(ctx, 0, "/icon_1234", "")
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			assertCommandTrackerEmpty(t, c.cmdTrack)
			c.ftTrack.mu.Lock()
			defer c.ftTrack.mu.Unlock()
			if len(c.ftTrack.pending) != 0 {
				t.Fatal("file transfer registration leaked")
			}
		})
	}
}

func TestFileTransferInitDownloadContextEarlyDuplicateNotification(t *testing.T) {
	c := newTestClient(t)
	c.finalCmdHandler = func(raw string) error {
		cmd := commands.ParseCommand(raw)
		if cmd.Name != "ftinitdownload" || cmd.Params["cid"] != "0" || cmd.Params["name"] != "/icon_1234" {
			t.Fatalf("unexpected transfer command: %s", raw)
		}
		id, _ := strconv.ParseUint(cmd.Params["clientftfid"], 10, 16)
		c.ftTrack.notify(uint16(id), FileDownloadInfo{Size: 42, Port: 30033, FileTransferKey: "key"})
		c.ftTrack.notify(uint16(id), FileDownloadInfo{Size: 500})
		c.handleCommand("error id=0 msg=ok return_code=" + cmd.Params["return_code"])
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := c.FileTransferInitDownloadContext(ctx, 0, "icon_1234", "")
	if err != nil || got == nil || got.Size != 42 {
		t.Fatalf("download initialization: %#v %v", got, err)
	}
}

func TestFileTransferTracker_Register_ReturnsUniqueIDs(t *testing.T) {
	tr := newFileTransferTracker()

	id1, _ := tr.register()
	id2, _ := tr.register()

	if id1 == 0 {
		t.Error("expected non-zero ID")
	}
	if id2 <= id1 {
		t.Errorf("expected id2 > id1, got id1=%d id2=%d", id1, id2)
	}
}

func TestFileTransferTracker_Notify_DeliversValue(t *testing.T) {
	tr := newFileTransferTracker()
	id, ch := tr.register()

	go func() {
		time.Sleep(10 * time.Millisecond)
		tr.notify(id, FileUploadInfo{Port: 30033, FileTransferKey: "abc"})
	}()

	select {
	case val := <-ch:
		info, ok := val.(FileUploadInfo)
		if !ok {
			t.Fatalf("expected FileUploadInfo, got %T", val)
		}
		if info.Port != 30033 || info.FileTransferKey != "abc" {
			t.Errorf("unexpected info: %+v", info)
		}
	case <-time.After(time.Second):
		t.Error("notify did not deliver value")
	}
}

func TestFileTransferTracker_Notify_UnregisteredID_NoOp(t *testing.T) {
	tr := newFileTransferTracker()
	// Notifying a non-existent ID should not block or panic.
	tr.notify(999, FileUploadInfo{})
}

func TestFileTransferTracker_Unregister_PreventsDelivery(t *testing.T) {
	tr := newFileTransferTracker()
	id, ch := tr.register()
	tr.unregister(id)

	tr.notify(id, FileUploadInfo{Port: 1})

	select {
	case <-ch:
		t.Error("unregistered channel should not receive")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFileTransferTracker_Reset_ClearsPending(t *testing.T) {
	tr := newFileTransferTracker()
	_, _ = tr.register()
	_, _ = tr.register()

	tr.reset()

	// After reset, notify is a no-op.
	tr.notify(1, FileUploadInfo{})
	tr.notify(2, FileUploadInfo{})
}

func TestFileTransferTracker_DownloadInfo_Delivered(t *testing.T) {
	tr := newFileTransferTracker()
	id, ch := tr.register()

	info := FileDownloadInfo{Port: 30034, FileTransferKey: "xyz", Size: 1024}
	go func() { tr.notify(id, info) }()

	select {
	case val := <-ch:
		got, ok := val.(FileDownloadInfo)
		if !ok {
			t.Fatalf("expected FileDownloadInfo, got %T", val)
		}
		if got.Size != 1024 {
			t.Errorf("expected size 1024, got %d", got.Size)
		}
	case <-time.After(time.Second):
		t.Error("notify did not deliver download info")
	}
}

func TestFileTransferTracker_StatusInfo_Delivered(t *testing.T) {
	tr := newFileTransferTracker()
	id, ch := tr.register()

	status := FileTransferStatusInfo{Status: 2, Message: "error"}
	go func() { tr.notify(id, status) }()

	select {
	case val := <-ch:
		got, ok := val.(FileTransferStatusInfo)
		if !ok {
			t.Fatalf("expected FileTransferStatusInfo, got %T", val)
		}
		if got.Message != "error" {
			t.Errorf("expected 'error', got %q", got.Message)
		}
	case <-time.After(time.Second):
		t.Error("notify did not deliver status")
	}
}

// FileTransferDeleteFile — pure command building (no network)

func TestFileTransferDeleteFile_EmptyPaths_ReturnsNil(t *testing.T) {
	c := newTestClient(t)
	err := c.FileTransferDeleteFile(1, nil)
	if err != nil {
		t.Errorf("expected nil error for empty paths, got %v", err)
	}
}
