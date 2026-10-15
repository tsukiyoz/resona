package desktopipc

import (
	"context"
	"testing"

	"github.com/tsukiyoz/resona/internal/updates"
)

func TestPortableUpdateCommandsCannotDownloadOrInstall(t *testing.T) {
	updater := updates.New()
	defer updater.Close()
	for _, method := range []string{"DownloadUpdate", "GetUpdatePackage", "InstallUpdate"} {
		t.Run(method, func(t *testing.T) {
			if !updateMethod(method) {
				t.Fatal("legacy method escaped update dispatch")
			}
			value, err := dispatchUpdate(context.Background(), updater, request{Method: method}, func(updates.Status) { t.Fatal("unsupported command started work") })
			if err == nil || value != nil {
				t.Fatalf("unsupported command accepted: %v, %v", value, err)
			}
			if status := updater.Status(); status.State != "idle" || status.CanDownload || status.CanInstall {
				t.Fatalf("unsupported command mutated state: %+v", status)
			}
		})
	}
}
