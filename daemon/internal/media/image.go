package media

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"runtime/debug"

	// Decoders registered with package image.
	_ "image/gif"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// maxDecodePixels refuses decoding images larger than this (50 MP).
	maxDecodePixels = 50_000_000
	// bigDecodePixels: after decoding something this large, hand the
	// memory back to the OS right away (keeps idle RSS low).
	bigDecodePixels = 8_000_000
)

var errTooLarge = errors.New("image too large to decode")

// decodeLimited decodes an image after checking its dimensions with
// DecodeConfig. r must be re-readable from the start via the seek func.
func decodeLimited(r io.ReadSeeker) (image.Image, string, error) {
	cfg, format, err := image.DecodeConfig(bufio.NewReader(r))
	if err != nil {
		return nil, "", err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", errors.New("image has no pixels")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return nil, "", fmt.Errorf("%w: %dx%d", errTooLarge, cfg.Width, cfg.Height)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	img, format, err := image.Decode(bufio.NewReader(r))
	if err != nil {
		return nil, "", err
	}
	return img, format, nil
}

// fitSize returns w×h scaled to fit within max×max (never upscaled).
func fitSize(w, h, max int) (int, int) {
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		nh := h * max / w
		if nh < 1 {
			nh = 1
		}
		return max, nh
	}
	nw := w * max / h
	if nw < 1 {
		nw = 1
	}
	return nw, max
}

// downscale scales img to fit max×max onto an opaque white background
// (JPEG has no alpha). Images already small enough are only flattened.
func downscale(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := fitSize(b.Dx(), b.Dy(), max)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	} else {
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	}
	return dst
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// thumbJPEG decodes an image from r (size-checked), scales it to fit
// max×max and encodes it as JPEG. Only one decoded image is alive at a time.
func thumbJPEG(r io.ReadSeeker, max, quality int) ([]byte, image.Config, error) {
	img, _, err := decodeLimited(r)
	if err != nil {
		return nil, image.Config{}, err
	}
	b := img.Bounds()
	cfg := image.Config{Width: b.Dx(), Height: b.Dy()}
	big := cfg.Width*cfg.Height > bigDecodePixels
	small := downscale(img, max)
	if big {
		debug.FreeOSMemory()
	}
	data, err := encodeJPEG(small, quality)
	return data, cfg, err
}

// thumbFromFile makes a JPEG thumbnail ≤ max px from an image file.
func thumbFromFile(path string, max, quality int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, _, err := thumbJPEG(f, max, quality)
	return data, err
}
