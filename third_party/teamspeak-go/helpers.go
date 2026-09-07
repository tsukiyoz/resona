package teamspeak

import (
	"math"
	"strconv"
	"strings"

	"github.com/honeybbq/teamspeak-go/commands"
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
	var shared map[string]string
	shareMemberContext := name == "notifycliententerview" || name == "notifyclientleftview" || name == "notifyclientmoved"
	for _, part := range parts {
		if part == "" {
			continue
		}
		row := name + " " + part
		if shareMemberContext {
			parsed := commands.ParseCommand(row)
			if shared == nil {
				shared = parsed.Params
			} else {
				// TS3 batches put common movement context on the first member row.
				// Keep per-member fields local and preserve explicit row overrides.
				for _, key := range []string{"cfid", "ctid", "reasonid", "reasonmsg", "invokerid", "invokername", "invokeruid", "bantime"} {
					value, inherited := shared[key]
					if _, explicit := parsed.Params[key]; inherited && !explicit {
						row += " " + key + "=" + commands.Escape(value)
					}
				}
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return []string{line}
	}

	return rows
}
