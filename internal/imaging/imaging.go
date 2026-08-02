// Package imaging validates and normalises candidate photographs.
//
// Every accepted photo leaves this package as two baseline JPEGs: a display
// copy of at most 1080x1080 and a 320x320 thumbnail. The thumbnail is what the
// ballot grid loads, which is the single biggest factor in how quickly that
// page becomes usable on a phone over mobile data.
//
// A note on the output format. The brief asked for something that loads fast at
// lossless quality; those two pull against each other. WebP would be the usual
// answer, but Go's standard library can only decode it, not encode it, and the
// x/image package is the same. Rather than add a cgo dependency on libwebp,
// which would complicate the container build, photos are written as JPEG at
// quality 92 after a Catmull-Rom resample. That is visually indistinguishable
// from the original at these dimensions while being a fraction of the bytes of
// a lossless PNG.
package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder
	"math"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // lets a member upload a WebP straight from a phone
)

const (
	// MaxUploadBytes is the largest file accepted from the browser.
	MaxUploadBytes = 2 << 20

	// MinSide is the smallest acceptable edge; below this the photo looks soft
	// once it is displayed on a high-density screen.
	MinSide = 512

	// MaxSide is the largest stored edge. Anything bigger is scaled down.
	MaxSide = 1080

	// ThumbSide is the ballot grid size.
	ThumbSide = 320

	displayQuality = 92
	thumbQuality   = 82

	// squareTolerance allows a pixel or two of rounding in an otherwise square
	// crop, which phone editors frequently produce.
	squareTolerance = 0.01
)

var (
	ErrTooLarge   = fmt.Errorf("حجم الصورة يتجاوز الحد المسموح وهو %d ميغابايت", MaxUploadBytes>>20)
	ErrNotImage   = errors.New("الملف ليس صورة صالحة. الصيغ المقبولة: JPG أو PNG أو WEBP")
	ErrNotSquare  = errors.New("يجب أن تكون الصورة مربعة الأبعاد بنسبة 1:1")
	ErrTooSmall   = fmt.Errorf("أبعاد الصورة صغيرة. الحد الأدنى %[1]dx%[1]d بكسل", MinSide)
	ErrEncodeFail = errors.New("تعذرت معالجة الصورة")
)

// Result carries the two encoded copies plus the dimensions actually stored.
type Result struct {
	Display []byte
	Thumb   []byte
	Side    int
}

// Process validates the upload and returns the normalised copies.
func Process(data []byte) (Result, error) {
	if len(data) == 0 {
		return Result{}, ErrNotImage
	}
	if len(data) > MaxUploadBytes {
		return Result{}, ErrTooLarge
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, ErrNotImage
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return Result{}, ErrNotImage
	}

	// 1:1 check, with a small tolerance for rounding.
	ratio := float64(w) / float64(h)
	if math.Abs(ratio-1) > squareTolerance {
		return Result{}, ErrNotSquare
	}
	if w < MinSide || h < MinSide {
		return Result{}, ErrTooSmall
	}

	// A square that is off by a pixel is trimmed to the shorter edge so every
	// stored photo is exactly square.
	side := w
	if h < side {
		side = h
	}
	if side > MaxSide {
		side = MaxSide
	}

	display, err := encode(src, side, displayQuality)
	if err != nil {
		return Result{}, err
	}
	thumb, err := encode(src, ThumbSide, thumbQuality)
	if err != nil {
		return Result{}, err
	}
	return Result{Display: display, Thumb: thumb, Side: side}, nil
}

// encode resamples the source to a square of the given side and writes a JPEG.
func encode(src image.Image, side, quality int) ([]byte, error) {
	dst := image.NewRGBA(image.Rect(0, 0, side, side))

	// JPEG has no alpha channel, so a transparent PNG would otherwise render
	// with black edges. Fill white first.
	draw.Draw(dst, dst.Bounds(), image.NewUniform(image.White), image.Point{}, draw.Src)

	// Take the largest centred square of the source, then scale it. Catmull-Rom
	// keeps portraits sharp without the ringing that Lanczos can introduce.
	b := src.Bounds()
	edge := b.Dx()
	if b.Dy() < edge {
		edge = b.Dy()
	}
	offX := b.Min.X + (b.Dx()-edge)/2
	offY := b.Min.Y + (b.Dy()-edge)/2
	crop := image.Rect(offX, offY, offX+edge, offY+edge)

	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, ErrEncodeFail
	}
	return buf.Bytes(), nil
}
