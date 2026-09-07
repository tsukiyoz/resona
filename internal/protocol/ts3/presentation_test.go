package ts3

import "testing"

func TestChannelPresentation(t *testing.T) {
	for _, tt := range []struct {
		name, parent, kind, display, align string
		permanent, repeat                  bool
	}{
		{"[spacer1]", "0", "separator", "", "left", true, false},
		{"[cspacer12]Community", "0", "separator", "Community", "center", true, false},
		{"[rspacer]Staff", "0", "separator", "Staff", "right", true, false},
		{"[lspacer2]Rooms", "", "separator", "Rooms", "left", true, false},
		{"[*spacer3]--", "0", "separator", "--", "left", true, true},
		{"[cspacerid]Title", "0", "separator", "Title", "center", true, false},
		{"[spacer1]", "2", "channel", "[spacer1]", "left", true, false},
		{"[spacer1]", "0", "channel", "[spacer1]", "left", false, false},
		{"[xspacer1]Title", "0", "channel", "[xspacer1]Title", "left", true, false},
		{"prefix[spacer1]", "0", "channel", "prefix[spacer1]", "left", true, false},
		{"[spacer1", "0", "channel", "[spacer1", "left", true, false},
	} {
		t.Run(tt.name+tt.parent, func(t *testing.T) {
			got := parseChannelPresentation(tt.name, tt.parent, tt.permanent)
			if got.Kind != tt.kind || got.DisplayName != tt.display || got.Align != tt.align || got.Repeat != tt.repeat {
				t.Fatalf("presentation = %#v", got)
			}
		})
	}
}

func TestNormalizeIconID(t *testing.T) {
	for raw, want := range map[string]string{"": "", "0": "", "100": "100", "-1": "4294967295", "-2147483648": "2147483648", "4294967295": "4294967295", "4294967296": "", "-2147483649": "", "../icon": "", "0x123": ""} {
		if got := normalizeIconID(raw); got != want {
			t.Errorf("normalizeIconID(%q) = %q; want %q", raw, got, want)
		}
	}
	if customIconID("999") || customIconID("-1") || !customIconID("1000") {
		t.Fatal("invalid custom icon range")
	}
}
