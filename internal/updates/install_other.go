//go:build !windows

package updates

import (
	"errors"
	"os/exec"

	"github.com/tsukiyoz/resona/internal/version"
)

func configureHelper(*exec.Cmd) {}
func canApply() bool            { return false }
func currentVersion() string    { return version.Current().Version }
func prepareParentWait(...int) (func() error, func(), error) {
	return nil, nil, errors.New("此平台不支持自动安装")
}
func ReportInstallError(error)   {}
func launchDesktop(string) error { return errors.New("此平台不支持自动安装") }
