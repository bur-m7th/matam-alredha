package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func square(side int) image.Image {
	m := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			m.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 120, 255})
		}
	}
	return m
}

func rect(w, h int) image.Image {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, color.RGBA{200, 100, 50, 255})
		}
	}
	return m
}

func asPNG(t *testing.T, m image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func asJPEG(t *testing.T, m image.Image, q int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, m, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decodeSide(t *testing.T, data []byte) int {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output is not decodable: %v", err)
	}
	if cfg.Width != cfg.Height {
		t.Fatalf("output not square: %dx%d", cfg.Width, cfg.Height)
	}
	return cfg.Width
}

func TestAcceptsExactMinimum(t *testing.T) {
	res, err := Process(asPNG(t, square(512)))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSide(t, res.Display); got != 512 {
		t.Fatalf("display side = %d, want 512", got)
	}
	if got := decodeSide(t, res.Thumb); got != ThumbSide {
		t.Fatalf("thumb side = %d, want %d", got, ThumbSide)
	}
}

func TestRejectsBelowMinimum(t *testing.T) {
	if _, err := Process(asPNG(t, square(511))); err != ErrTooSmall {
		t.Fatalf("got %v, want ErrTooSmall", err)
	}
}

func TestRejectsNonSquare(t *testing.T) {
	if _, err := Process(asPNG(t, rect(1000, 800))); err != ErrNotSquare {
		t.Fatalf("got %v, want ErrNotSquare", err)
	}
}

func TestDownscalesOversizeTo1080(t *testing.T) {
	res, err := Process(asJPEG(t, square(2400), 95))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSide(t, res.Display); got != MaxSide {
		t.Fatalf("display side = %d, want %d", got, MaxSide)
	}
}

func TestLeavesUndersizedAlone(t *testing.T) {
	res, err := Process(asPNG(t, square(800)))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSide(t, res.Display); got != 800 {
		t.Fatalf("an 800px photo should stay 800px, got %d", got)
	}
}

func TestRejectsOversizeFile(t *testing.T) {
	big := make([]byte, MaxUploadBytes+1)
	if _, err := Process(big); err != ErrTooLarge {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestRejectsNonImage(t *testing.T) {
	if _, err := Process([]byte("this is definitely not a photograph")); err != ErrNotImage {
		t.Fatalf("got %v, want ErrNotImage", err)
	}
}

// A disguised file must not slip through on its extension alone; the decoder
// is what decides.
func TestRejectsDisguisedFile(t *testing.T) {
	fake := append([]byte("\xff\xd8\xff"), []byte("not really a jpeg body")...)
	if _, err := Process(fake); err != ErrNotImage {
		t.Fatalf("got %v, want ErrNotImage", err)
	}
}

// The whole point of the thumbnail is that the ballot grid downloads far less.
func TestThumbIsSubstantiallySmaller(t *testing.T) {
	res, err := Process(asPNG(t, square(1600)))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Thumb) >= len(res.Display) {
		t.Fatalf("thumb %d bytes is not smaller than display %d bytes", len(res.Thumb), len(res.Display))
	}
	if len(res.Thumb) > 60<<10 {
		t.Fatalf("thumb is %d bytes, too heavy for a grid of them", len(res.Thumb))
	}
}

// A 2MB upload has to come out small enough to serve quickly.
func TestOutputIsCompressed(t *testing.T) {
	res, err := Process(asPNG(t, square(1600)))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Display) > 400<<10 {
		t.Fatalf("display copy is %d bytes, expected well under 400KB", len(res.Display))
	}
}

// A transparent PNG must not gain black edges when written as JPEG.
func TestTransparentPNGGetsWhiteBackground(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 600, 600))
	// fully transparent
	res, err := Process(asPNG(t, m))
	if err != nil {
		t.Fatal(err)
	}
	out, err := jpeg.Decode(bytes.NewReader(res.Display))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := out.At(300, 300).RGBA()
	if r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Fatalf("transparent area rendered dark: %d %d %d", r>>8, g>>8, b>>8)
	}
}

func TestNearlySquareIsTolerated(t *testing.T) {
	// 1000x1004 is within tolerance and is trimmed to a centred square.
	res, err := Process(asPNG(t, rect(1000, 1004)))
	if err != nil {
		t.Fatalf("a nearly-square photo should be accepted, got %v", err)
	}
	decodeSide(t, res.Display)
}
