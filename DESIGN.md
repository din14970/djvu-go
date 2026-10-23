# djvu-go design notes

This document describes the architecture of the library, the details of
the BZZ coder implementation, and the roadmap towards image decoding.
Byte-level behavior was derived from the DjVu v3 specification
(November 2005) and cross-checked against the DjVuLibre reference
implementation (libdjvu 3.5.x); deviations are noted below.

## Package layout

```
djvu.go        Document, Open, Kind, PageCount, Pages, Components,
               Annotations, Metadata
info.go        INFO chunk (page geometry and display parameters)
anno.go        annotation expression parser, Annotations, Metadata
text.go        hidden text (TXTa/TXTz)
outline.go     outline / bookmarks (NAVM)
iff/           low-level IFF85 chunk parser (public)
internal/bzz/   BZZ decompression (ZP coder + inverse Burrows-Wheeler)
internal/dirm/ DIRM directory parser (multipage documents)
```

Planned packages for image decoding (see roadmap):

```
internal/jb2/  JB2 bilevel image decoder (Sjbz/Djbz chunks)
internal/iw44/ IW44 wavelet image decoder (BG44/FG44/TH44 chunks)
internal/num/  number codec shared by JB2 and IW44 (built on the ZP coder)
```

## The chunk model

DjVu files follow the EA IFF 85 container format: a stream of chunks,
each a 4 byte identifier plus a 32 bit big endian length. The chunk
content follows, padded to an even file offset with a single byte when
its length is odd. The identifiers `FORM`, `LIST`, `PROP` and `CAT `
denote composite chunks; their content begins with a 4 byte secondary
identifier (written FORM:DJVU, FORM:DJVM, ...) followed by a nested
chunk stream.

`iff.ParseChunks` parses the top-level chunk sequence; `Chunk.Children`
descends into a composite. The parser mirrors the rules of
IFFByteStream.cpp:

- Identifiers must consist of printable bytes (0x20-0x7e). The reserved
  prefixes `FOR`, `LIS` and `CAT` followed by a digit 1-9 are rejected,
  as are composite secondary identifiers.
- The magic sequences `AT&T` (0x41 0x54 0x26 0x54) and `SDJV` may
  appear at chunk boundaries and are skipped. djvulibre-written files
  start with `AT&T` to dodge a Microsoft AIFF misdetection in old
  browsers.
- Chunk lengths are enforced strictly: a chunk may never extend beyond
  its enclosing composite. At the top level, a missing final pad byte is
  tolerated (the reference implementation does the same), but truncated
  headers are errors.
- Unknown chunk types are skipped without failing, as the specification
  mandates.

Deviations from the reference: nesting is capped at `iff.MaxDepth` (100)
levels to reject pathological files cheaply (the reference has no
limit); payloads are never copied - `Chunk.Payload` returns an
`io.SectionReader` view over the original input, which must therefore
implement `io.ReaderAt` or be buffered by the caller side of `ParseChunks`.

## Document model

Three document kinds are recognized by `Open`:

1. Single-page: a top-level FORM:DJVU (the obsolete IW44 files
   FORM:PM44 and FORM:BM44 are accepted as well).
2. Bundled multipage: FORM:DJVM whose first chunk is DIRM. The
   directory lists component files (pages, shared components,
   thumbnails, shared annotations) with their offsets and sizes inside
   the file. Every directory entry is resolved to an actual chunk in
   the FORM:DJVM stream; a mismatch fails the open.
3. Indirect multipage: FORM:DJVM whose DIRM has the indirect flag.
   Only the index file is read; page data would live in external files
   (not loaded in v0.1, names are exposed).

### DIRM

Layout (both versions): a flags/version byte (bit 7 = bundled, bits 0-6
= version 0 or 1), a big endian file count, 32 bit offsets for bundled
documents (interleaved with 24 bit sizes in version 0), then a BZZ
compressed tail with the version 1 sizes, the flag bytes and the
zero-terminated id/name/title strings.

The parser follows DjVmDir.cpp, including the version 0 flag
conversion, the single-shared-annotation rule and duplicate id/name/title
checks. One deviation: string table overruns are reported as errors,
where the reference implementation reads past the buffer.

### INFO

Ten bytes: big endian width and height, then - little endian, unlike the
rest of the format - version and dpi, then gamma*10 and orientation
flags. The 0xff sentinel in the high bytes marks the old single-byte
form. Defaults (dpi 300, gamma 2.2) and clamps follow DjVuInfo.cpp.

### Annotations, text, outline

- Annotations live in ANTa (plain) or ANTz (BZZ) chunks, optionally
  wrapped in a FORM:ANNO composite. Their content is a sequence of
  parenthesized expressions (`(maparea ...)`, `(metadata ...)`, ...).
  The parser follows DjVuAnno.cpp's tokenizer (quoted strings with
  octal and single-character escapes; malformed constructs are silently
  tolerated). Page annotations are merged with those of INCL-included
  components and the shared annotation file, includes first, so that
  the page's own annotations take precedence - the order used by
  DjVuFile::get_merged_anno.
- Document metadata is the `(metadata (key "value") ...)` expressions
  found in the annotations of the shared annotation file and the pages.
- Hidden text lives in TXTa/TXTz chunks: a 24 bit text length followed
  by UTF-8 text and an optional positional zone tree (not decoded).
- Outlines live in the NAVM chunk of the FORM:DJVM: a BZZ stream with a
  big endian entry count and flat pre-order entries (little endian
  child count, big endian title length, title, big endian URL length,
  URL) from which the bookmark tree is rebuilt.

## The BZZ coder

BZZ (BSByteStream in the reference library) is the general purpose
compressor of the format, used by DIRM, ANTz, TXTz and NAVM. It is a
Burrows-Wheeler block sorting coder in the spirit of bzip2:

1. Each block starts with a 24 bit stored size (the payload length plus
   one dummy byte that the encoder appends; the decoder serves
   size-1 bytes per block), followed by one or two raw "estimation
   speed" flag bits.
2. The block body is the Burrows-Wheeler transform of the payload plus
   a trailing zero marker byte, coded with a move-to-front ranking
   refined by empirical frequencies (the FREQMAX=4 frequency list and
   the `fadd >> fshift` overflow adjustment are replicated exactly).
3. The MTF ranks are coded with a fixed binary decision tree over an
   array of 300 binary contexts, driven by the ZP quasi-arithmetic
   coder. `decode_raw` and `decode_binary` mirror the reference
   functions.
4. A stored size of zero terminates the stream.

The ZP coder (ZPCodec.cpp, ZPCODER variant with the djvucompat table)
is a binary adaptive coder with a fixed 256-state adaptation table
containing per-state probability estimates, adaptation thresholds and
next-state links. The decoder keeps a 16 bit code register refilled
from a 32 bit preload buffer, uses a "fence" fast path for the most
probable symbol, and tolerates reading up to 25 bytes past the end of
the input before reporting an error (the reference decoder over-reads
a few bytes by design). The adaptation table is generated from
ZPCodec.cpp (see internal/bzz/zp_table.go).

Coverage: decompression only. Encoding is not implemented (tests use
streams produced by the djvulibre `bzz` tool). Note that BZZ carries no
checksums: a truncated or corrupt stream can decode to garbage without
an error, exactly like the reference decoder. Error detection relies on
block-level structural checks (marker position, reconstructed stream
consistency).

The same ZP coder, with different context trees and number codecs, is
the foundation of JB2 and IW44; internal/bzz/zp.go is written so it can
be reused for those decoders.

## Roadmap: image decoding

1. `internal/num`: the number codec from the JB2 specification
   (arithmetic coded numbers with adaptive contexts, built on the ZP
   coder from internal/bzz).
2. `internal/jb2`: JB2 bilevel decoding for Sjbz chunks (records:
   start-of-image, symbol library, matched copies and refinements,
   blit list; REQUIRED_DICT_OR_RESET records from format version 21
   for shared Djbz dictionaries). Output: 1-bit page masks.
3. `internal/iw44`: IW44 wavelet decoding for BG44/FG44 chunks
   (chunk headers with serial/chunk numbers, zigzag coefficient
   coding, successive refinement passes). Output: gray/color images.
4. With Sjbz + BG44/FG44 decoded, compound pages can be rendered by
   painting the foreground color image through the JB2 mask over the
   background. Smmr (MMR) and BGjp/FGjp (JPEG) alternative encodings
   can follow; JPEG would need either a stdlib-based decoder (Go 1.22
   has image/jpeg) or an external dependency.
5. FGbz palettes (DjVuPalette, itself BZZ compressed) for per-object
   foreground colors.

Until then this library reads everything except pixels.

## Test strategy

- Unit tests for each package, including byte-level edge cases taken
  from the specification and the reference sources.
- Golden fixtures in testdata/ generated with the djvulibre tools
  (cjb2, djvm, djvmcvt, djvused, bzz); testdata/djvu/generate.sh
  regenerates them. Synthetic DIRM and BZZ payloads are built from
  pre-compressed blobs so tests do not need a BZZ encoder.
- Fuzz targets for the chunk parser (iff) and the BZZ decoder.
- Benchmarks for the chunk walker and the BZZ decoder.
