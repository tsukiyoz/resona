package transport

import "math"

type GenerationWindow struct {
	mappedBaseOffset int
	generation       uint32
	mod              int
	receiveWindow    int
}

func NewGenerationWindow(mod int, windowSize int) *GenerationWindow {
	return &GenerationWindow{
		mod:           mod,
		receiveWindow: windowSize,
	}
}

func (g *GenerationWindow) Advance(amount int) {
	if amount <= 0 {
		return
	}
	newBaseOffset := g.mappedBaseOffset + amount
	genStep := newBaseOffset / g.mod
	if genStep > 0 {
		if genStep > math.MaxUint32 {
			genStep = math.MaxUint32
		}
		g.generation += uint32(genStep)
	}
	g.mappedBaseOffset = newBaseOffset % g.mod
}

func (g *GenerationWindow) AdvanceToExcluded(mappedValue int) {
	moveDist := mappedValue - g.mappedBaseOffset
	if moveDist < 0 {
		moveDist += g.mod
	}
	g.Advance(moveDist + 1)
}

// SyncTo advances the window baseline toward mappedValue (handles wrap and resync).
func (g *GenerationWindow) SyncTo(mappedValue int) {
	moveDist := mappedValue - g.mappedBaseOffset
	if moveDist < 0 {
		moveDist += g.mod
	}
	g.Advance(moveDist)
}

func (g *GenerationWindow) IsInWindow(mappedValue int) bool {
	maxOffset := g.mappedBaseOffset + g.receiveWindow
	if maxOffset < g.mod {
		return mappedValue >= g.mappedBaseOffset && mappedValue < maxOffset
	}

	return mappedValue >= g.mappedBaseOffset || mappedValue < maxOffset-g.mod
}

// MappedToIndex returns the offset from the window base; negative means stale, >= receiveWindow too far ahead.
func (g *GenerationWindow) MappedToIndex(mappedValue int) int {
	if g.IsNextGen(mappedValue) {
		return (mappedValue + g.mod) - g.mappedBaseOffset
	}

	return mappedValue - g.mappedBaseOffset
}

// IsOldPacket reports whether mappedValue is before the receive window.
func (g *GenerationWindow) IsOldPacket(mappedValue int) bool {
	index := g.MappedToIndex(mappedValue)

	return index < 0
}

// IsFuturePacket reports whether mappedValue lies beyond the window.
func (g *GenerationWindow) IsFuturePacket(mappedValue int) bool {
	index := g.MappedToIndex(mappedValue)

	return index >= g.receiveWindow
}

func (g *GenerationWindow) IsNextGen(mappedValue int) bool {
	return g.mappedBaseOffset > (g.mod-g.receiveWindow) &&
		mappedValue < (g.mappedBaseOffset+g.receiveWindow)-g.mod
}

func (g *GenerationWindow) GetGeneration(mappedValue int) uint32 {
	if g.IsNextGen(mappedValue) {
		return g.generation + 1
	}

	return g.generation
}

func (g *GenerationWindow) Reset() {
	g.mappedBaseOffset = 0
	g.generation = 0
}
