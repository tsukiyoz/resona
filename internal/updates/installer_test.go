package updates

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallerManifestDistribution(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	manifest := testManifest()
	manifest.Schema = 2
	asset := manifest.Assets["windows/amd64"]
	asset.Distribution = DistributionInstaller
	manifest.Assets["windows/amd64"] = asset
	key := base64.StdEncoding.EncodeToString(public)
	if _, err := Verify(signedForTest(t, manifest, private), key, time.Now()); err != nil {
		t.Fatal(err)
	}
	manifest.Schema = 1
	if _, err := Verify(signedForTest(t, manifest, private), key, time.Now()); err == nil {
		t.Fatal("installer accepted by portable schema")
	}
}

func TestInstallerPackageIdentityAndPaths(t *testing.T) {
	for _, test := range []struct {
		name, distribution, specVersion, extra string
		valid                                  bool
	}{
		{"valid", "installer", "1.2.3", "", true},
		{"portable marker", "portable", "1.2.3", "", false},
		{"different nuspec", "installer", "1.2.4", "", false},
		{"traversal", "installer", "1.2.3", "lib/app/../../escape.exe", false},
		{"leading traversal", "installer", "1.2.3", "../escape.exe", false},
		{"parent directory", "installer", "1.2.3", "../", false},
		{"duplicate case", "installer", "1.2.3", "lib/app/RESONA-desktop.exe", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "package.nupkg")
			file, err := os.Create(filename)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(file)
			entries := map[string]string{
				"lib/app/resona-desktop.exe": "executable", "lib/app/resona_webrtc_apm.dll": "audio",
				"lib/app/resona-package.json": `{"schema":1,"product":"resona","platform":"windows/amd64","version":"v1.2.3","distribution":"` + test.distribution + `"}`,
				"Resona.nuspec":               `<package><metadata><id>Resona</id><version>` + test.specVersion + `</version></metadata></package>`,
			}
			if test.extra != "" {
				entries[test.extra] = "unsafe"
			}
			for name, content := range entries {
				entry, err := writer.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(name, "/") {
					continue
				}
				if _, err = strings.NewReader(content).WriteTo(entry); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := ValidateWindowsInstaller(filename, "v1.2.3"); (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}
