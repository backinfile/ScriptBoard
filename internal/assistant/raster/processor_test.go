package raster

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestProcessorReencodesAndBoundsRasterImages(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 3000, 1200))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 90, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	result, err := New(2).Process(context.Background(), bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if result.MIMEType != "image/jpeg" || len(result.Data) == 0 || len(result.Data) > MaximumOutputBytes {
		t.Fatalf("result = mime %q bytes %d", result.MIMEType, len(result.Data))
	}
	decoded, err := jpeg.Decode(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 2048 || decoded.Bounds().Dy() != 819 {
		t.Fatalf("bounds = %v", decoded.Bounds())
	}
}

func TestProcessorRejectsUnsupportedAndOversizedPixelClaims(t *testing.T) {
	processor := New(1)
	if _, err := processor.Process(context.Background(), bytes.NewReader([]byte("GIF89a"))); err != ErrUnsupportedImage {
		t.Fatalf("GIF error = %v", err)
	}
	if _, err := processor.Process(context.Background(), bytes.NewReader(pngHeader(7000, 6000))); err != ErrImageTooLarge {
		t.Fatalf("pixel bomb error = %v", err)
	}
}

func pngHeader(width, height uint32) []byte {
	result := append([]byte(nil), []byte("\x89PNG\r\n\x1a\n")...)
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8], data[9], data[10], data[11], data[12] = 8, 2, 0, 0, 0
	chunk := append([]byte("IHDR"), data...)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))
	result = append(result, length...)
	result = append(result, chunk...)
	checksum := make([]byte, 4)
	binary.BigEndian.PutUint32(checksum, crc32.ChecksumIEEE(chunk))
	return append(result, checksum...)
}
