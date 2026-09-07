package commands

import (
	"strings"
)

func ParseCommand(s string) *Command {
	if s == "" {
		return nil
	}

	startIndex := 0
	for i := range len(s) {
		if s[i] >= 32 && s[i] <= 126 {
			startIndex = i

			break
		}
	}
	if startIndex > 0 {
		s = s[startIndex:]
	}

	parts := strings.Split(s, " ")
	if len(parts) == 0 {
		return nil
	}

	cmd := &Command{
		Name:   parts[0],
		Params: make(map[string]string),
	}

	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 {
			cmd.Params[Unescape(kv[0])] = Unescape(kv[1])
		} else {
			cmd.Params[Unescape(p)] = ""
		}
	}

	return cmd
}

var unescaper = strings.NewReplacer(
	"\\\\", "\\",
	"\\/", "/",
	"\\s", " ",
	"\\p", "|",
	"\\a", "\a",
	"\\b", "\b",
	"\\f", "\f",
	"\\n", "\n",
	"\\r", "\r",
	"\\t", "\t",
	"\\v", "\v",
)

func Unescape(s string) string {
	return unescaper.Replace(s)
}
