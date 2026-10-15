package updates

import (
	"errors"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/tsukiyoz/resona/internal/version"
	"golang.org/x/sys/windows"
)

func configureHelper(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
func canApply() bool         { return true }
func currentVersion() string { return version.Current().Version }
func prepareParentWait(pids ...int) (func() error, func(), error) {
	var handles []windows.Handle
	closeHandles := func() {
		for _, handle := range handles {
			windows.CloseHandle(handle)
		}
	}
	for _, pid := range pids {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if err == windows.ERROR_INVALID_PARAMETER {
			continue
		}
		if err != nil {
			closeHandles()
			return nil, nil, errors.New("无法等待旧应用退出，更新未安装")
		}
		handles = append(handles, handle)
	}
	return func() error {
		if len(handles) == 0 {
			return nil
		}
		result, err := windows.WaitForMultipleObjects(handles, true, 30000)
		if err != nil || result == uint32(windows.WAIT_TIMEOUT) {
			return errors.New("旧应用尚未退出，更新未安装")
		}
		return nil
	}, closeHandles, nil
}

func ReportInstallError(err error) {
	message, _ := windows.UTF16PtrFromString("Resona 更新未完成：" + err.Error())
	title, _ := windows.UTF16PtrFromString("Resona 更新")
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}

func launchDesktop(path string) error {
	cmd := exec.Command(path)
	cmd.Dir = filepath.Dir(path)
	configureHelper(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
