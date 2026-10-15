package updates

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type installPlan struct {
	Target     string `json:"target"`
	DesktopPID int    `json:"desktop_pid"`
	CorePID    int    `json:"core_pid"`
	CoreSHA256 string `json:"core_sha256"`
}

func InstallCommitted(planPath string) bool {
	directory := filepath.Dir(planPath)
	return markerExists(directory, "armed") && !markerExists(directory, "abort")
}

func (m *Manager) PrepareInstall(desktopPID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.done != nil || m.status.State != "ready" || !m.status.CanInstall {
		return errors.New("更新包尚未就绪或此平台暂不支持自动安装")
	}
	executable, err := os.Executable()
	if err != nil {
		return errors.New("无法定位安装目录")
	}
	target := filepath.Dir(executable)
	if filepath.Base(executable) != "resona-core.exe" {
		return errors.New("自动更新仅支持官方便携包")
	}
	for _, name := range []string{"resona-desktop.exe", "resona_webrtc_apm.dll"} {
		if info, err := os.Lstat(filepath.Join(target, name)); err != nil || !info.Mode().IsRegular() {
			return errors.New("安装目录缺少产品文件")
		}
	}
	if !packageMarker(target) {
		return errors.New("安装目录缺少官方包标记，拒绝整目录更新")
	}
	if _, err := os.Stat(target + ".previous"); !os.IsNotExist(err) {
		return errors.New("上次更新备份仍在，请验收后移走安装目录旁的 .previous 备份")
	}
	digest, err := fileDigest(executable)
	if err != nil {
		return errors.New("无法校验当前 Core")
	}
	attemptDir, err := os.MkdirTemp(m.directory, "install-")
	if err != nil {
		return errors.New("无法创建更新事务")
	}
	handedOff := false
	var cmd *exec.Cmd
	defer func() {
		if !handedOff {
			_ = os.WriteFile(filepath.Join(attemptDir, "abort"), nil, 0o600)
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
	}()
	helper := filepath.Join(attemptDir, "update-helper.exe")
	if err := copyFile(executable, helper); err != nil {
		return errors.New("无法准备更新安装器")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return errors.New("无法读取当前运行库")
	}
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".dll") {
			continue
		}
		if err := copyFile(filepath.Join(target, entry.Name()), filepath.Join(attemptDir, entry.Name())); err != nil {
			return errors.New("无法准备更新安装器运行库")
		}
	}
	plan, _ := json.Marshal(installPlan{Target: target, DesktopPID: desktopPID, CorePID: os.Getpid(), CoreSHA256: digest})
	planPath := filepath.Join(attemptDir, "install.json")
	if err := os.WriteFile(planPath, plan, 0o600); err != nil {
		return errors.New("无法准备更新计划")
	}
	cmd = exec.Command(helper, "--apply-update", planPath)
	cmd.Dir = attemptDir
	configureHelper(cmd)
	if err := cmd.Start(); err != nil {
		return errors.New("无法启动更新安装器，应用尚未退出")
	}
	if !waitMarker(attemptDir, "ready", time.Now().Add(5*time.Second)) {
		return errors.New("更新安装器准备失败，应用保持运行")
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "commit"), []byte("install"), 0o600); err != nil {
		return errors.New("无法提交更新，应用保持运行")
	}
	if !waitMarker(attemptDir, "armed", time.Now().Add(5*time.Second)) {
		return errors.New("更新安装器未确认交接，应用保持运行")
	}
	handedOff = true
	_ = cmd.Process.Release()
	m.status.State = "installing"
	return nil
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if syncErr := out.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

// Extract only into a new private directory. Reject Windows path aliases, links,
// duplicate entries, oversized expansion and configuration files in the bundle.
func extractPackage(archive, destination, version string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	if len(r.File) > 256 {
		return errors.New("更新包文件过多")
	}
	seen := make(map[string]bool)
	var expanded uint64
	for _, entry := range r.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" || strings.Contains(name, "\\") || filepath.IsAbs(name) || entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("更新包路径无效")
		}
		for _, part := range strings.Split(name, "/") {
			if part == "" || part == "." || part == ".." || strings.ContainsAny(part, ":\x00") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
				return errors.New("更新包路径无效")
			}
			base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
			if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
				return errors.New("更新包路径无效")
			}
		}
		key := strings.ToLower(name)
		if seen[key] {
			return errors.New("更新包包含重复路径")
		}
		seen[key] = true
		if key == "servers.json" || key == "native-preferences.json" || strings.HasSuffix(key, ".key") {
			return errors.New("更新包不能包含用户配置")
		}
		if entry.UncompressedSize64 > uint64(MaxPackageSize) || expanded+entry.UncompressedSize64 > uint64(1<<30) {
			return errors.New("更新包解压大小超限")
		}
		expanded += entry.UncompressedSize64
		path := filepath.Join(destination, filepath.FromSlash(name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return errors.New("更新包文件类型无效")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		in, err := entry.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			in.Close()
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, int64(entry.UncompressedSize64)+1))
		in.Close()
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil || n != int64(entry.UncompressedSize64) {
			return errors.New("更新包解压失败")
		}
	}
	for _, name := range []string{"resona-desktop.exe", "resona-core.exe", "resona_webrtc_apm.dll"} {
		if info, err := os.Lstat(filepath.Join(destination, name)); err != nil || !info.Mode().IsRegular() {
			return errors.New("更新包缺少必需产品文件")
		}
	}
	data, err := readBoundedFile(filepath.Join(destination, "resona-package.json"), 4096)
	if err != nil {
		return errors.New("更新包缺少产品版本标记")
	}
	if !packageMarker(destination) {
		return errors.New("更新包缺少官方包标记")
	}
	return verifyPackageMetadata(data, version)
}

func verifyArchive(archive string, asset Asset) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(f, asset.Size+1))
	if err != nil || n != asset.Size || hex.EncodeToString(digest.Sum(nil)) != asset.SHA256 {
		return errors.New("更新包完整性验证失败")
	}
	return nil
}

// The trusted copied core verifies the package again after both old processes
// exit. A failed directory replacement restores the complete old installation.
func Apply(planPath string) error {
	if !canApply() {
		return errors.New("此平台不支持便携包自动安装")
	}
	data, err := readBoundedFile(planPath, 4096)
	if err != nil {
		return errors.New("更新计划无效")
	}
	var plan installPlan
	if strictJSON(data, &plan) != nil || !filepath.IsAbs(plan.Target) || plan.DesktopPID <= 0 || plan.CorePID <= 0 {
		return errors.New("更新计划无效")
	}
	attemptDir := filepath.Dir(planPath)
	cache := filepath.Dir(attemptDir)
	data, err = readBoundedFile(filepath.Join(cache, "manifest.json"), 64<<10)
	if err != nil {
		return err
	}
	manifest, err := Verify(data, PublicKey, time.Now())
	if err != nil {
		return err
	}
	newer, err := Newer(manifest.Version, currentVersion())
	if err != nil || !newer {
		return errors.New("拒绝安装旧版本")
	}
	asset, ok := manifest.Assets["windows/amd64"]
	if !ok {
		return errors.New("无 Windows 更新包")
	}
	archive := filepath.Join(cache, "package.zip")
	if info, err := os.Lstat(plan.Target); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || filepath.Dir(plan.Target) == plan.Target {
		return errors.New("安装目录无效")
	}
	resolved, err := filepath.EvalSymlinks(plan.Target)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(plan.Target)) {
		return errors.New("安装目录包含重解析路径")
	}
	if digest, err := fileDigest(filepath.Join(plan.Target, "resona-core.exe")); err != nil || digest != plan.CoreSHA256 {
		return errors.New("原安装目录已改变，更新未安装")
	}
	if !packageMarker(plan.Target) {
		return errors.New("原安装目录缺少官方包标记")
	}
	wait, closeHandles, err := prepareParentWait(plan.DesktopPID, plan.CorePID)
	if err != nil {
		return err
	}
	defer closeHandles()
	if err := os.WriteFile(filepath.Join(attemptDir, "ready"), []byte("ready"), 0o600); err != nil {
		return err
	}
	if !waitMarker(attemptDir, "commit", time.Now().Add(10*time.Second)) {
		return errors.New("更新未提交，保留原版")
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "armed"), nil, 0o600); err != nil {
		return err
	}
	if err := wait(); err != nil {
		return err
	}
	if markerExists(attemptDir, "abort") {
		return errors.New("更新交接已取消，保留原版")
	}
	if err := verifyArchive(archive, asset); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(plan.Target), ".resona-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := extractPackage(archive, stage, manifest.Version); err != nil {
		return err
	}
	if !packageMarker(plan.Target) {
		return errors.New("原安装目录标记已改变")
	}
	if digest, err := fileDigest(filepath.Join(plan.Target, "resona-core.exe")); err != nil || digest != plan.CoreSHA256 {
		return errors.New("原安装目录已改变")
	}
	backup := plan.Target + ".previous"
	if err := replaceDirectory(plan.Target, stage, backup); err != nil {
		return err
	}
	if err := launchDesktop(filepath.Join(plan.Target, "resona-desktop.exe")); err != nil {
		if moveErr := os.Rename(plan.Target, stage); moveErr != nil {
			return errors.New("启动失败，请从 .previous 目录恢复")
		}
		if rollbackErr := os.Rename(backup, plan.Target); rollbackErr != nil {
			return errors.New("启动及回滚失败，请从 .previous 目录恢复")
		}
		_ = launchDesktop(filepath.Join(plan.Target, "resona-desktop.exe"))
		return errors.New("新版启动失败，已恢复原版")
	}
	return nil
}

func packageMarker(directory string) bool {
	info, err := os.Lstat(filepath.Join(directory, ".resona-package"))
	return err == nil && info.Mode().IsRegular()
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("更新元数据大小超限")
	}
	return data, nil
}

func markerExists(directory, name string) bool {
	info, err := os.Lstat(filepath.Join(directory, name))
	return err == nil && info.Mode().IsRegular()
}

func waitMarker(directory, name string, deadline time.Time) bool {
	for {
		if !time.Now().Before(deadline) || markerExists(directory, "abort") {
			return false
		}
		if markerExists(directory, name) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func verifyPackageMetadata(data []byte, version string) error {
	var marker struct {
		Schema   int    `json:"schema"`
		Product  string `json:"product"`
		Platform string `json:"platform"`
		Version  string `json:"version"`
	}
	if len(data) > 4096 || strictJSON(data, &marker) != nil || marker.Schema != 1 || marker.Product != "resona" || marker.Platform != "windows/amd64" || marker.Version != version {
		return errors.New("更新包产品、平台或版本标记不匹配")
	}
	return nil
}

func ValidateWindowsArchive(path, version string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	names := make(map[string]bool)
	var metadata []byte
	for _, entry := range r.File {
		name := strings.ToLower(entry.Name)
		if _, exists := names[name]; exists {
			return errors.New("更新包包含重复路径")
		}
		names[name] = entry.Mode().IsRegular()
		if name == "resona-package.json" {
			in, err := entry.Open()
			if err != nil {
				return err
			}
			metadata, err = io.ReadAll(io.LimitReader(in, 4097))
			in.Close()
			if err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"resona-desktop.exe", "resona-core.exe", "resona_webrtc_apm.dll", ".resona-package", "resona-package.json"} {
		if !names[name] {
			return errors.New("更新包缺少必需的产品文件或标记")
		}
	}
	return verifyPackageMetadata(metadata, version)
}

func replaceDirectory(target, stage, backup string) error {
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("安装目录无效")
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		return errors.New("备份目录冲突")
	}
	if err := os.Rename(target, backup); err != nil {
		return errors.New("安装目录正在使用或无法写入，原版保持不变")
	}
	if err := os.Rename(stage, target); err != nil {
		if rollbackErr := os.Rename(backup, target); rollbackErr != nil {
			return errors.New("更新及回滚失败，请从旁边的 previous 目录恢复")
		}
		return errors.New("更新失败，已恢复原版")
	}
	return nil
}
