// Package raster turns an explicitly authorized managed image into a bounded,
// metadata-free provider payload. It never preserves the source filename or
// original encoding bytes.
package raster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	MaximumInputBytes  = 10 << 20
	MaximumOutputBytes = 4 << 20
	MaximumPixels      = 40_000_000
	MaximumDimension   = 2048
)

var (
	ErrUnsupportedImage = errors.New("unsupported raster image")
	ErrImageTooLarge    = errors.New("raster image exceeds its safety bound")
	ErrInvalidImage     = errors.New("raster image is invalid")
)

type Result struct {
	Data             []byte
	MIMEType         string
	Width, Height    int
	OriginalMIMEType string
}

type Processor struct{ slots chan struct{} }

func New(maximumConcurrent int) *Processor {
	if maximumConcurrent < 1 {
		maximumConcurrent = 1
	}
	if maximumConcurrent > 4 {
		maximumConcurrent = 4
	}
	return &Processor{slots: make(chan struct{}, maximumConcurrent)}
}

func (processor *Processor) Process(ctx context.Context, reader io.Reader) (Result, error) {
	if reader == nil {
		return Result{}, ErrInvalidImage
	}
	select {
	case processor.slots <- struct{}{}:
		defer func() { <-processor.slots }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaximumInputBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("read raster image: %w", err)
	}
	if len(raw) == 0 {
		return Result{}, ErrInvalidImage
	}
	if len(raw) > MaximumInputBytes {
		return Result{}, ErrImageTooLarge
	}
	mimeType := http.DetectContentType(raw)
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return Result{}, ErrUnsupportedImage
	}
	configuration, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || configuration.Width <= 0 || configuration.Height <= 0 {
		return Result{}, ErrInvalidImage
	}
	if int64(configuration.Width)*int64(configuration.Height) > MaximumPixels {
		return Result{}, ErrImageTooLarge
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Result{}, ErrInvalidImage
	}
	width, height := configuration.Width, configuration.Height
	if width > MaximumDimension || height > MaximumDimension {
		ratio := float64(MaximumDimension) / float64(max(width, height))
		width, height = max(1, int(float64(width)*ratio)), max(1, int(float64(height)*ratio))
	}
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(destination, destination.Bounds(), decoded, decoded.Bounds(), xdraw.Over, nil)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var encoded bytes.Buffer
	for _, quality := range []int{85, 72, 60} {
		encoded.Reset()
		if err := jpeg.Encode(&encoded, destination, &jpeg.Options{Quality: quality}); err != nil {
			return Result{}, fmt.Errorf("encode safe raster image: %w", err)
		}
		if encoded.Len() <= MaximumOutputBytes {
			data := append([]byte(nil), encoded.Bytes()...)
			return Result{Data: data, MIMEType: "image/jpeg", Width: width, Height: height, OriginalMIMEType: mimeType}, nil
		}
	}
	return Result{}, ErrImageTooLarge
}
