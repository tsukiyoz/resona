package teamspeak

import (
	"math"
	"strconv"
	"strings"
)

func parseUint64Value(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}

func parseUint16Value(s string) (uint16, error) {
	v, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, err
	}

	return uint16(v), nil
}

func parseIntValue(s string) (int, error) {
	v, err := strconv.ParseInt(s, 10, strconv.IntSize)
	if err != nil {
		return 0, err
	}
	if strconv.IntSize == 32 && v > math.MaxInt32 {
		return math.MaxInt32, nil
	}

	return int(v), nil
}

// isAutoNicknameMatch reports whether actual equals expected or expected followed by only
// digits — the pattern TeamSpeak uses when a requested nickname is already taken.
func isAutoNicknameMatch(expected, actual string) bool {
	if actual == expected {
		return true
	}
	if !strings.HasPrefix(actual, expected) {
		return false
	}
	suffix := strings.TrimPrefix(actual, expected)
	for i := range len(suffix) {
		if suffix[i] < '0' || suffix[i] > '9' {
			return false
		}
	}

	return true
}

// splitCommandRows expands a pipe-separated multi-row TS3 command line into individual
// rows, each prefixed with the command name.
func splitCommandRows(line string) []string {
	before, after, ok := strings.Cut(line, " ")
	if !ok {
		return []string{line}
	}
	name := before
	rest := after
	if !strings.Contains(rest, "|") {
		return []string{line}
	}
	parts := strings.Split(rest, "|")
	rows := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		rows = append(rows, name+" "+part)
	}
	if len(rows) == 0 {
		return []string{line}
	}

	return rows
}
