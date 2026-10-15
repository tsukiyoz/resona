package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSigningKeyMatchesClientTrust(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, encoded string
		valid         bool
	}{
		{"matching", base64.StdEncoding.EncodeToString(public), true},
		{"different signing identity", base64.StdEncoding.EncodeToString(other), false},
		{"missing client trust", "", false},
		{"invalid encoding", "invalid", false},
		{"wrong length", base64.StdEncoding.EncodeToString([]byte("short")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validatePublicKey(private, tc.encoded); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestReleasePublicationCompletenessAndImmutability(t *testing.T) {
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("release shell tools unavailable")
		}
	}
	script, err := os.ReadFile("../../scripts/publish-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, releases, failure string
		success                 bool
		latest                  string
	}{
		{name: "complete first release", success: true, latest: "true"},
		{name: "older release", releases: `{"tag_name":"v0.2.0","draft":false,"prerelease":false}`, success: true, latest: "false"},
		{name: "existing version", releases: `{"tag_name":"v0.1.5","draft":false,"prerelease":false}`},
		{name: "incomplete builds", failure: "missing"},
		{name: "signature failure", failure: "sign"},
		{name: "upload failure", failure: "upload"},
		{name: "tag moved during build", failure: "tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(path, data string, mode os.FileMode) {
				t.Helper()
				full := filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(data), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("publish.sh", string(script), 0o700)
			write("VERSION", "0.1.5\n", 0o600)
			write("docs/releases/v0.1.5.md", "Test release", 0o600)
			for _, asset := range []string{"windows/Resona-win-x64.zip", "arm64/Resona-macos-arm64.zip", "x64/Resona-macos-x64.zip"} {
				if tc.failure == "missing" && strings.HasPrefix(asset, "x64/") {
					continue
				}
				write("build/release/"+asset, "fixture", 0o600)
			}
			write("bin/gh", `#!/bin/bash
if [[ "$1" == api ]]; then printf '%s\n' "$MOCK_RELEASES"; exit 0; fi
printf '%s\n' "$*" >> "$MOCK_COMMANDS"
if [[ "$2" == upload && "$MOCK_FAILURE" == upload ]]; then exit 1; fi
`, 0o700)
			write("bin/go", `#!/bin/bash
if [[ "$MOCK_FAILURE" == sign ]]; then exit 1; fi
while [[ $# -gt 0 ]]; do
  if [[ "$1" == -out ]]; then printf '{}' > "$2"; exit 0; fi
  shift
done
exit 1
`, 0o700)
			write("bin/date", "#!/bin/bash\nprintf '2027-10-15T00:00:00Z\\n'\n", 0o700)
			write("bin/git", `#!/bin/bash
case "$1" in
  fetch) exit 0 ;;
  cat-file) echo tag ;;
  rev-parse)
    if [[ "$MOCK_FAILURE" == tag ]]; then echo changed; else echo "$GITHUB_SHA"; fi ;;
  *) exit 1 ;;
esac
`, 0o700)
			commands := filepath.Join(dir, "commands")
			cmd := exec.Command("bash", "publish.sh")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GITHUB_REPOSITORY=test/resona", "GITHUB_SHA=expected", "RELEASE_TAG=v0.1.5", "RESONA_UPDATE_PUBLIC_KEY=fixture", "RESONA_UPDATE_SIGNING_KEY=fixture",
				"MOCK_RELEASES="+tc.releases, "MOCK_FAILURE="+tc.failure, "MOCK_COMMANDS="+commands)
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("success=%v, error=%v, output=%s", tc.success, err, output)
			}
			recorded, _ := os.ReadFile(commands)
			calls := string(recorded)
			if tc.success {
				lines := strings.Split(strings.TrimSpace(calls), "\n")
				if len(lines) != 3 || !strings.HasPrefix(lines[0], "release create ") || !strings.Contains(lines[0], "--draft") ||
					!strings.HasPrefix(lines[1], "release upload ") || !strings.Contains(lines[1], "manifest.json") ||
					!strings.Contains(lines[2], "--draft=false --latest="+tc.latest) {
					t.Fatalf("unsafe publication order: %s", calls)
				}
			} else if strings.Contains(calls, "release edit ") {
				t.Fatalf("failed release must never become public: %s", calls)
			} else if tc.failure != "upload" && calls != "" {
				t.Fatalf("existing/incomplete/unsigned release must not create a draft: %s", calls)
			}
		})
	}
}
