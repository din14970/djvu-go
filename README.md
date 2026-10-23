# djvu-go

[![Go Reference](https://pkg.go.dev/badge/codeberg.org/din14970/djvu-go.svg)](https://pkg.go.dev/codeberg.org/din14970/djvu-go)

A pure Go library for reading and rendering DjVu documents. No cgo, no
external dependencies, no shelling out to djvulibre.

`djvu-go` parses the structure of DjVu files and decodes the image
layers: it counts and describes pages, extracts text, annotations and
metadata, and renders pages to RGBA images with output byte-identical
to the reference `ddjvu` tool.

## Comparison with djvulibre

djvulibre is the reference implementation and the feature superset.
This library is a pure Go decoder whose output is verified byte-identical
to djvulibre's `ddjvu` for everything it implements.

### Feature comparison

| | djvu-go | djvulibre |
|---|---|---|
| Language / dependencies | Pure Go, cgo-free, standard library only | C++, needs libjpeg |
| IFF chunk structure | yes | yes |
| DIRM directory (bundled + indirect) | yes (indirect: directory only) | yes (loads external files) |
| Page INFO, annotations, metadata, hidden text, outline | yes | yes |
| BZZ (de)compression | decode | encode + decode |
| JB2 bilevel (Sjbz, Djbz, shared dictionaries) | decode | encode + decode |
| IW44 wavelet (BG44, FG44) | decode, byte-exact vs ddjvu | encode + decode |
| FGbz palettes | decode | encode + decode |
| MRC compositing, subsampled rendering | yes, byte-exact vs ddjvu | yes |
| MMR (Smmr), JPEG in DjVu (BGjp/FGjp) | no | yes |
| TH44 thumbnails | listed, not decoded | decode |
| Page rotation, non-identity gamma at render time | no | yes |
| Encoding / writing DjVu files | no | yes (c44, cjb2, cpaldjvu, ...) |
| Command line tools | examples only (webserver, test_read) | 20+ tools (ddjvu, djvused, djvumake, ...) |
| C API / browser plugin | no | ddjvuapi, NSPlugin |
| Runs untrusted input in-process | Go memory safety; all decoders fuzzed | C++ with raw pointers; known crash bugs |

### Engineering comparison

djvulibre ships no test suite: its repository has no test directory and
no `make check` target; validation consists of debug assertions that
are compiled out of release builds. Its tools' output is the de facto
conformance reference. This library therefore verifies against that
reference directly:

| | djvu-go | djvulibre |
|---|---|---|
| Test suite | unit + golden tests for every decoder; renderings compared byte for byte with ddjvu (gray and color IW44, compound palette, three-layer, bitonal pages, subsampling factors 1-12, page masks vs `ddjvu -mode=black`) | none (debug-only assertions) |
| Fuzzing | built-in `go test -fuzz` targets for every decoder plus an end-to-end pipeline target; fuzz findings fixed, not filtered; corpus entries committed as regression fixtures | none in-tree |
| Real-world corpus | renders every page of a real-world corpus when present; fails only on errors or panics | n/a |
| Lint / CI | golangci-lint (errcheck, govet, staticcheck, revive, gosec) clean; CI builds and tests Linux + macOS on Go 1.22/1.23 with a 60s fuzz job per target | no CI tests |

### Security posture

The two security issues open against djvulibre at the time of writing
(upstream #1 and #2, both in the IW44 decoder) do not affect this
library:

- **Integer overflow and null write in the IW44 Map constructor** (CWE-190,
  CWE-476): a stream declaring 65535x65535 overflows the 32-bit block
  count. Here dimensions are validated before any allocation and the
  block count is computed in 64-bit integers; the equivalent proof of
  concept is rejected with an error (`testdata/security/`).
- **Division by zero in IW44 reconstruction** (CWE-369): a stream
  declaring a 0x0 image. Rejected at the same guard.

Both proof of concept files are committed as regression fixtures in
`testdata/security/` and replayed by `TestSecurityPoCs`. For
reference, an unpatched djvulibre (< 3.5.30) crashes on both; the
guards djvulibre 3.5.30 added mirror the ones this library has had
since its first IW44 commit.

## Installation

```
go get codeberg.org/din14970/djvu-go
```

Requires Go 1.22 or newer.

## Usage

Open a document from any `io.Reader`. Inputs implementing `io.ReaderAt`
(`*os.File`, `*bytes.Reader`, files opened from an `embed.FS`) are parsed
in place; anything else is buffered in memory first.

```go
package main

import (
	"fmt"
	"os"

	"codeberg.org/din14970/djvu-go"
)

func main() {
	f, err := os.Open("document.djvu")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	fi, _ := f.Stat()

	doc, err := djvu.Open(f, fi.Size())
	if err != nil {
		panic(err)
	}

	n, _ := doc.PageCount()
	fmt.Println("pages:", n)

	// Render the first page at its native resolution.
	img, err := doc.Render(0, djvu.RenderOptions{})
	if err != nil {
		panic(err)
	}
	// img is a *image.RGBA: encode it, serve it, process it.
	_ = img

	// Or render at a quarter resolution for thumbnails.
	thumb, _ := doc.Render(0, djvu.RenderOptions{Subsample: 4})
	_ = thumb

	meta, _ := doc.Metadata()
	fmt.Println("title: ", meta.Title)

	text, _ := doc.PageText(0)
	fmt.Println("text:  ", text)
}
```

Serving pages as PNGs over HTTP needs one helper call:

```go
http.HandleFunc("/page/0.png", func(w http.ResponseWriter, r *http.Request) {
	if err := doc.RenderPNG(w, 0, djvu.RenderOptions{}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
})
```

A complete runnable server is in [examples/webserver](examples/webserver);
`examples/test_read` dumps everything the library can extract from a
document.

### Rendering notes

- Decoded pages are cached per `Document`; repeated renders (including
  at different subsampling factors) do not decode again.
- Pages over 20000 pixels wide or high are rejected before any
  allocation.
- Bilevel pages (a mask without color layers) render as anti-aliased
  black on white; pages without a background render over white, both
  matching `ddjvu`.
- Decoding a typical 300 dpi scanned page takes well under a second
  on a modern core; subsequent renders of the same page only pay for
  compositing.

### Indirect documents

Indirect multipage documents store their pages in external files listed
in the DIRM directory of the index file. `Kind` reports
`KindIndirect` for them; `Pages` and `Components` expose the names of the
external files. Loading the external files is not implemented yet, so
page-level data (text, annotations, INFO fields, rendering) is
unavailable for indirect documents.

### Low-level chunk access

The IFF chunk layer is exported as the
[`iff`](https://pkg.go.dev/codeberg.org/din14970/djvu-go/iff) package:

```go
chunks, err := iff.ParseChunks(r) // top-level chunk list
for _, c := range chunks {
	fmt.Println(c.FullID(), c.Length, c.Start) // e.g. FORM:DJVM 770 0
	if c.Composite() {
		children, err := c.Children() // recurse into FORM chunks
		// ...
	} else {
		payload := c.Payload() // io.ReadSeeker over the chunk payload
		// ...
	}
}
```

## What does not work (yet)

- Writing or modifying DjVu files.
- Loading external files of indirect documents.
- MMR (Smmr) and JPEG (BGjp/FGjp) image alternatives.
- Thumbnail decoding (thumbnails are only listed in the directory).
- Page rotation and non-identity gamma correction at render time.
- The METa/METz metadata chunks (rare; metadata is normally stored in
  annotations, which is supported).

See [DESIGN.md](DESIGN.md) for the internals and the roadmap.

## Testing

```
go test ./...
```

The test suite includes unit tests for every decoder, byte-exact
golden tests against `ddjvu` renderings for JB2 masks, IW44 gray and
color images, compound palette pages and three-layer pages, fuzz
targets for every decoder plus an end-to-end target (`go test -fuzz`),
a corpus test over the real-world documents in `realdata/` when that
directory is present, and benchmarks for the chunk walker, BZZ decoder,
alpha blits and full page rendering.

Golden fixtures are generated with the djvulibre tools;
`testdata/*/generate.sh` regenerates them.

## License

MIT, see [LICENSE](LICENSE).
