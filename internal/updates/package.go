package updates

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
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

// ValidateWindowsInstaller checks the complete Velopack package before signing
// its digest. Extraction and replacement remain the Velopack updater's job.
func ValidateWindowsInstaller(filename, version string) error {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer archive.Close()
	required := map[string]bool{
		"lib/app/resona-desktop.exe": false, "lib/app/resona_webrtc_apm.dll": false,
		"lib/app/resona-package.json": false,
	}
	names := make(map[string]bool)
	var metadata, spec []byte
	var expanded uint64
	for _, entry := range archive.File {
		name := strings.ToLower(entry.Name)
		clean := strings.TrimSuffix(name, "/")
		if clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") || path.Clean(clean) != clean || names[name] || entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("安装包包含不安全或重复路径")
		}
		names[name] = true
		if entry.UncompressedSize64 > 2<<30-expanded {
			return errors.New("安装包解压大小超出限制")
		}
		expanded += entry.UncompressedSize64
		if _, ok := required[name]; ok {
			required[name] = entry.Mode().IsRegular()
		}
		if name != "lib/app/resona-package.json" && name != "resona.nuspec" {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(file, 64<<10+1))
		file.Close()
		if err != nil || len(data) > 64<<10 {
			return errors.New("安装包元数据无效")
		}
		if name == "resona.nuspec" {
			spec = data
		} else {
			metadata = data
		}
	}
	for _, exists := range required {
		if !exists {
			return errors.New("安装包缺少产品文件")
		}
	}
	marker, err := parsePackageMetadata(metadata, "windows/amd64", version)
	if err != nil {
		return err
	}
	if marker.Distribution != DistributionInstaller {
		return errors.New("安装包必须标记为 installer")
	}
	var nuspec struct {
		XMLName  xml.Name `xml:"package"`
		Metadata struct {
			ID      string `xml:"id"`
			Version string `xml:"version"`
		} `xml:"metadata"`
	}
	if xml.Unmarshal(spec, &nuspec) != nil || nuspec.Metadata.ID != "Resona" || nuspec.Metadata.Version != strings.TrimPrefix(version, "v") {
		return errors.New("安装包的 Velopack 身份或版本不匹配")
	}
	return nil
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
