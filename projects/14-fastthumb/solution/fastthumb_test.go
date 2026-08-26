package fastthumb

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Fixture synthesis — no committed binaries; images are built in-test
// ---------------------------------------------------------------------------

// solidImage is a w x h image of one flat color (with an alpha
// channel, so PNG round-trips through NRGBA semantics).
type solidImage struct {
	w, h int
	c    color.RGBA
}

func (s *solidImage) ColorModel() color.Model { return color.RGBAModel }
func (s *solidImage) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *solidImage) At(x, y int) color.Color { return s.c }

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Format: magic-byte sniffing
// ---------------------------------------------------------------------------

func TestFormat(t *testing.T) {
	pngBytes := encodePNG(t, &solidImage{4, 4, color.RGBA{255, 0, 0, 255}})
	jpegBytes := encodeJPEG(t, &solidImage{4, 4, color.RGBA{0, 255, 0, 255}})

	tests := []struct {
		name    string
		src     []byte
		want    string
		wantErr error // sentinel expected via errors.Is
	}{
		{"png", pngBytes, "png", nil},
		{"jpeg", jpegBytes, "jpeg", nil},
		{"empty", nil, "", ErrBadFormat},
		{"garbage", []byte("not an image at all"), "", ErrBadFormat},
		{"truncated-png-header", pngBytes[:3], "", ErrBadFormat},
		{"gif-magic-rejected", []byte("GIF89a...."), "", ErrBadFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Format(tt.src)
			if got != tt.want {
				t.Errorf("Format() = %q, want %q", got, tt.want)
			}
			if tt.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != nil && !isBadFormat(err) {
				t.Fatalf("error = %v, want wrap of ErrBadFormat", err)
			}
		})
	}
}

func isBadFormat(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrBadFormat.Error()) ||
		err != nil && errorsIs(err, ErrBadFormat)
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ---------------------------------------------------------------------------
// Aspect-fit math (table-driven). fitDims is exercised indirectly via
// Resize+Thumb geometry below; here we pin the pure arithmetic through
// the public Thumb path on synthetic images and check output dims.
// ---------------------------------------------------------------------------

func TestThumbAspectFit(t *testing.T) {
	tests := []struct {
		name         string
		srcW, srcH   int
		boxW, boxH   int
		wantW, wantH int
	}{
		{"wide-into-square", 100, 50, 50, 50, 50, 25},
		{"tall-into-square", 50, 100, 50, 50, 25, 50},
		{"exact-fit", 300, 300, 100, 100, 100, 100},
		{"no-upscale", 200, 100, 400, 400, 200, 100},
		{"no-upscale-small-source", 30, 20, 100, 100, 30, 20},
		{"shrink-width-tall-box", 80, 40, 40, 200, 40, 20},
		{"landscape-box", 400, 200, 100, 50, 100, 50},
		{"portrait-source-landscape-box", 100, 400, 100, 50, 12, 50},
		{"odd-rounding-floor-min1", 101, 51, 50, 50, 50, 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := encodePNG(t, &solidImage{tt.srcW, tt.srcH, color.RGBA{10, 20, 30, 255}})
			out, err := Thumb(src, tt.boxW, tt.boxH)
			if err != nil {
				t.Fatalf("Thumb: %v", err)
			}
			img, err := png.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("output not decodable PNG: %v", err)
			}
			gotW := img.Bounds().Dx()
			gotH := img.Bounds().Dy()
			if gotW != tt.wantW || gotH != tt.wantH {
				t.Errorf("Thumb fit = %dx%d, want %dx%d", gotW, gotH, tt.wantW, tt.wantH)
			}
		})
	}
}

// JPEG in → JPEG out (same format), with correct fitted geometry.
func TestThumbJPEGRoundTripKeepsFormat(t *testing.T) {
	src := encodeJPEG(t, &solidImage{120, 60, color.RGBA{200, 200, 200, 255}})
	out, err := Thumb(src, 60, 60)
	if err != nil {
		t.Fatalf("Thumb: %v", err)
	}
	f, err := Format(out)
	if err != nil {
		t.Fatalf("Format(output): %v", err)
	}
	if f != "jpeg" {
		t.Errorf("output format = %q, want jpeg", f)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}
	if img.Bounds().Dx() != 60 || img.Bounds().Dy() != 30 {
		t.Errorf("dims = %dx%d, want 60x30", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

// Bilinear on a solid-color source must stay that color everywhere.
func TestResizeSolidColorInvariance(t *testing.T) {
	c := color.RGBA{17, 99, 205, 255}
	resized := Resize(&solidImage{64, 32, c}, 13, 7)
	if resized == nil {
		t.Fatal("Resize returned nil")
	}
	b := resized.Bounds()
	if b.Dx() != 13 || b.Dy() != 7 {
		t.Fatalf("dims = %dx%d, want 13x7", b.Dx(), b.Dy())
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := resized.At(x, y).RGBA()
			wantR, wantG, wantB, wantA := uint32(c.R)*257, uint32(c.G)*257, uint32(c.B)*257, uint32(c.A)*257
			if absDiff(r, wantR) > 2 || absDiff(g, wantG) > 2 || absDiff(bl, wantB) > 2 || absDiff(a, wantA) > 2 {
				t.Fatalf("At(%d,%d) = (%d,%d,%d,%d), want ≈(%d,%d,%d,%d)",
					x, y, r>>8, g>>8, bl>>8, a>>8, wantR>>8, wantG>>8, wantB>>8, wantA>>8)
			}
		}
	}
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// ---------------------------------------------------------------------------
// Error paths
// ---------------------------------------------------------------------------

func TestThumbErrors(t *testing.T) {
	pngBytes := encodePNG(t, &solidImage{8, 8, color.RGBA{1, 2, 3, 255}})

	tests := []struct {
		name    string
		src     []byte
		w, h    int
		wantErr error
	}{
		{"zero-w", pngBytes, 0, 10, ErrBadDims},
		{"negative-h", pngBytes, 10, -1, ErrBadDims},
		{"oversized-w", pngBytes, MaxDim + 1, 10, ErrBadDims},
		{"oversized-h", pngBytes, 10, MaxDim * 2, ErrBadDims},
		{"corrupt-input", []byte("\x89PNG\r\n\x1a\n but then garbage"), 10, 10, ErrBadFormat},
		{"nil-input", nil, 10, 10, ErrBadFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Thumb(tt.src, tt.w, tt.h)
			if out != nil {
				t.Errorf("Thumb returned non-nil output on error path")
			}
			if !errorsIs(err, tt.wantErr) {
				t.Errorf("error = %v, want wrap of %v", err, tt.wantErr)
			}
		})
	}
}

// Sentinel identity: callers must be able to errors.Is against the
// exported sentinels.
func TestSentinelIdentity(t *testing.T) {
	if _, err := Format([]byte("junk")); !errorsIs(err, ErrBadFormat) {
		t.Errorf("Format junk: err = %v, want errors.Is(err, ErrBadFormat)", err)
	}
}
