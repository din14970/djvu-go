package iff

import (
	"bytes"
	"testing"
)

// FuzzParseChunks feeds arbitrary data to the chunk parser and walks the
// resulting chunk tree. The parser must not panic, hang or produce chunks
// that point outside the input.
func FuzzParseChunks(f *testing.F) {
	f.Add([]byte{})
	f.Add(chunk("INFO", make([]byte, 10)))
	f.Add(form("DJVU", chunk("INFO", make([]byte, 10))))
	f.Add([]byte("AT&T" + "FORM\x00\x00\x00\x0eDJVUINFO\x00\x00\x00\x02ab"))
	f.Add([]byte("FORM\x00\x00\x00\x04"))
	f.Add([]byte("FORM\xff\xff\xff\xffDJVU"))
	f.Add(form("DJVU", form("DJVU", form("DJVU", chunk("Sjbz", nil)))))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		chunks, err := ParseChunks(bytes.NewReader(data))
		if err != nil {
			return
		}
		// Walk the full tree; chunk offsets must stay inside the input.
		var visit func(cs []*Chunk)
		visit = func(cs []*Chunk) {
			for _, c := range cs {
				if c.Start < 0 || c.Start > int64(len(data)) {
					t.Fatalf("chunk %s starts at %d, input is %d bytes", c.FullID(), c.Start, len(data))
				}
				if c.Composite() {
					children, err := c.Children()
					if err == nil {
						visit(children)
					}
				}
			}
		}
		visit(chunks)
	})
}

func BenchmarkParseChunks(b *testing.B) {
	// A large synthetic file: a FORM:DJVM holding 4096 component files,
	// each a small FORM:DJVU with an INFO chunk.
	data := buildLargeFile(4096)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chunks, err := ParseChunks(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		if len(chunks) != 1 {
			b.Fatalf("got %d top-level chunks", len(chunks))
		}
		children, err := chunks[0].Children()
		if err != nil {
			b.Fatal(err)
		}
		if len(children) != 4097 { // DIRM + 4096 files
			b.Fatalf("got %d children", len(children))
		}
	}
}

func BenchmarkWalkTree(b *testing.B) {
	data := buildLargeFile(4096)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chunks, err := ParseChunks(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		n := walkAll(chunks)
		if n != 8194 { // DJVM + DIRM + 4096*(FORM:DJVU + INFO)
			b.Fatalf("walked %d chunks, want 8194", n)
		}
	}
}

// walkAll visits every chunk in the tree and returns the total count.
func walkAll(chunks []*Chunk) int {
	n := 0
	for _, c := range chunks {
		n++
		if c.Composite() {
			if children, err := c.Children(); err == nil {
				n += walkAll(children)
			}
		}
	}
	return n
}

// buildLargeFile builds a bundled-style synthetic document with n component
// files, each consisting of a FORM:DJVU containing an INFO chunk.
func buildLargeFile(n int) []byte {
	var content []byte
	dirm := chunk("DIRM", make([]byte, 16))
	content = append(content, dirm...)
	for i := 0; i < n; i++ {
		info := chunk("INFO", make([]byte, 10))
		page := form("DJVU", pad(info))
		if len(page)%2 != 0 {
			page = append(page, 0)
		}
		content = append(content, page...)
	}
	return form("DJVM", content)
}
