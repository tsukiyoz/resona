// appicon generates themed runtime icons and white default package icons.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"

	"golang.org/x/image/draw"
)

func main() {
	if len(os.Args) == 1 {
		if err := generateThemes(); err != nil {
			panic(err)
		}
		return
	}
	if len(os.Args) != 3 {
		panic("usage: go run ./tools/appicon [source.png output.ico]")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	src, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		panic(err)
	}
	if err := writeICO(src, os.Args[2]); err != nil {
		panic(err)
	}
}

func generateThemes() error {
	f, err := os.Open("desktop/assets/resona-source.png")
	if err != nil {
		return err
	}
	dark, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	light := lightVariant(dark)
	for _, item := range []struct {
		path  string
		image image.Image
	}{
		{"build/appicon.png", light},
		{"desktop/assets/resona-light.png", resize(light, 256)},
		{"desktop/assets/resona-dark.png", resize(dark, 256)},
		{"desktop/assets/resona-macos-light.png", macIcon(light)},
		{"desktop/assets/resona-macos-dark.png", macIcon(dark)},
	} {
		var data bytes.Buffer
		if err := png.Encode(&data, item.image); err != nil {
			return err
		}
		if err := os.WriteFile(item.path, data.Bytes(), 0o644); err != nil {
			return err
		}
	}
	if err := writeICO(light, "desktop/assets/resona.ico"); err != nil {
		return err
	}
	return writeICO(dark, "desktop/assets/resona-dark.ico")
}

func macIcon(src image.Image) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	// Match the optical footprint of macOS rounded-square icons.
	draw.CatmullRom.Scale(dst, image.Rect(100, 100, 924, 924), src, src.Bounds(), draw.Src, nil)
	return dst
}

// Preserve the source silhouette and alpha while mapping its monochrome ramp.
func lightVariant(src image.Image) image.Image {
	b := src.Bounds()
	low, high := uint8(255), uint8(0)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			if c.A != 0 {
				low, high = min(low, c.R), max(high, c.R)
			}
		}
	}
	dst := image.NewNRGBA(b)
	span := max(1, int(high)-int(low))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			t := max(0, int(c.R)-int(low))
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(255 - 231*t/span), G: uint8(255 - 227*t/span),
				B: uint8(255 - 223*t/span), A: c.A,
			})
		}
	}
	return dst
}

func resize(src image.Image, size int) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

func writeICO(src image.Image, path string) error {
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	var images [][]byte
	for _, size := range sizes {
		var b bytes.Buffer
		if err := png.Encode(&b, resize(src, size)); err != nil {
			return err
		}
		images = append(images, b.Bytes())
	}
	data := make([]byte, 6+16*len(sizes))
	binary.LittleEndian.PutUint16(data[2:], 1)
	binary.LittleEndian.PutUint16(data[4:], uint16(len(sizes)))
	offset := len(data)
	for i, size := range sizes {
		entry := data[6+i*16 : 6+(i+1)*16]
		entry[0], entry[1] = byte(size%256), byte(size%256)
		binary.LittleEndian.PutUint16(entry[4:], 1)
		binary.LittleEndian.PutUint16(entry[6:], 32)
		binary.LittleEndian.PutUint32(entry[8:], uint32(len(images[i])))
		binary.LittleEndian.PutUint32(entry[12:], uint32(offset))
		offset += len(images[i])
	}
	for _, blob := range images {
		data = append(data, blob...)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("Generated %s (%d sizes)\n", path, len(sizes))
	return nil
}
