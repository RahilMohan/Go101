// Package fastthumb resizes PNG and JPEG images to thumbnail size.
//
// It teaches image decoding via magic-byte sniffing, the geometry of
// aspect-preserving fit, hand-rolled bilinear interpolation (and why
// golang.org/x/image/draw exists), and the cgo boundary: an OPTIONAL
// libvips binding behind a `vips` build tag shows where a C imaging
// library pays off and what crossing the boundary costs (call
// overhead, pointer-passing rules, cross-compilation pain).
//
// # API
//
//	Format(src []byte) (string, error)   // "png" | "jpeg" by magic bytes
//	Thumb(src []byte, w, h int) ([]byte, error) // decode → aspect-fit → encode
//	Resize(img image.Image, w, h int) image.Image // exact-size bilinear scale
//
// Thumb never upscales: sources smaller than the box keep their
// original dimensions.
package fastthumb

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

// MaxDim bounds any single requested dimension; larger requests are
// rejected (a thumbnailer should never be handed "shrink to 100000px").
const MaxDim = 8192

// MaxSrcDim bounds any single dimension of a DECODED SOURCE image.
// Sources whose headers declare larger dimensions are rejected at the
// header-peek stage, BEFORE any bitmap allocation — Go's PNG decoder
// sizes its buffers from the declared header, so an unbounded decode
// turns a few hundred attacker-chosen bytes into gigabytes of RAM
// (a decompression bomb).
const MaxSrcDim = 20000

// maxSrcPixels caps the total declared source pixel count (~256 MiB
// of RGBA), catching wide-but-short or tall-but-narrow bombs that fit
// under MaxSrcDim on each side individually.
const maxSrcPixels = 1 << 26

// ErrBadFormat is returned (wrapped) when src is not decodable as PNG
// or JPEG — corrupt data, truncated headers, or an unsupported format.
var ErrBadFormat = errors.New("fastthumb: unknown or corrupt image format")

// ErrBadDims is returned (wrapped) for non-positive or oversized
// target dimensions, or a decoded source exceeding the
// MaxSrcDim/maxSrcPixels budget.
var ErrBadDims = errors.New("fastthumb: invalid dimensions")

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// Format reports the image format of src by its magic bytes:
// "png" or "jpeg". Anything else is an error wrapping ErrBadFormat.
func Format(src []byte) (string, error) {
	switch {
	case len(src) >= len(pngMagic) && bytes.Equal(src[:len(pngMagic)], pngMagic):
		return "png", nil
	case len(src) >= 3 && src[0] == 0xFF && src[1] == 0xD8 && src[2] == 0xFF:
		return "jpeg", nil
	default:
		return "", fmt.Errorf("fastthumb: %w (%d bytes, no PNG/JPEG magic)", ErrBadFormat, len(src))
	}
}

// rgba16 converts any color to premultiplied 16-bit channels — the
// common denominator image.Color.RGBA already gives us.
func rgba16(c color.Color) [4]uint32 {
	r, g, b, a := c.RGBA()
	return [4]uint32{r, g, b, a}
}

// straight16 converts a premultiplied 16-bit channel value back to
// straight alpha; a fully transparent pixel contributes no color.
func straight16(premul, a uint32) uint32 {
	if a == 0 {
		return 0
	}
	v := premul * 65535 / a
	if v > 65535 {
		return 65535
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// fitDims returns the largest w x h-preserving rectangle inside the
// box maxW x maxH, never upscaling (a smaller source keeps its size).
// Integer cross-multiplication avoids float precision surprises.
func fitDims(w, h, maxW, maxH int) (int, int) {
	if w <= maxW && h <= maxH {
		return w, h
	}
	// Compare scales maxW/w vs maxH/h without division: if h*maxW <= w*maxH,
	// the width constraint is tighter (or equal).
	if int64(h)*int64(maxW) <= int64(w)*int64(maxH) {
		nh := maxInt(1, h*maxW/w)
		return maxInt(1, maxW), nh
	}
	nw := maxInt(1, w*maxH/h)
	return nw, maxInt(1, maxH)
}

// Resize scales src to exactly w x h with bilinear interpolation,
// sampling source pixel centers so output pixels map symmetrically to
// source space. Output is *image.NRGBA regardless of src's color
// model: sampling happens in premultiplied space, and the result is
// un-premultiplied once, into straight-alpha storage — writing into
// *image.RGBA instead would reinterpret the same bytes as
// premultiplied again and darken translucent sources.
func Resize(src image.Image, w, h int) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	b := src.Bounds()
	sw := b.Dx()
	sh := b.Dy()
	sx := float64(sw) / float64(w)
	sy := float64(sh) / float64(h)

	for y := 0; y < h; y++ {
		fy := (float64(y)+0.5)*sy - 0.5
		fy = maxFloat(fy, 0)
		y0 := minInt(int(fy), sh-1)
		y1 := minInt(y0+1, sh-1)
		tY := fy - float64(y0)

		for x := 0; x < w; x++ {
			fx := (float64(x)+0.5)*sx - 0.5
			fx = maxFloat(fx, 0)
			x0 := minInt(int(fx), sw-1)
			x1 := minInt(x0+1, sw-1)
			tX := fx - float64(x0)

			c00 := rgba16(src.At(b.Min.X+x0, b.Min.Y+y0))
			c10 := rgba16(src.At(b.Min.X+x1, b.Min.Y+y0))
			c01 := rgba16(src.At(b.Min.X+x0, b.Min.Y+y1))
			c11 := rgba16(src.At(b.Min.X+x1, b.Min.Y+y1))

			// Interpolate in premultiplied space (correct for alpha
			// compositing — translucent edges don't bleed color).
			topR := lerp16(c00[0], c10[0], tX)
			topG := lerp16(c00[1], c10[1], tX)
			topB := lerp16(c00[2], c10[2], tX)
			topA := lerp16(c00[3], c10[3], tX)
			botR := lerp16(c01[0], c11[0], tX)
			botG := lerp16(c01[1], c11[1], tX)
			botB := lerp16(c01[2], c11[2], tX)
			botA := lerp16(c01[3], c11[3], tX)
			a16 := lerp16(topA, botA, tY)

			// Un-premultiply exactly once, into straight-alpha
			// storage; storing the premultiplied values as-is
			// darkened every translucent source toward black.
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(straight16(lerp16(topR, botR, tY), a16) >> 8),
				G: uint8(straight16(lerp16(topG, botG, tY), a16) >> 8),
				B: uint8(straight16(lerp16(topB, botB, tY), a16) >> 8),
				A: uint8(a16 >> 8),
			})
		}
	}
	return dst
}

func lerp16(a, b uint32, t float64) uint32 {
	v := float64(a)*(1-t) + float64(b)*t + 0.5
	if v >= 65536 {
		return 65535
	}
	return uint32(v)
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// decode sniffs the format and decodes accordingly; the returned name
// is the format to re-encode as. Before any full decode, the header
// dimensions are peeked via image.DecodeConfig and checked against
// MaxSrcDim/maxSrcPixels — a decoder would otherwise allocate the full
// bitmap straight from attacker-controlled header values.
func decode(src []byte) (image.Image, string, error) {
	format, err := Format(src)
	if err != nil {
		return nil, "", err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return nil, "", fmt.Errorf("fastthumb: %w: %v", ErrBadFormat, err)
	}
	if cfg.Width > MaxSrcDim || cfg.Height > MaxSrcDim ||
		int64(cfg.Width)*int64(cfg.Height) > maxSrcPixels {
		return nil, "", fmt.Errorf(
			"fastthumb: source %dx%d exceeds budget (%d per side, %d pixels): %w",
			cfg.Width, cfg.Height, MaxSrcDim, maxSrcPixels, ErrBadDims)
	}
	r := bytes.NewReader(src)
	var img image.Image
	switch format {
	case "png":
		img, err = png.Decode(r)
	case "jpeg":
		img, err = jpeg.Decode(r)
	}
	if err != nil {
		return nil, "", fmt.Errorf("fastthumb: %w: %v", ErrBadFormat, err)
	}
	return img, format, nil
}

// Thumb decodes src (PNG or JPEG, detected by magic bytes), resizes it
// to the largest box-fitting rectangle that preserves aspect ratio
// (never upscaling), and re-encodes in the SAME format it decoded
// from. Errors from detection wrap ErrBadFormat; bad dimensions wrap
// ErrBadDims.
func Thumb(src []byte, w, h int) ([]byte, error) {
	if w <= 0 || h <= 0 || w > MaxDim || h > MaxDim {
		return nil, fmt.Errorf("fastthumb: dims %dx%d outside [1,%d]: %w", w, h, MaxDim, ErrBadDims)
	}
	img, format, err := decode(src)
	if err != nil {
		return nil, err
	}
	fw, fh := fitDims(img.Bounds().Dx(), img.Bounds().Dy(), w, h)

	var out bytes.Buffer
	switch format {
	case "png":
		err = png.Encode(&out, Resize(img, fw, fh))
	case "jpeg":
		err = jpeg.Encode(&out, Resize(img, fw, fh), &jpeg.Options{Quality: 85})
	}
	if err != nil {
		return nil, fmt.Errorf("fastthumb: encoding %s: %w", format, err)
	}
	return out.Bytes(), nil
}
