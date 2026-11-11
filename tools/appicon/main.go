// appicon generates themed runtime icons and white default package icons.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strconv"

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
	lightSVG, err := os.ReadFile("desktop/assets/resona.svg")
	if err != nil {
		return err
	}
	darkSVG, err := darkVariant(lightSVG)
	if err != nil {
		return err
	}
	light, err := rasterize(lightSVG, 1024)
	if err != nil {
		return err
	}
	dark, err := rasterize(darkSVG, 1024)
	if err != nil {
		return err
	}
	if err := os.WriteFile("desktop/assets/resona-dark.svg", darkSVG, 0o644); err != nil {
		return err
	}
	for _, item := range []struct {
		path  string
		image image.Image
	}{
		{"desktop/assets/appicon.png", light},
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

// Change only the palette; both themes share the same vector geometry.
func darkVariant(source []byte) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(source))
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			// The source already declares its default SVG namespace.
			element.Name.Space = ""
			for i := range element.Attr {
				attr := &element.Attr[i]
				if attr.Name.Local == "fill" {
					switch attr.Value {
					case "#ffffff":
						attr.Value = "#111315"
					case "#181c20":
						attr.Value = "#eceff1"
					}
				}
			}
			token = element
		case xml.EndElement:
			element.Name.Space = ""
			token = element
		}
		if err := encoder.EncodeToken(token); err != nil {
			return nil, err
		}
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func rasterize(source []byte, size int) (image.Image, error) {
	cmd := exec.Command("rsvg-convert", "--width", strconv.Itoa(size), "--height", strconv.Itoa(size))
	cmd.Stdin = bytes.NewReader(source)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("render SVG (install librsvg for rsvg-convert): %w: %s", err, stderr.String())
	}
	return png.Decode(bytes.NewReader(data))
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
