package teamspeak

import (
	"testing"
	"time"
)

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
