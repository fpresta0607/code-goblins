package clipboard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// twoByTwo is a picture with a colour of its own in each corner, the bottom
// right one half transparent.
func twoByTwo(t *testing.T) (image.Image, []byte) {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	picture.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	picture.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	picture.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 255})
	picture.SetNRGBA(1, 1, color.NRGBA{R: 10, G: 20, B: 30, A: 128})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return picture, encoded.Bytes()
}

func TestBitmapIsADeviceIndependentBitmapFromTheBottomRowUp(t *testing.T) {
	// Arrange
	picture, _ := twoByTwo(t)

	// Act
	data := bitmap(picture)

	// Assert
	header := struct {
		Size                  uint32
		Width, Height         int32
		Planes, BitsPerPixel  uint16
		Compression, ImageLen uint32
	}{}
	if err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &header); err != nil {
		t.Fatal(err)
	}
	if header.Size != 40 || header.Width != 2 || header.Height != 2 || header.Planes != 1 || header.BitsPerPixel != 32 || header.Compression != 0 || header.ImageLen != 16 {
		t.Fatalf("header = %+v, want a 40-byte header of a 2 by 2, 32-bit, uncompressed bitmap of 16 bytes", header)
	}
	want := []byte{
		255, 0, 0, 255, 30, 20, 10, 128, // the bottom row: blue, then the half transparent pixel
		0, 0, 255, 255, 0, 255, 0, 255, // the top row: red, then green
	}
	if got := data[40:]; !bytes.Equal(got, want) {
		t.Fatalf("pixels = %v, want %v", got, want)
	}
}

func TestSetImageRefusesWhatIsNotAPNG(t *testing.T) {
	for name, picture := range map[string][]byte{
		"nothing":   nil,
		"a JPEG":    {0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F'},
		"some text": []byte("not a picture"),
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			err := SetImage(picture)

			// Assert
			if !errors.Is(err, ErrNotPNG) {
				t.Fatalf("SetImage = %v, want ErrNotPNG", err)
			}
		})
	}
}
