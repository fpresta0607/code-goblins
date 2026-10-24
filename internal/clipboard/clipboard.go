// Package clipboard puts a picture on this PC's clipboard, where a terminal's
// harness reads a pasted image from when its image key is pressed.
package clipboard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
)

// ErrNotPNG is a picture that is not a PNG this package can read.
var ErrNotPNG = errors.New("the picture is not a PNG")

// SetImage puts a PNG picture on this PC's clipboard twice over: as a bitmap,
// which every Windows program reads a pasted image as, and as the PNG itself,
// which a program that reads PNG takes with its transparency. It replaces
// what the clipboard held.
func SetImage(picture []byte) error {
	decoded, err := png.Decode(bytes.NewReader(picture))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotPNG, err)
	}
	return set(bitmap(decoded), picture)
}

// bitmap is a picture as the clipboard's device-independent bitmap, CF_DIB: a
// BITMAPINFOHEADER, then its rows from the bottom up, each pixel's blue,
// green, red and alpha in turn.
func bitmap(picture image.Image) []byte {
	bounds := picture.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	pixels := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(pixels, pixels.Rect, picture, bounds.Min, draw.Src)
	data := make([]byte, 40+width*height*4)
	binary.LittleEndian.PutUint32(data[0:], 40)
	binary.LittleEndian.PutUint32(data[4:], uint32(width))
	binary.LittleEndian.PutUint32(data[8:], uint32(height))
	binary.LittleEndian.PutUint16(data[12:], 1)
	binary.LittleEndian.PutUint16(data[14:], 32)
	binary.LittleEndian.PutUint32(data[20:], uint32(width*height*4))
	at := 40
	for y := height - 1; y >= 0; y-- {
		row := pixels.Pix[y*pixels.Stride : y*pixels.Stride+width*4]
		for x := 0; x < len(row); x += 4 {
			data[at], data[at+1], data[at+2], data[at+3] = row[x+2], row[x+1], row[x], row[x+3]
			at += 4
		}
	}
	return data
}
