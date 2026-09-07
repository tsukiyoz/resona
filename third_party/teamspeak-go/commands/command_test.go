package commands_test

import (
	"strings"
	"testing"

	"github.com/honeybbq/teamspeak-go/commands"
)

const (
	cmdClientlist = "clientlist"
	strHelloWorld = "hello world"
)

func TestEscapeSpecialChars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"hello", "hello"},
		{strHelloWorld, "hello\\sworld"},
		{"a\\b", "a\\\\b"},
		{"a/b", "a\\/b"},
		{"a|b", "a\\pb"},
		{"a\ab", "a\\ab"},
		{"a\bb", "a\\bb"},
		{"a\fb", "a\\fb"},
		{"a\nb", "a\\nb"},
		{"a\rb", "a\\rb"},
		{"a\tb", "a\\tb"},
		{"a\vb", "a\\vb"},
		{"back\\slash /slash |pipe", "back\\\\slash\\s\\/slash\\s\\ppipe"},
	}
	for _, tt := range tests {
		got := commands.Escape(tt.input)
		if got != tt.expected {
			t.Errorf("Escape(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestUnescapeSpecialChars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"hello", "hello"},
		{"hello\\sworld", strHelloWorld},
		{"a\\\\b", "a\\b"},
		{"a\\/b", "a/b"},
		{"a\\pb", "a|b"},
		{"a\\ab", "a\ab"},
		{"a\\bb", "a\bb"},
		{"a\\fb", "a\fb"},
		{"a\\nb", "a\nb"},
		{"a\\rb", "a\rb"},
		{"a\\tb", "a\tb"},
		{"a\\vb", "a\vb"},
	}
	for _, tt := range tests {
		got := commands.Unescape(tt.input)
		if got != tt.expected {
			t.Errorf("Unescape(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEscapeUnescapeRoundtrip(t *testing.T) {
	inputs := []string{
		strHelloWorld,
		"back\\slash",
		"pipe|val",
		"slash/val",
		"all\\ \t\n\r",
		"",
		"plain text",
		"mix\\ed /chars|here",
	}
	for _, input := range inputs {
		got := commands.Unescape(commands.Escape(input))
		if got != input {
			t.Errorf("Unescape(Escape(%q)) = %q", input, got)
		}
	}
}

func TestParseCommandEmpty(t *testing.T) {
	if commands.ParseCommand("") != nil {
		t.Error("expected nil for empty string")
	}
}

func TestParseCommandNameOnly(t *testing.T) {
	cmd := commands.ParseCommand(cmdClientlist)
	if cmd == nil {
		t.Fatal("expected non-nil")
	}
	if cmd.Name != cmdClientlist {
		t.Errorf("Name = %q, want %q", cmd.Name, cmdClientlist)
	}
	if len(cmd.Params) != 0 {
		t.Errorf("expected 0 params, got %d", len(cmd.Params))
	}
}

func TestParseCommandWithParams(t *testing.T) {
	cmd := commands.ParseCommand("notifytextmessage targetmode=3 msg=hello\\sworld invokerid=42")
	if cmd == nil {
		t.Fatal("expected non-nil")
	}
	if cmd.Name != "notifytextmessage" {
		t.Errorf("Name = %q", cmd.Name)
	}
	if cmd.Params["targetmode"] != "3" {
		t.Errorf("targetmode = %q", cmd.Params["targetmode"])
	}
	if cmd.Params["msg"] != strHelloWorld {
		t.Errorf("msg = %q", cmd.Params["msg"])
	}
	if cmd.Params["invokerid"] != "42" {
		t.Errorf("invokerid = %q", cmd.Params["invokerid"])
	}
}

func TestParseCommandFlagParam(t *testing.T) {
	cmd := commands.ParseCommand("serverinfo -virtualserver_flag")
	if cmd == nil {
		t.Fatal("expected non-nil")
	}
	if _, ok := cmd.Params["-virtualserver_flag"]; !ok {
		t.Error("expected flag param to exist")
	}
	if cmd.Params["-virtualserver_flag"] != "" {
		t.Errorf("flag param value should be empty, got %q", cmd.Params["-virtualserver_flag"])
	}
}

func TestParseCommandLeadingNonPrintable(t *testing.T) {
	input := "\x00\x01\x02" + cmdClientlist + " clid=1"
	cmd := commands.ParseCommand(input)
	if cmd == nil {
		t.Fatal("expected non-nil")
	}
	if cmd.Name != cmdClientlist {
		t.Errorf("Name = %q, want %s", cmd.Name, cmdClientlist)
	}
	if cmd.Params["clid"] != "1" {
		t.Errorf("clid = %q", cmd.Params["clid"])
	}
}

func TestParseCommandEscapedValues(t *testing.T) {
	cmd := commands.ParseCommand("test key=hello\\sworld pipe=a\\pb slash=a\\/b")
	if cmd == nil {
		t.Fatal("expected non-nil")
	}
	if cmd.Params["key"] != strHelloWorld {
		t.Errorf("key = %q", cmd.Params["key"])
	}
	if cmd.Params["pipe"] != "a|b" {
		t.Errorf("pipe = %q", cmd.Params["pipe"])
	}
	if cmd.Params["slash"] != "a/b" {
		t.Errorf("slash = %q", cmd.Params["slash"])
	}
}

func TestBuildCommandNoParams(t *testing.T) {
	result := commands.BuildCommand(cmdClientlist, nil)
	if result != cmdClientlist {
		t.Errorf("BuildCommand = %q, want %q", result, cmdClientlist)
	}
}

func TestBuildCommandEscapesValues(t *testing.T) {
	result := commands.BuildCommand("sendtextmessage", map[string]string{
		"msg": strHelloWorld,
	})
	if !strings.Contains(result, "msg=hello\\sworld") {
		t.Errorf("BuildCommand missing escaped value: %q", result)
	}
	if !strings.HasPrefix(result, "sendtextmessage") {
		t.Errorf("BuildCommand missing command name: %q", result)
	}
}

func TestBuildCommandOrderedPreservesOrder(t *testing.T) {
	result := commands.BuildCommandOrdered("clientinitiv", [][2]string{
		{"alpha", "aaa"},
		{"omega", "bbb"},
		{"ot", "1"},
		{"ip", ""},
	})
	expected := "clientinitiv alpha=aaa omega=bbb ot=1 ip="
	if result != expected {
		t.Errorf("BuildCommandOrdered = %q, want %q", result, expected)
	}
}

func TestBuildCommandOrderedEscapesValues(t *testing.T) {
	result := commands.BuildCommandOrdered("cmd", [][2]string{
		{"key", "a b|c"},
	})
	if !strings.Contains(result, "key=a\\sb\\pc") {
		t.Errorf("BuildCommandOrdered missing escaped: %q", result)
	}
}

func TestCommandBuildMethod(t *testing.T) {
	cmd := &commands.Command{
		Name:   "sendtextmessage",
		Params: map[string]string{"msg": "hi"},
	}
	result := cmd.Build()
	if !strings.HasPrefix(result, "sendtextmessage") {
		t.Errorf("Build() = %q, missing command name", result)
	}
	if !strings.Contains(result, "msg=hi") {
		t.Errorf("Build() = %q, missing param", result)
	}
}

func TestParseBuildRoundtrip(t *testing.T) {
	original := commands.BuildCommandOrdered("test", [][2]string{
		{"key1", "val1"},
		{"key2", strHelloWorld},
		{"key3", "pipe|val"},
		{"key4", "back\\slash"},
	})
	cmd := commands.ParseCommand(original)
	if cmd == nil {
		t.Fatal("ParseCommand returned nil")
	}
	if cmd.Name != "test" {
		t.Errorf("Name = %q", cmd.Name)
	}
	if cmd.Params["key1"] != "val1" {
		t.Errorf("key1 = %q", cmd.Params["key1"])
	}
	if cmd.Params["key2"] != strHelloWorld {
		t.Errorf("key2 = %q", cmd.Params["key2"])
	}
	if cmd.Params["key3"] != "pipe|val" {
		t.Errorf("key3 = %q", cmd.Params["key3"])
	}
	if cmd.Params["key4"] != "back\\slash" {
		t.Errorf("key4 = %q", cmd.Params["key4"])
	}
}
