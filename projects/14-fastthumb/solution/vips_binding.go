//go:build vips

// OPTIONAL libvips binding — excluded from the default build.
//
// This file is compiled ONLY when the `vips` build tag is set:
//
//	go build -tags vips ./...
//
// It will NOT compile on a host without libvips C headers installed
// (pkg-config must resolve libvips; see CGO_NOTES.md). That failure
// mode is intentional and isolated: because of the build tag, a
// missing library never breaks the pure-Go default build, tests, or
// cross-compilation.
package fastthumb

// #include <vips/vips.h>
import "C"

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"unsafe"
)

// init brings up libvips once per process. Real bindings would also
// register a shutdown hook (C.vips_shutdown) via runtime.AddCleanup
// or an explicit Close — omitted here for brevity in a teaching stub.
func init() {
	if err := C.vips_init(C.CString("fastthumb")); err != 0 {
		panic(fmt.Sprintf("fastthumb: vips_init failed: %d", int(err)))
	}
}

// thumbVIPS delegates to libvips' thumbnail operation, which does
// decode + shrink-on-load + encode entirely inside libvips: Go only
// hands over one input buffer and receives one output buffer. For big
// JPEGs this beats any pure-Go path because JPEG entropy decoding can
// be skipped for most scanlines during shrink-on-load.
func thumbVIPS(src []byte, w, h int) ([]byte, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("fastthumb(vips): %w", ErrBadFormat)
	}
	// Pointer-passing rule #1: C memory must be allocated/owned by C.
	// C.CBytes copies src into C-allocated memory so libvips may hold
	// onto it beyond this call without aliasing Go's GC'd heap.
	csrc := C.CBytes(src)
	defer C.free(csrc)

	// Pointer-passing rule #2 (the inverse): we may pass a *Go* pointer
	// into a C call ONLY if C does not retain it after returning.
	// Here C writes into our buffer and finishes before the call
	// returns, so &out[0] is legal.
	out := make([]byte, w*h*4)

	var outLen C.size_t
	rc := C.fastthumb_thumbnail((*C.uchar)(csrc), C.size_t(len(src)),
		C.int(w), C.int(h),
		(*C.uchar)(unsafe.Pointer(&out[0])), C.size_t(cap(out)), &outLen)
	if rc != 0 {
		return nil, fmt.Errorf("fastthumb(vips): operation failed (%d)", int(rc))
	}
	runtime.KeepAlive(out) // pin until C is done with it
	return out[:int(outLen)], nil
}

// Resize exists so the vips-tagged package satisfies the same API as
// the pure-Go one; production code would route through vips_resize.
func Resize(src image.Image, w, h int) image.Image {
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		return image.NewRGBA(image.Rect(0, 0, w, h))
	}
	out, err := thumbVIPS(buf.Bytes(), w, h)
	if err != nil {
		return image.NewRGBA(image.Rect(0, 0, w, h))
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		return image.NewRGBA(image.Rect(0, 0, w, h))
	}
	return img
}
