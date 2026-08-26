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
// original dimensions. Dimensions must be in [1, MaxDim].
package fastthumb

import (
	"errors"
	"image"
)

// MaxDim bounds any single requested dimension; larger requests are
// rejected (a thumbnailer should never be handed "shrink to 100000px").
const MaxDim = 8192

// MaxSrcDim bounds any single dimension of a decoded SOURCE image;
// sources declaring larger dimensions must be rejected from their
// headers, before any bitmap allocation (decompression-bomb defense).
const MaxSrcDim = 20000

// ErrBadFormat is returned (wrapped) when src is not decodable as PNG
// or JPEG — corrupt data, truncated headers, or an unsupported format.
var ErrBadFormat = errors.New("fastthumb: unknown or corrupt image format")

// ErrBadDims is returned (wrapped) for non-positive or oversized
// target dimensions.
var ErrBadDims = errors.New("fastthumb: invalid dimensions")

// Format reports the image format of src by its magic bytes:
// "png" or "jpeg". Anything else is an error wrapping ErrBadFormat.
func Format(src []byte) (string, error) {
	// TODO: Implement
	return "", errors.New("fastthumb: not implemented")
}

// Resize scales src to exactly w x h with bilinear interpolation.
// Aspect handling (fit math) is the caller's job — see Thumb.
func Resize(src image.Image, w, h int) image.Image {
	// TODO: Implement
	return nil
}

// Thumb decodes src (PNG or JPEG, detected by magic bytes), resizes it
// to the largest w x h-fitting rectangle that preserves aspect ratio
// (never upscaling), and re-encodes in the SAME format it decoded
// from. Errors from detection wrap ErrBadFormat; bad dimensions wrap
// ErrBadDims.
func Thumb(src []byte, w, h int) ([]byte, error) {
	// TODO: Implement
	return nil, errors.New("fastthumb: not implemented")
}
