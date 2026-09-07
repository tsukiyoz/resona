package transport_test

import (
	"testing"

	"github.com/honeybbq/teamspeak-go/transport"
)

func TestGenerationWindowNew(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	if gw == nil {
		t.Fatal("expected non-nil GenerationWindow")
	}
	if gw.GetGeneration(0) != 0 {
		t.Error("initial generation should be 0")
	}
	if !gw.IsInWindow(0) {
		t.Error("0 should be in window after creation")
	}
}

func TestGenerationWindowReset(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	gw.Advance(500)
	gw.Reset()
	if gw.GetGeneration(0) != 0 {
		t.Error("generation should be 0 after Reset")
	}
	// After Reset, base=0 so window is [0..1023]
	if !gw.IsInWindow(0) {
		t.Error("0 should be in window after Reset")
	}
	if !gw.IsInWindow(1023) {
		t.Error("1023 should be in window after Reset")
	}
	if gw.IsInWindow(1024) {
		t.Error("1024 should NOT be in window after Reset")
	}
}

func TestGenerationWindowAdvanceBasic(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	gw.Advance(100)
	if !gw.IsInWindow(100) {
		t.Error("100 should be in window after Advance(100)")
	}
	if gw.IsInWindow(99) {
		t.Error("99 should not be in window after Advance(100)")
	}
	if !gw.IsInWindow(1123) {
		t.Error("1123 (100+1023) should be in window")
	}
	if gw.IsInWindow(1124) {
		t.Error("1124 should not be in window")
	}
}

func TestGenerationWindowAdvanceZeroAndNegative(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	gw.Advance(0)
	gw.Advance(-1)
	if !gw.IsInWindow(0) {
		t.Error("0 should still be in window after zero/negative advance")
	}
	if gw.GetGeneration(0) != 0 {
		t.Error("generation should remain 0")
	}
}

func TestGenerationWindowFullCycleIncrementsGeneration(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	gw.Advance(65536)
	if gw.GetGeneration(0) != 1 {
		t.Errorf("after one full cycle, generation should be 1, got %d", gw.GetGeneration(0))
	}
}

func TestGenerationWindowIsOldPacket(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	gw.Advance(100)
	if !gw.IsOldPacket(0) {
		t.Error("0 should be old after Advance(100)")
	}
	if !gw.IsOldPacket(99) {
		t.Error("99 should be old after Advance(100)")
	}
	if gw.IsOldPacket(100) {
		t.Error("100 should not be old (it's the window start)")
	}
	if gw.IsOldPacket(500) {
		t.Error("500 should not be old (it's in window)")
	}
}

func TestGenerationWindowIsFuturePacket(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	if !gw.IsFuturePacket(1024) {
		t.Error("1024 should be a future packet (base=0, window=1024)")
	}
	if gw.IsFuturePacket(1023) {
		t.Error("1023 should not be a future packet (last in window)")
	}
	if gw.IsFuturePacket(500) {
		t.Error("500 should not be a future packet")
	}
}

func TestGenerationWindowAdvanceToExcluded(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	// AdvanceToExcluded(10): moveDist=10, Advance(11), base becomes 11
	gw.AdvanceToExcluded(10)
	if !gw.IsOldPacket(10) {
		t.Error("10 should be old after AdvanceToExcluded(10)")
	}
	if gw.IsOldPacket(11) {
		t.Error("11 should be in window (new base)")
	}
}

func TestGenerationWindowSyncTo(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	gw.SyncTo(500)
	if gw.IsOldPacket(500) {
		t.Error("500 should not be old after SyncTo(500)")
	}
	if !gw.IsOldPacket(499) {
		t.Error("499 should be old after SyncTo(500)")
	}
}

func TestGenerationWindowWrapAroundInWindow(t *testing.T) {
	// mod=16, window=4: after Advance(14), window spans 14,15,0,1
	gw := transport.NewGenerationWindow(16, 4)
	gw.Advance(14)
	if !gw.IsInWindow(14) {
		t.Error("14 should be in window")
	}
	if !gw.IsInWindow(15) {
		t.Error("15 should be in window")
	}
	if !gw.IsInWindow(0) {
		t.Error("0 (wrapped) should be in window")
	}
	if !gw.IsInWindow(1) {
		t.Error("1 (wrapped) should be in window")
	}
	if gw.IsInWindow(2) {
		t.Error("2 should NOT be in window")
	}
	if gw.IsInWindow(13) {
		t.Error("13 should NOT be in window")
	}
}

func TestGenerationWindowIsNextGen(t *testing.T) {
	// mod=16, window=4: IsNextGen requires base > 16-4=12
	gw := transport.NewGenerationWindow(16, 4)
	gw.Advance(13) // base=13
	// 0 < (13+4)-16=1 and base=13>12 → IsNextGen(0)=true
	if !gw.IsNextGen(0) {
		t.Error("0 should be next-gen when base=13, mod=16, window=4")
	}
	// 1 is NOT < 1 → IsNextGen(1)=false
	if gw.IsNextGen(1) {
		t.Error("1 should NOT be next-gen when base=13")
	}
}

func TestGenerationWindowNextGenIncreasesGeneration(t *testing.T) {
	gw := transport.NewGenerationWindow(16, 4)
	gw.Advance(13)
	if gw.GetGeneration(13) != 0 {
		t.Errorf("generation of 13 should be 0, got %d", gw.GetGeneration(13))
	}
	if gw.GetGeneration(0) != 1 {
		t.Errorf("generation of 0 (next-gen) should be 1, got %d", gw.GetGeneration(0))
	}
}

func TestGenerationWindowMultipleAdvanceCycles(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	gw.Advance(65536)
	gw.Advance(65536)
	gw.Advance(65536)
	if gw.GetGeneration(0) != 3 {
		t.Errorf("after 3 full cycles, generation should be 3, got %d", gw.GetGeneration(0))
	}
}

func TestGenerationWindowMappedToIndex(t *testing.T) {
	gw := transport.NewGenerationWindow(65536, 1024)
	gw.Advance(100) // base=100
	idx := gw.MappedToIndex(150)
	if idx != 50 {
		t.Errorf("MappedToIndex(150)=%d, want 50 (base=100)", idx)
	}
	idx = gw.MappedToIndex(99)
	if idx != -1 {
		t.Errorf("MappedToIndex(99)=%d, want -1 (old packet)", idx)
	}
}
