package ts3

import (
	"regexp"
	"strconv"
)

var spacerName = regexp.MustCompile(`^\[([clr*]?)spacer[^\]]*\](.*)$`)

type channelPresentation struct {
	Kind        string
	DisplayName string
	Align       string
	Repeat      bool
}

// TS3 spacers remain real channels; only top-level permanent channels qualify.
func parseChannelPresentation(name, parentID string, permanent bool) channelPresentation {
	p := channelPresentation{Kind: "channel", DisplayName: name, Align: "left"}
	if !permanent || (parentID != "" && parentID != "0") {
		return p
	}
	match := spacerName.FindStringSubmatch(name)
	if match == nil {
		return p
	}
	p.Kind, p.DisplayName = "separator", match[2]
	switch match[1] {
	case "c":
		p.Align = "center"
	case "r":
		p.Align = "right"
	case "*":
		p.Repeat = true
	}
	return p
}

// Some TS3 permission paths serialize a CRC32 icon identifier as signed int32.
func normalizeIconID(raw string) string {
	if n, err := strconv.ParseUint(raw, 10, 32); err == nil && n != 0 {
		return strconv.FormatUint(n, 10)
	}
	if n, err := strconv.ParseInt(raw, 10, 32); err == nil && n < 0 {
		return strconv.FormatUint(uint64(uint32(n)), 10)
	}
	return ""
}

func customIconID(id string) bool {
	n, err := strconv.ParseUint(id, 10, 32)
	return err == nil && n > 999
}
