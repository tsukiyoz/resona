package teamspeak

import (
	"reflect"
	"testing"

	"github.com/honeybbq/teamspeak-go/commands"
)

func TestSplitMemberRowsInitialSnapshot(t *testing.T) {
	rows := splitCommandRows(`notifycliententerview cfid=0 ctid=10 reasonid=2 clid=7 client_nickname=First client_unique_identifier=first|clid=8 client_nickname=Second|clid=9`)
	if len(rows) != 3 {
		t.Fatalf("expected three members, got %v", rows)
	}
	for i, id := range []string{"7", "8", "9"} {
		params := commands.ParseCommand(rows[i]).Params
		if params["clid"] != id || params["cfid"] != "0" || params["ctid"] != "10" || params["reasonid"] != "2" {
			t.Errorf("member %d missing subscription context: %v", i, params)
		}
		if i > 0 {
			if _, inherited := params["client_unique_identifier"]; inherited {
				t.Errorf("member %d inherited another identity", i)
			}
		}
	}
	if _, inherited := commands.ParseCommand(rows[2]).Params["client_nickname"]; inherited {
		t.Fatal("member without a nickname inherited another member's name")
	}
}

func TestSplitMemberRowsMovementContextAndOverrides(t *testing.T) {
	for _, name := range []string{"notifyclientmoved", "notifyclientleftview"} {
		t.Run(name, func(t *testing.T) {
			rows := splitCommandRows(name + ` cfid=10 ctid=20 reasonid=1 invokerid=4 invokername=Room\sAdmin invokeruid=abc\/def reasonmsg=literal\\s\swith\ppipe bantime=60 clid=7|clid=8 ctid=30 reasonid=4 reasonmsg=|clid=9`)
			if len(rows) != 3 {
				t.Fatalf("expected three members, got %v", rows)
			}
			second := commands.ParseCommand(rows[1]).Params
			want := map[string]string{
				"clid": "8", "cfid": "10", "ctid": "30", "reasonid": "4", "reasonmsg": "",
				"invokerid": "4", "invokername": "Room Admin", "invokeruid": "abc/def", "bantime": "60",
			}
			if !reflect.DeepEqual(second, want) {
				t.Errorf("explicit row context did not win: got %v, want %v", second, want)
			}
			third := commands.ParseCommand(rows[2]).Params
			if third["ctid"] != "20" || third["reasonid"] != "1" || third["reasonmsg"] != `literal\s with|pipe` {
				t.Errorf("shared first-row context changed or escaped twice: %v", third)
			}
		})
	}
}

func TestSplitMemberRowsDoNotShareUnrelatedCommands(t *testing.T) {
	for _, name := range []string{"clientlist", "channellist", "notifyclientupdated", "error"} {
		t.Run(name, func(t *testing.T) {
			rows := splitCommandRows(name + " cfid=0 ctid=10 reasonid=2 clid=7|clid=8")
			if len(rows) != 2 || rows[1] != name+" clid=8" {
				t.Errorf("unrelated command inherited member context: %v", rows)
			}
		})
	}
}
