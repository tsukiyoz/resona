package transport

import (
	"encoding/binary"
	"errors"
)

var (
	errQlzDataTooShort          = errors.New("data too short")
	errQlzUnsupportedLevel      = errors.New("only QuickLZ level 1 is supported")
	errQlzDataTooShortForHeader = errors.New("data too short for header")
)

// TableSize is the QuickLZ level-1 hash table size.
const TableSize = 4096

type Qlz struct {
	hashtable [TableSize]int
}

type qlzState struct {
	control    uint32
	sourcePos  int
	destPos    int
	nextHashed int
}

func NewQlz() *Qlz {
	return &Qlz{}
}

func getDecompressedSize(data []byte) int {
	if (data[0] & 0x02) != 0 {
		return int(binary.LittleEndian.Uint32(data[5:9]))
	}

	return int(data[2])
}

func (q *Qlz) Decompress(data []byte) ([]byte, error) {
	headerLen, decompressedSize, flags, err := parseQlzHeader(data)
	if err != nil {
		return nil, err
	}
	dest := make([]byte, decompressedSize)

	if (flags & 0x01) == 0 {
		copy(dest, data[headerLen:headerLen+decompressedSize])

		return dest, nil
	}

	for i := range q.hashtable {
		q.hashtable[i] = 0
	}

	state := qlzState{
		control:   1,
		sourcePos: headerLen,
	}

	for q.ensureControl(data, &state) {
		if (state.control & 1) != 0 {
			if !q.processReference(data, dest, &state) {
				break
			}
		} else {
			if q.processLiteral(data, dest, decompressedSize, &state) {
				break
			}
		}
	}

	return dest, nil
}

func parseQlzHeader(data []byte) (int, int, byte, error) {
	if len(data) < 3 {
		return 0, 0, 0, errQlzDataTooShort
	}
	flags := data[0]
	level := (flags >> 2) & 0x03
	if level != 1 {
		return 0, 0, 0, errQlzUnsupportedLevel
	}
	headerLen := 3
	if (flags & 0x02) != 0 {
		headerLen = 9
	}
	if len(data) < headerLen {
		return 0, 0, 0, errQlzDataTooShortForHeader
	}

	return headerLen, getDecompressedSize(data), flags, nil
}

func (q *Qlz) ensureControl(data []byte, st *qlzState) bool {
	if st.control != 1 {
		return true
	}
	if st.sourcePos+4 > len(data) {
		return false
	}
	st.control = binary.LittleEndian.Uint32(data[st.sourcePos : st.sourcePos+4])
	st.sourcePos += 4

	return true
}

func (q *Qlz) processReference(data, dest []byte, st *qlzState) bool {
	st.control >>= 1
	if st.sourcePos+2 > len(data) {
		return false
	}
	b1 := data[st.sourcePos]
	b2 := data[st.sourcePos+1]
	st.sourcePos += 2

	hash := int(b1>>4) | (int(b2) << 4)
	matchlen := int(b1 & 0x0F)
	if matchlen != 0 {
		matchlen += 2
	} else {
		if st.sourcePos >= len(data) {
			return false
		}
		matchlen = int(data[st.sourcePos])
		st.sourcePos++
	}

	offset := q.hashtable[hash]
	for i := range matchlen {
		if st.destPos < len(dest) && offset+i < st.destPos {
			dest[st.destPos] = dest[offset+i]
			st.destPos++
		}
	}

	end := st.destPos + 1 - matchlen
	q.updateHashtable(dest, &st.nextHashed, end)
	st.nextHashed = st.destPos

	return true
}

func (q *Qlz) processLiteral(data, dest []byte, decompressedSize int, st *qlzState) bool {
	if st.destPos >= max(decompressedSize, 10)-10 {
		for st.destPos < decompressedSize {
			if st.control == 1 {
				st.sourcePos += 4
				if st.sourcePos > len(data) {
					break
				}
				st.control = binary.LittleEndian.Uint32(data[st.sourcePos-4 : st.sourcePos])
			}
			if st.sourcePos >= len(data) {
				break
			}
			dest[st.destPos] = data[st.sourcePos]
			st.destPos++
			st.sourcePos++
			st.control >>= 1
		}

		return true
	}
	if st.sourcePos >= len(data) || st.destPos >= len(dest) {
		return true
	}
	dest[st.destPos] = data[st.sourcePos]
	st.destPos++
	st.sourcePos++
	st.control >>= 1
	end := max(st.destPos-2, 0)
	q.updateHashtable(dest, &st.nextHashed, end)
	if st.nextHashed < end {
		st.nextHashed = end
	}

	return false
}

func (q *Qlz) updateHashtable(dest []byte, nextHashed *int, end int) {
	for *nextHashed < end {
		if *nextHashed+3 > len(dest) {
			break
		}
		v := uint32(dest[*nextHashed]) | (uint32(dest[*nextHashed+1]) << 8) | (uint32(dest[*nextHashed+2]) << 16)
		hash := ((v >> 12) ^ v) & 0xFFF
		q.hashtable[hash] = *nextHashed
		*nextHashed++
	}
}
