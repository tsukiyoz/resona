package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestActivationRejectsOldPasswordInputsBeforeStopping(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX deployment script")
	}
	for _, mode := range []string{"file", "environment"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"data", "access", "bin"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "data/noise.key"), []byte("test"), 0o600); err != nil {
				t.Fatal(err)
			}
			program := `#!/bin/sh
case "$*" in
  'image inspect '*) exit 0 ;;
  *State.Running*) echo true ;;
  *Config.Cmd*) if [ "$TEST_MODE" = file ]; then echo '["serve","--password-file","/access/private"]'; else echo '["serve"]'; fi ;;
  *Config.Env*) if [ "$TEST_MODE" = environment ]; then echo 'RESONA_SERVER_PASSWORD=do-not-expose-this'; fi ;;
  *) echo 'unexpected operation reached' >&2; exit 99 ;;
esac
`
			if err := os.WriteFile(filepath.Join(root, "bin/docker"), []byte(program), 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "activate.sh", "resona-test:local")
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"), "RESONA_ROOT="+root, "TEST_MODE="+mode)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "Import the old connection password") || strings.Contains(string(out), "do-not-expose-this") || strings.Contains(string(out), "unexpected operation") {
				t.Fatal("unsafe upgrade or leaked password", err, string(out))
			}
		})
	}
}
