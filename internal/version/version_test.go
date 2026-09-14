package version

import (
	"runtime/debug"
	"testing"
)

func TestBuildMetadataFallbackAndOverrides(t *testing.T) {
	oldV, oldC, oldT, oldD := Version, Commit, BuildTime, Dirty
	t.Cleanup(func() { Version, Commit, BuildTime, Dirty = oldV, oldC, oldT, oldD })
	Version, Commit, BuildTime, Dirty = "dev", "", "", ""
	build := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}, {Key: "vcs.time", Value: "not-a-build-time"}}}
	got := fromBuild(build)
	if got.Commit != "abc" || got.Dirty != "true" || got.BuildTime != "unknown" {
		t.Fatalf("wrong fallback: %+v", got)
	}
	Version, Commit, BuildTime, Dirty = "v1.2.3", "override", "2026-09-14T00:00:00Z", "false"
	got = fromBuild(build)
	if got.Version != Version || got.Commit != Commit || got.BuildTime != BuildTime || got.Dirty != Dirty {
		t.Fatalf("override lost: %+v", got)
	}
	Commit, Dirty = "", ""
	got = fromBuild(nil)
	if got.Commit != "unknown" || got.Dirty != "unknown" {
		t.Fatalf("missing metadata presented as clean: %+v", got)
	}
}

func TestVersionQueryDoesNotMatchFlagValue(t *testing.T) {
	if !Requested([]string{"--version"}) || !Requested([]string{"-version"}) {
		t.Fatal("query rejected")
	}
	if Requested([]string{"--name", "--version"}) || Requested(nil) {
		t.Fatal("non-query matched")
	}
}
