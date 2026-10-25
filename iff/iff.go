// Go port of djvulibre's libdjvu/IFFByteStream.cpp.
// Upstream copyright (c) 2002 Leon Bottou and Yann Le Cun; derived
// from the DjVu Reference Library (c) 1999-2001 LizardTech, Inc. This
// derivative work is licensed GPL-2.0-or-later; see the LICENSE file.

// Package iff implements a reader for the EA IFF 85 chunk structure that
// forms the backbone of the DjVu file format. It is the low-level API of
// this module: the djvu package builds document parsing on top of it,
// and it can be used directly by tools that need to inspect or walk the
// chunk structure of IFF files.
//
// An IFF file is a sequence of chunks. Each chunk starts with a four byte
// chunk identifier followed by a 32 bit big endian length. Chunks whose
// identifier is "FORM", "LIST", "PROP" or "CAT " are composite chunks: their
// payload starts with a four byte secondary identifier (written FORM:DJVU,
// FORM:DJVM, ...) and contains a nested sequence of chunks. Chunks with an
// odd payload length are padded to an even offset with a single byte.
//
// This implementation mirrors the chunk walking rules of the DjVu reference
// library (IFFByteStream.cpp):
//
//   - The magic sequences "AT&T" (0x41 0x54 0x26 0x54) and "SDJV" may appear
//     at chunk boundaries and are skipped.
//   - Chunk identifiers must consist of printable bytes (0x20-0x7e). The
//     reserved prefixes "FOR", "LIS" and "CAT" followed by a digit 1-9 are
//     rejected. A secondary identifier of a composite chunk must not be
//     composite itself.
//   - Unknown chunk types are skipped without failing.
//   - Chunk length fields are enforced strictly: a chunk may never extend
//     beyond its enclosing composite chunk. At the top level a missing final
//     pad byte is tolerated, but a truncated chunk header is an error.
//   - Composite nesting is limited to MaxDepth levels to reject pathological
//     files; the reference implementation has no such limit.
//
// The parser never copies chunk payloads into memory. Payloads are exposed
// as *io.SectionReader views over the original input, which therefore must
// either implement io.ReaderAt or be fully buffered (the fallback used for
// plain io.Reader inputs).
package iff

import (
	"bytes"
	"fmt"
	"io"
)

// MaxDepth is the maximum nesting depth of composite chunks accepted by the
// parser. Files nesting deeper are rejected with an error. Real DjVu files
// nest no more than a handful of levels.
const MaxDepth = 100

// unlimited is the sentinel region end used for the top-level chunk list.
const unlimited = int64(1) << 62

const (
	idFORM = "FORM"
	idLIST = "LIST"
	idPROP = "PROP"
	idCAT  = "CAT "

	magicATT = "AT&T"
	magicSDJ = "SDJV"
)

// Chunk describes a single IFF chunk.
//
// Offsets and lengths reported by a Chunk are relative to the reader that
// was passed to [ParseChunks] (for a *os.File this means the beginning of
// the file, regardless of the file's current seek position).
type Chunk struct {
	// ID is the primary four byte chunk identifier, for example "INFO",
	// "DIRM" or "FORM".
	ID string

	// Secondary holds the secondary identifier of composite chunks, for
	// example "DJVU" for a FORM:DJVU chunk. It is empty for plain chunks.
	Secondary string

	// Length is the value of the chunk's length field. For plain chunks it
	// is the payload size. For composite chunks it covers the four bytes
	// of the secondary identifier plus all nested chunks.
	Length int64

	// Start is the offset of the chunk header (its identifier) within the
	// region the chunk list was parsed from.
	Start int64

	ra        io.ReaderAt
	depth     int
	composite bool
	// contentOff and contentEnd delimit the chunk content region: the
	// payload for plain chunks, the nested chunk stream for composites.
	contentOff int64
	contentEnd int64
}

// Composite reports whether the chunk is a composite chunk ("FORM", "LIST",
// "PROP" or "CAT ") that contains nested chunks.
func (c *Chunk) Composite() bool { return c.composite }

// FullID returns "ID:Secondary" for composite chunks and "ID" otherwise.
func (c *Chunk) FullID() string {
	if c.Composite() {
		return c.ID + ":" + c.Secondary
	}
	return c.ID
}

// Payload returns a reader for the chunk content. For plain chunks this is
// the Length payload bytes. For composite chunks it is the nested chunk
// stream, excluding the secondary identifier. The returned section reader
// is positioned at the start of the content and can be read and seeked
// independently of other chunks.
func (c *Chunk) Payload() *io.SectionReader {
	off, n := c.contentRange()
	return io.NewSectionReader(c.ra, off, n)
}

// ReadAll returns the full chunk content as returned by [Chunk.Payload].
// It fails if the input ends before the declared length has been read.
func (c *Chunk) ReadAll() ([]byte, error) {
	r := c.Payload()
	size := r.Size()
	var buf bytes.Buffer
	// Do not preallocate the full declared length: a corrupt length
	// field must not cause a huge allocation.
	if grow := min(size, 1<<20); grow > 0 {
		buf.Grow(int(grow))
	}
	if _, err := io.CopyN(&buf, r, size); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("iff: reading %d bytes of chunk %s at offset %d: %w", size, c.FullID(), c.Start, err)
	}
	return buf.Bytes(), nil
}

// Children parses and returns the chunks nested inside a composite chunk.
// The returned chunk offsets are relative to the same origin as the parent
// chunk. Calling Children on a plain chunk returns an error. Composites
// are parsed lazily and independently, so repeated calls re-parse the
// nested chunk stream.
func (c *Chunk) Children() ([]*Chunk, error) {
	if !c.Composite() {
		return nil, fmt.Errorf("iff: chunk %q is not a composite chunk", c.ID)
	}
	if c.Length < 4 {
		return nil, fmt.Errorf("iff: composite chunk %s at offset %d is corrupt: length %d does not even cover the secondary identifier", c.FullID(), c.Start, c.Length)
	}
	off, n := c.contentRange()
	return parseList(c.ra, off, off+n, c.depth+1)
}

// contentRange returns the offset and size of the chunk content region:
// the payload for plain chunks, the nested stream for composites.
func (c *Chunk) contentRange() (off, size int64) {
	off = c.contentOff
	size = c.contentEnd - c.contentOff
	if c.composite {
		off += 4
		size -= 4
	}
	if size < 0 {
		size = 0
	}
	return off, size
}

// ParseChunks parses the top-level chunk sequence from r and returns the
// chunks it contains. Composite chunks are returned as single chunks; use
// [Chunk.Children] to descend into them.
//
// If r implements io.ReaderAt (as *os.File, *bytes.Reader and files opened
// from an embed.FS do) the input is accessed in place. Otherwise r is read
// fully into memory first.
func ParseChunks(r io.Reader) ([]*Chunk, error) {
	ra, ok := r.(io.ReaderAt)
	if !ok {
		buf, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("iff: reading input: %w", err)
		}
		ra = bytes.NewReader(buf)
	}
	return parseList(ra, 0, unlimited, 0)
}

// parseList parses the chunk sequence in the byte region [base, end) of ra.
// end == unlimited denotes an unbounded region that ends at the end of ra.
func parseList(ra io.ReaderAt, base, end int64, depth int) ([]*Chunk, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("iff: composite chunks nested deeper than %d levels", MaxDepth)
	}
	var chunks []*Chunk
	off := base
	for {
		if end != unlimited {
			if off == end {
				return chunks, nil
			}
			if off > end {
				return nil, fmt.Errorf("iff: chunk at offset %d overruns the enclosing composite chunk", off)
			}
		}
		// Skip the pad byte that follows an odd-sized chunk.
		if off%2 != 0 {
			var pad [1]byte
			n, err := ra.ReadAt(pad[:], off)
			if n == 0 {
				if end == unlimited && (err == io.EOF || err == nil) {
					// The final pad byte may be missing at the end of
					// the file; the reference implementation tolerates
					// this at the top level.
					return chunks, nil
				}
				return nil, fmt.Errorf("iff: missing pad byte at offset %d: %w", off, io.ErrUnexpectedEOF)
			}
			if err != nil && err != io.EOF {
				return nil, fmt.Errorf("iff: reading pad byte at offset %d: %w", off, err)
			}
			off++
		}

		// Read the chunk identifier, skipping magic sequences.
		var id [4]byte
		for {
			if end != unlimited {
				if off == end {
					return chunks, nil
				}
				if off+4 > end {
					return nil, fmt.Errorf("iff: chunk identifier at offset %d overruns the enclosing composite chunk", off)
				}
			}
			n, err := ra.ReadAt(id[:], off)
			if n == 0 {
				if end == unlimited && (err == io.EOF || err == nil) {
					return chunks, nil
				}
				return nil, fmt.Errorf("iff: truncated chunk identifier at offset %d: %w", off, io.ErrUnexpectedEOF)
			}
			if n < 4 {
				return nil, fmt.Errorf("iff: truncated chunk identifier at offset %d: %w", off, io.ErrUnexpectedEOF)
			}
			if err != nil && err != io.EOF {
				return nil, fmt.Errorf("iff: reading chunk identifier at offset %d: %w", off, err)
			}
			if string(id[:]) == magicATT || string(id[:]) == magicSDJ {
				off += 4
				continue
			}
			break
		}

		// Validate the identifier.
		kind, err := checkID(string(id[:]))
		if err != nil {
			return nil, fmt.Errorf("iff: chunk at offset %d: %w", off, err)
		}

		// Read the length field.
		var sizeBuf [4]byte
		if end != unlimited && off+8 > end {
			return nil, fmt.Errorf("iff: chunk header of %q at offset %d overruns the enclosing composite chunk", string(id[:]), off)
		}
		if _, err := ra.ReadAt(sizeBuf[:], off+4); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("iff: reading length of chunk %q at offset %d: %w", string(id[:]), off, err)
		}
		length := int64(sizeBuf[0])<<24 | int64(sizeBuf[1])<<16 | int64(sizeBuf[2])<<8 | int64(sizeBuf[3])

		// Enforce the length against the enclosing region.
		contentOff := off + 8
		if end != unlimited && contentOff+length > end {
			return nil, fmt.Errorf("iff: chunk %q at offset %d: declared length %d overruns the enclosing composite chunk", string(id[:]), off, length)
		}

		c := &Chunk{
			ID:         string(id[:]),
			Length:     length,
			Start:      off,
			ra:         ra,
			depth:      depth,
			composite:  kind == 1,
			contentOff: contentOff,
			contentEnd: contentOff + length,
		}

		if kind == 1 && length >= 4 {
			// Composite chunk: read the secondary identifier. When the
			// length does not cover the identifier, the reference lets
			// the header through and fails when descending into the
			// (empty) content; Children reports the error here too.
			var sec [4]byte
			if _, err := ra.ReadAt(sec[:], contentOff); err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return nil, fmt.Errorf("iff: reading secondary identifier of FORM at offset %d: %w", off, err)
			}
			if _, err := checkID(string(sec[:])); err != nil || string(sec[:]) == idFORM || string(sec[:]) == idLIST || string(sec[:]) == idPROP || string(sec[:]) == idCAT {
				return nil, fmt.Errorf("iff: composite chunk at offset %d: invalid secondary identifier %q", off, string(sec[:]))
			}
			c.Secondary = string(sec[:])
		}

		chunks = append(chunks, c)
		off = c.contentEnd
		// Pad byte (if any) is consumed at the top of the loop.
	}
}

// checkID validates a four byte chunk identifier and reports its kind:
// 0 for a plain chunk, 1 for a composite chunk. Illegal identifiers
// produce an error, following IFFByteStream::check_id in the reference
// implementation.
func checkID(id string) (int, error) {
	for i := 0; i < 4; i++ {
		if id[i] < 0x20 || id[i] > 0x7e {
			return 0, fmt.Errorf("illegal chunk identifier %q: byte %d out of range", id, i)
		}
	}
	switch id {
	case idFORM, idLIST, idPROP, idCAT:
		return 1, nil
	}
	for _, prefix := range []string{"FOR", "LIS", "CAT"} {
		if len(id) >= 4 && id[:3] == prefix && id[3] >= '1' && id[3] <= '9' {
			return 0, fmt.Errorf("reserved chunk identifier %q", id)
		}
	}
	return 0, nil
}
