// appicon generates the Windows multi-resolution icon from the shared app PNG.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"

	"golang.org/x/image/draw"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: go run ./tools/appicon build/appicon.png desktop/assets/resona.ico")
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
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	var images [][]byte
	for _, size := range sizes {
		dst := image.NewNRGBA(image.Rect(0, 0, size, size))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		var b bytes.Buffer
		if err = png.Encode(&b, dst); err != nil {
			panic(err)
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
	if err = os.WriteFile(os.Args[2], data, 0644); err != nil {
		panic(err)
	}
	fmt.Printf("Generated %s (%d sizes)\n", os.Args[2], len(sizes))
}
