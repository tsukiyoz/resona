// Package version reports reproducible build metadata without initializing services.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set these with -ldflags -X for release/container builds. Ordinary go build
// supplies revision and modified state through its embedded VCS metadata.
var (
	Version   = "dev"
	Commit    string
	BuildTime string
	Dirty     string
)

type Info struct {
	Version, Commit, BuildTime, Dirty, GoVersion, Platform string
}

func Current() Info {
	build, _ := debug.ReadBuildInfo()
	return fromBuild(build)
}

func fromBuild(build *debug.BuildInfo) Info {
	info := Info{Version: Version, Commit: Commit, BuildTime: BuildTime, Dirty: Dirty, GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if build != nil {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = setting.Value
				}
			case "vcs.modified":
				if info.Dirty == "" {
					info.Dirty = setting.Value
				}
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.BuildTime == "" {
		info.BuildTime = "unknown"
	}
	if info.Dirty == "" {
		info.Dirty = "unknown"
	}
	return info
}

func (i Info) String() string {
	return fmt.Sprintf("%s (commit=%s dirty=%s built=%s %s %s)", i.Version, i.Commit, i.Dirty, i.BuildTime, i.GoVersion, i.Platform)
}

// Requested recognizes a standalone query before flags with config-derived
// defaults are initialized. It deliberately does not match a flag value.
func Requested(args []string) bool {
	return len(args) == 1 && (args[0] == "--version" || args[0] == "-version")
}
