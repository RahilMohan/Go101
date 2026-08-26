# CGO Notes — Project 14 (fastthumb)

Why a thumbnailer might want libvips, what crossing the cgo boundary
costs, and how the `vips` build tag keeps that optional.

## Why libvips at all?

libvips decodes, shrinks, and encodes without materializing full
decompressed bitmaps in Go. For large JPEGs its shrink-on-load skips
entropy-decoding most scanlines entirely — often 5–20x faster than
decode-then-resize-in-Go, with lower peak memory. The pure-Go path in
this project is fine for small/medium images; the C path exists for
when it isn't. That trade (portability vs throughput) IS the lesson.

## Cgo call overhead

Every Go→C call costs on the order of ~50–200 ns: the runtime must
transition off the Go scheduler's stack management, save registers,
and switch to C ABI (cgo trampoline). Per-call overhead is negligible
for operations like "thumbnail this 4 MB JPEG" (milliseconds of work)
and ruinous for per-pixel calls (never call C per pixel from Go —
batch into whole-buffer operations). The binding here follows that
rule: ONE call per image, buffers in, buffer out.

## Pointer-passing rules

The rules that keep cgo memory-safe with the garbage collector:

1. **Go memory passed to C**: legal only if the pointer contains no Go
   pointers itself AND C does not retain it after the call returns.
   The GC may move/reclaim Go memory; C has no visibility into that.
2. **C memory held by Go**: allocate with `C.CBytes` / `C.malloc`,
   free with `defer C.free(...)` or leak deliberately for objects C
   owns. Never hand C a pointer into Go-managed memory you expect to
   outlive the call.
3. **No Go pointers inside C structs** unless documented otherwise;
   use pinned/persistent handles instead.
4. `runtime.KeepAlive(x)` pins `x` against collection until after the
   final C use when the compiler cannot prove liveness (e.g. the only
   reference was converted via unsafe.Pointer).

In `vips_binding.go`: input bytes are COPIED into C memory (`C.CBytes`)
because libvips may retain them internally; the output slice is passed
in because C finishes writing before the call returns.

## Build tags

`vips_binding.go` starts with `//go:build vips`. Consequences:

- Default builds (`go build ./...`, `go test ./...`) never compile it —
  no libvips? No problem. CI stays green without system dependencies.
- Opt in explicitly: `go build -tags vips ./...`.
- Tagged and untagged files implement the same package API, so tests
  exercise whichever implementation is active.

## Cross-compilation

Pure Go cross-compiles trivially (`GOOS=windows GOARCH=amd64 go build`
— no toolchain needed). cgo breaks that:

- You need a C cross-toolchain for every target:
  `CC_FOR_TARGET=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64
  CGO_ENABLED=1 go build -tags vips ./...`
- macOS targets need an OS X SDK + `CC_FOR_TARGET` pointing at an
  osxcross wrapper; Linux→Linux needs matching sysroots.
- Static linking quirks multiply (`--ldflags '-extldflags -static'`,
  musl vs glibc).

This asymmetry is why the default build is pure Go and the vips path
is opt-in: ship portable by default, pay the toolchain tax only where
the performance demands it.

## Status of this binding

`vips_binding.go` calls a hypothetical helper `fastthumb_thumbnail`
(plus real `vips_init`). It does not compile without libvips headers,
and the helper itself would need a small C shim. This is expected and
documented: the file demonstrates structure (init, buffer handoff,
error mapping), and its failure mode is fully contained behind the
build tag — verified in the project report.
