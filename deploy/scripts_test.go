package deploy_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestActivationRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX deployment script")
	}
	source, err := os.ReadFile("activate.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"", "stop", "backup", "run", "health"} {
		t.Run("fault_"+fault, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			for _, dir := range []string{"bin", "data", "access", "state"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(name, data string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("activate.sh", string(source), 0o700)
			write("data/noise.key", "test-only-identity", 0o600)
			write("bin/docker", fakeDocker, 0o700)
			write("bin/tar", "#!/bin/sh\nif [ \"$FAULT\" = backup ]; then exit 1; fi\nexec /usr/bin/tar \"$@\"\n", 0o700)
			cmd := exec.Command("sh", filepath.Join(root, "activate.sh"), "resona-test:local")
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RESONA_ROOT="+root,
				"RESONA_CONTAINER=resona-test", "RESONA_START_WAIT=0", "RESONA_BIND_IP=127.0.0.1", "RESONA_PORT=19988", "FAULT="+fault, "FAKE_STATE="+filepath.Join(root, "state"))
			out, err := cmd.CombinedOutput()
			if (err != nil) != (fault != "") {
				t.Fatalf("unexpected outcome: %v\n%s", err, out)
			}
			if bytes.Contains(out, []byte("test-environment-value")) {
				t.Fatal("environment leaked")
			}
			if _, err := os.Stat(filepath.Join(root, ".upgrade-resona-test.lock")); !os.IsNotExist(err) {
				t.Fatal("upgrade lock retained", err)
			}
			_, restored := os.Stat(filepath.Join(root, "state/restored"))
			if (restored == nil) != (fault != "") {
				t.Fatal("rollback restart state wrong", restored)
			}
			_, newContainer := os.Stat(filepath.Join(root, "state/new"))
			if (newContainer == nil) != (fault == "") {
				t.Fatal("new container cleanup wrong", newContainer)
			}
			backups, err := filepath.Glob(filepath.Join(root, "backups/*/environment"))
			if err != nil || len(backups) != 1 {
				t.Fatal("missing environment backup", err)
			}
			info, err := os.Stat(backups[0])
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("environment permissions", err)
			}
			key, _ := os.ReadFile(filepath.Join(root, "data/noise.key"))
			if string(key) != "test-only-identity" {
				t.Fatal("identity modified")
			}
		})
	}
}

func TestPackageRejectsInvalidInputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX deployment script")
	}
	for _, setting := range []string{"VERSION=../../outside", "DEPLOY_ARCH=unsupported", "BUILD_TIME=bad time"} {
		cmd := exec.Command("sh", "package.sh")
		cmd.Env = append(os.Environ(), "VERSION=dev", "DEPLOY_ARCH=amd64", "BUILD_TIME=2026-09-15T00:00:00Z", setting)
		out, err := cmd.CombinedOutput()
		if err == nil || (!strings.Contains(string(out), "Invalid") && !strings.Contains(string(out), "DEPLOY_ARCH")) {
			t.Fatalf("input not rejected before building: %s, %v, %s", setting, err, out)
		}
	}
}

func TestPackagePreservesUnknownOutputAndGenerationLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX deployment script")
	}
	source, err := os.ReadFile("package.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	base := filepath.Join(root, "build/deploy")
	output := filepath.Join(base, "resona-server-test-linux-amd64")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "deploy"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "deploy/package.sh")
	if err := os.WriteFile(script, source, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(output, "private-data")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(want string) {
		t.Helper()
		cmd := exec.Command("sh", script)
		cmd.Env = append(os.Environ(), "VERSION=test", "DEPLOY_ARCH=amd64", "BUILD_TIME=2026-09-15T00:00:00Z")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), want) {
			t.Fatalf("missing refusal %q: %v %s", want, err, out)
		}
	}
	run("unrecognized deployment directory")
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "keep" {
		t.Fatal("unknown output modified", err)
	}
	lock := filepath.Join(base, ".resona-server-test-linux-amd64.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal("failed generation retained lock", err)
	}
	run("already being generated")
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("other generation lock removed", err)
	}
}

const fakeDocker = `#!/bin/sh
set -eu
case "$1" in
  image) exit 0 ;;
  container) test -f "$FAKE_STATE/backup" ;;
  inspect)
    case "$*" in
      *Config.Env*) echo 'EXAMPLE=test-environment-value' ;;
      *) if [ "$FAULT" = health ] && [ -f "$FAKE_STATE/new" ]; then echo false; else echo true; fi ;;
    esac ;;
  stop) test "$FAULT" != stop ;;
  rename)
    case "$2" in
      *-backup-*) rm -f "$FAKE_STATE/backup" ;;
      *) touch "$FAKE_STATE/backup" ;;
    esac ;;
  run)
    while [ "$#" -gt 0 ]; do
      if [ "$1" = --cidfile ]; then shift; printf '%s' new-id > "$1"; fi
      shift
    done
    touch "$FAKE_STATE/new"
    test "$FAULT" != run ;;
  rm) rm -f "$FAKE_STATE/new" ;;
  start) touch "$FAKE_STATE/restored" ;;
  *) echo 'unexpected fake Docker operation' >&2; exit 2 ;;
esac
`
