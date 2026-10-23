# djvu-go

[![Go Reference](https://pkg.go.dev/badge/codeberg.org/din14970/djvu-go.svg)](https://pkg.go.dev/codeberg.org/din14970/djvu-go)

A pure Go library for reading DjVu documents. No cgo, no external
dependencies, no shelling out to djvulibre.

`djvu-go` parses the structure of DjVu files: the IFF chunk stream, the
multipage document directory (DIRM), page geometry (INFO), annotations,
document metadata, the hidden text layer and the outline (bookmarks).
It works with single-page documents and with bundled and indirect
multipage documents.

**Image decoding is not implemented.** DJVU pages are images compressed
with JB2 (bilevel) and IW44 (photo) codecs; this library does not decode
them and cannot render pages. See [DESIGN.md](DESIGN.md) for the roadmap.

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

	fmt.Println("kind:  ", doc.Kind()) // single-page, bundled or indirect

	n, _ := doc.PageCount()
	fmt.Println("pages: ", n)

	pages, _ := doc.Pages()
	for _, p := range pages {
		fmt.Printf("page %d %q: %dx%d @ %d dpi, DjVu v%d\n",
			p.Index, p.Name, p.Width, p.Height, p.DPI, p.Version)
	}

	meta, _ := doc.Metadata()
	fmt.Println("title: ", meta.Title)
	fmt.Println("author:", meta.Author)

	// Hidden text and annotations of the first page.
	text, _ := doc.PageText(0)
	fmt.Println("text:  ", text)

	annos, _ := doc.Annotations(0)
	for _, a := range annos {
		fmt.Println("anno:  ", a) // e.g. (maparea ...) or (metadata ...)
	}
}
```

### Indirect documents

Indirect multipage documents store their pages in external files listed
in the DIRM directory of the index file. `Kind` reports
`KindIndirect` for them; `Pages` and `Components` expose the names of the
external files. Loading the external files is not implemented yet, so
`PageText`, `Annotations` and page INFO fields are unavailable for
indirect documents.

### Low-level chunk access

The IFF chunk layer is exported as the
[`iff`](https://pkg.go.dev/codeberg.org/din14970/djvu-go/iff) package:

```go
chunks, err := iff.ParseChunks(r) // top-level chunk list
for _, c := range chunks {
	fmt.Println(c.FullID(), c.Length, c.Start) // e.g. FORM:DJVM 770 0
	if c.Composite() {
		children, err := c.Children() // recurse into FORM chunks
		...
	} else {
		payload := c.Payload() // io.ReadSeeker over the chunk payload
		...
	}
}
```

## What works

- IFF chunk walking: FORM/LIST/PROP/CAT composites, secondary
  identifiers, AT&T/SDJV magic sequences, even-offset padding, strict
  length checking, lazy payload access (`io.SectionReader` views, no
  copying).
- Bundled and indirect multipage documents (FORM:DJVM + DIRM), both
  directory format versions 0 and 1, including the BZZ-compressed
  directory tail. Bundled directories are cross-checked against the
  actual chunk stream.
- Per-page INFO: width, height, DPI, gamma, version, orientation.
- Annotations (ANTa, ANTz, FORM:ANNO) parsed into expression trees,
  merged from included files and the shared annotation file with the
  reference implementation's precedence order.
- Document metadata (title, author, subject, keywords, producer, and
  arbitrary other keys).
- Hidden text (TXTa, TXTz): the raw UTF-8 text of a page. The positional
  zone structure is not decoded.
- Outline / bookmarks (NAVM), also for indirect documents.
- BZZ decompression (Burrows-Wheeler + ZP arithmetic coder), used by
  DIRM, ANTz, TXTz and NAVM.

## What does not work (yet)

- Rendering or decoding images: JB2 (Sjbz/Djbz), IW44 (BG44/FG44/TH44),
  MMR (Smmr), JPEG (BGjp/FGjp) and palettes (FGbz) are not decoded.
- Writing or modifying DjVu files.
- Loading external files of indirect documents.
- Thumbnail decoding (thumbnails are only listed in the directory).
- The METa/METz metadata chunks (rare; metadata is normally stored in
  annotations, which is supported).

## Testing

```
go test ./...
```

The test suite includes unit tests for the chunk walker, the BZZ coder
and the DIRM parser, fuzz targets for the chunk parser and BZZ decoder
(`go test -fuzz`), benchmarks for the chunk walker and BZZ decoder, and
integration tests against golden fixtures generated with the djvulibre
tools (see `testdata/djvu/generate.sh`).

## License

MIT, see [LICENSE](LICENSE).
