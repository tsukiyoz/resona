package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVectorThemes(t *testing.T) {
	source, err := os.ReadFile("../../desktop/assets/resona.svg")
	if err != nil {
		t.Fatal(err)
	}
	dark, err := darkVariant(source)
	if err != nil {
		t.Fatal(err)
	}
	checkedIn, err := os.ReadFile("../../desktop/assets/resona-dark.svg")
	if err != nil || !bytes.Equal(checkedIn, dark) {
		t.Fatalf("dark SVG export is stale; regenerate icons: %v", err)
	}
	if _, err := darkVariant([]byte("<svg><rect></svg>")); err == nil {
		t.Fatal("invalid SVG accepted")
	}
	if _, err := exec.LookPath("rsvg-convert"); err != nil {
		t.Skip("SVG raster checks require librsvg; ordinary builds use checked-in exports")
	}
	lightImage, err := rasterize(source, 256)
	if err != nil {
		t.Fatal(err)
	}
	darkImage, err := rasterize(dark, 256)
	if err != nil {
		t.Fatal(err)
	}
	if lightImage.Bounds() != image.Rect(0, 0, 256, 256) || darkImage.Bounds() != lightImage.Bounds() {
		t.Fatal("unexpected raster dimensions")
	}
	for _, test := range []struct {
		point image.Point
		light color.NRGBA
		dark  color.NRGBA
	}{
		{image.Pt(0, 0), color.NRGBA{}, color.NRGBA{}},
		{image.Pt(128, 128), color.NRGBA{255, 255, 255, 255}, color.NRGBA{17, 19, 21, 255}},
		{image.Pt(109, 128), color.NRGBA{24, 28, 32, 255}, color.NRGBA{236, 239, 241, 255}},
	} {
		if color.NRGBAModel.Convert(lightImage.At(test.point.X, test.point.Y)) != test.light ||
			color.NRGBAModel.Convert(darkImage.At(test.point.X, test.point.Y)) != test.dark {
			t.Errorf("incorrect theme color at %v", test.point)
		}
	}
	for y := range 256 {
		for x := range 256 {
			_, _, _, a := lightImage.At(x, y).RGBA()
			_, _, _, b := darkImage.At(x, y).RGBA()
			if a != b {
				t.Fatalf("theme silhouettes differ at %d,%d", x, y)
			}
		}
	}
	mac := macIcon(lightImage)
	_, _, _, outside := mac.At(99, 512).RGBA()
	_, _, _, inside := mac.At(100, 512).RGBA()
	if outside != 0 || inside != 65535 {
		t.Fatal("macOS optical padding changed")
	}
}

func TestICOFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resona.ico")
	if err := writeICO(image.NewNRGBA(image.Rect(0, 0, 256, 256)), path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	if binary.LittleEndian.Uint16(data[2:]) != 1 || int(binary.LittleEndian.Uint16(data[4:])) != len(sizes) {
		t.Fatal("invalid ICO header")
	}
	for i, size := range sizes {
		entry := data[6+i*16 : 6+(i+1)*16]
		offset := binary.LittleEndian.Uint32(entry[12:])
		length := binary.LittleEndian.Uint32(entry[8:])
		frame, err := png.Decode(bytes.NewReader(data[offset : offset+length]))
		if err != nil || frame.Bounds() != image.Rect(0, 0, size, size) {
			t.Fatalf("invalid %dpx ICO frame: %v", size, err)
		}
	}
}
