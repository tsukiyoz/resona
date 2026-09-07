package commands

import (
	"strings"
)

// Command represents a parsed or built TS3 command line.
type Command struct {
	Params map[string]string
	Name   string
}

// Build returns the string representation of the command.
func (c *Command) Build() string {
	return BuildCommand(c.Name, c.Params)
}

var escaper = strings.NewReplacer(
	"\\", "\\\\",
	"/", "\\/",
	" ", "\\s",
	"|", "\\p",
	"\a", "\\a",
	"\b", "\\b",
	"\f", "\\f",
	"\n", "\\n",
	"\r", "\\r",
	"\t", "\\t",
	"\v", "\\v",
)

func Escape(s string) string {
	return escaper.Replace(s)
}

func BuildCommand(cmd string, params map[string]string) string {
	var res strings.Builder
	res.WriteString(Escape(cmd))
	for k, v := range params {
		res.WriteByte(' ')
		res.WriteString(k)
		res.WriteByte('=')
		res.WriteString(Escape(v))
	}

	return res.String()
}

func BuildCommandOrdered(cmd string, params [][2]string) string {
	var res strings.Builder
	res.WriteString(Escape(cmd))
	for _, kv := range params {
		res.WriteByte(' ')
		res.WriteString(kv[0])
		res.WriteByte('=')
		res.WriteString(Escape(kv[1]))
	}

	return res.String()
}
