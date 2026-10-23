# djvu-go

[![Go Reference](https://pkg.go.dev/badge/codeberg.org/din14970/djvu-go.svg)](https://pkg.go.dev/codeberg.org/din14970/djvu-go)

A pure Go library for reading and rendering DjVu documents. No cgo, no
external dependencies, no shelling out to djvulibre.

`djvu-go` parses the structure of DjVu files and decodes the image
layers: it counts and describes pages, extracts text, annotations and
metadata, and renders pages to RGBA images with output byte-identical
to the reference `ddjvu` tool.

## Format support

| Feature | Status |
|---|---|
| IFF chunk structure (FORM/LIST/PROP/CAT) | decoded |
| Bundled multipage documents (FORM:DJVM + DIRM) | decoded |
| Indirect multipage documents | directory only (external files are listed, not loaded) |
| Page INFO (size, DPI, gamma, orientation) | decoded |
| JB2 bilevel decoding (Sjbz, Djbz, shared dictionaries) | decoded |
| IW44 wavelet decoding (BG44, FG44) | decoded, byte-exact vs ddjvu |
| FGbz palettes (per-object colors) | decoded |
| MRC page compositing and Render | decoded, byte-exact vs ddjvu |
| Hidden text (TXTz/TXTa), outline (NAVM), annotations (ANTa/ANTz) | decoded |
| Subsampled rendering (factors 2..15) | decoded, byte-exact vs ddjvu |
| Encoding / writing DjVu files | not implemented |
| MMR (Smmr), JPEG (BGjp/FGjp) alternatives | not implemented |
| Page rotation from INFO orientation flags | not implemented (pages render unrotated) |
| Gamma correction other than identity | not implemented (colors match the file's design gamma) |

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
