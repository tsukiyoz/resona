package updates

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	DistributionPortable  = "portable"
	DistributionInstaller = "installer"
)

type packageMetadata struct {
	Schema       int    `json:"schema"`
	Product      string `json:"product"`
	Platform     string `json:"platform"`
	Version      string `json:"version"`
	Distribution string `json:"distribution"`
}

func ValidateWindowsArchive(path, version string) error {
	return validateReleaseArchive(path, "windows/amd64", version, "", []string{"resona-desktop.exe", "resona_webrtc_apm.dll", ".resona-package", "resona-package.json"})
}

func ValidateMacArchive(path, platform, version string) error {
	return validateReleaseArchive(path, platform, version, "resona.app/contents/resources/", []string{
		"resona.app/contents/macos/resona-desktop", "resona.app/contents/frameworks/libresona_webrtc_apm.dylib",
		"resona.app/contents/resources/.resona-bundle", "resona.app/contents/resources/resona-package.json",
	})
}

func validateReleaseArchive(path, platform, version, metadataDirectory string, required []string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	names := make(map[string]bool)
	var metadata []byte
	for _, entry := range archive.File {
		name := strings.ToLower(entry.Name)
		if _, exists := names[name]; exists {
			return errors.New("发布包包含重复路径")
		}
		names[name] = entry.Mode().IsRegular()
		if name == metadataDirectory+"resona-package.json" {
			file, err := entry.Open()
			if err != nil {
				return err
			}
			metadata, err = io.ReadAll(io.LimitReader(file, 4097))
			file.Close()
			if err != nil {
				return err
			}
		}
	}
	for _, name := range required {
		if !names[name] {
			return errors.New("发布包缺少必需的产品文件或标记")
		}
	}
	marker, err := parsePackageMetadata(metadata, platform, version)
	if err != nil {
		return err
	}
	if marker.Distribution != DistributionPortable {
		return errors.New("ZIP 发布包必须标记为 portable")
	}
	return nil
}

func parsePackageMetadata(data []byte, platform, version string) (packageMetadata, error) {
	var metadata packageMetadata
	if len(data) > 4096 || strictJSON(data, &metadata) != nil || metadata.Schema != 1 ||
		metadata.Product != "resona" || metadata.Platform != platform || metadata.Version != version ||
		metadata.Distribution == "" || len(metadata.Distribution) > 32 {
		return metadata, errors.New("包类型、产品、平台或版本标记不匹配")
	}
	return metadata, nil
}

// Unpackaged, legacy and invalid metadata never grant update capabilities.
func packageDistribution(executable, platform, version string) string {
	path := filepath.Join(filepath.Dir(executable), "resona-package.json")
	if filepath.Base(filepath.Dir(executable)) == "MacOS" {
		path = filepath.Join(filepath.Dir(filepath.Dir(executable)), "Resources", "resona-package.json")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return ""
	}
	metadata, err := parsePackageMetadata(data, platform, version)
	if err != nil {
		return ""
	}
	return metadata.Distribution
}
