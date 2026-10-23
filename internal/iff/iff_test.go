package iff

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// chunk builds the encoding of a plain chunk with the given payload.
func chunk(id string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString(id)
	buf.Write([]byte{byte(len(payload) >> 24), byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload))})
	buf.Write(payload)
	return buf.Bytes()
}

// form builds the encoding of a composite chunk holding content.
func form(secondary string, content []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("FORM")
	n := int64(len(content) + 4)
	buf.Write([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
	buf.WriteString(secondary)
	buf.Write(content)
	return buf.Bytes()
}

// mustParse parses b or fails the test.
func mustParse(t *testing.T, b []byte) []*Chunk {
	t.Helper()
	chunks, err := ParseChunks(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("ParseChunks: %v", err)
	}
	return chunks
}

// walk descends into all composite chunks, collecting them.
func walk(t *testing.T, chunks []*Chunk, visit func(*Chunk)) {
	t.Helper()
	for _, c := range chunks {
		visit(c)
		if c.Composite() {
			children, err := c.Children()
			if err != nil {
				t.Fatalf("Children of %s at %d: %v", c.FullID(), c.Start, err)
			}
			walk(t, children, visit)
		}
	}
}

func TestParseSingleChunk(t *testing.T) {
	data := chunk("INFO", []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	chunks := mustParse(t, data)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	c := chunks[0]
	if c.ID != "INFO" || c.Secondary != "" || c.Length != 10 || c.Start != 0 {
		t.Fatalf("unexpected chunk: %+v", c)
	}
	if c.Composite() {
		t.Fatal("plain chunk reported as composite")
	}
	payload, err := io.ReadAll(c.Payload())
	if err != nil || !bytes.Equal(payload, data[8:]) {
		t.Fatalf("payload = %v, %v", payload, err)
	}
}

func TestParseNestedForm(t *testing.T) {
	info := chunk("INFO", make([]byte, 10))
	anno := chunk("ANTa", []byte("(zoom stretch)")) // odd payload (15)
	thum := form("THUM", chunk("TH44", []byte{1, 2, 3, 4}))
	data := form("DJVU", append(append(append([]byte{}, info...), pad(anno)...), thum...))
	chunks := mustParse(t, data)
	if len(chunks) != 1 || chunks[0].FullID() != "FORM:DJVU" {
		t.Fatalf("unexpected top level: %+v", chunks)
	}
	top := chunks[0]
	if !top.Composite() || top.Length != int64(len(data)-8) {
		t.Fatalf("bad composite header: %+v", top)
	}
	children, err := top.Children()
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("got %d children, want 3 (INFO, ANTa, FORM:THUM)", len(children))
	}
	wantIDs := []string{"INFO", "ANTa", "FORM"}
	wantSec := []string{"", "", "THUM"}
	for i, c := range children {
		if c.ID != wantIDs[i] || c.Secondary != wantSec[i] {
			t.Errorf("child %d = %s:%s, want %s:%s", i, c.ID, c.Secondary, wantIDs[i], wantSec[i])
		}
	}
	// Child offsets are relative to the same origin as the parent.
	if children[0].Start != 12 {
		t.Errorf("INFO starts at %d, want 12", children[0].Start)
	}
	// ANTa has an odd payload: the next chunk starts after the pad byte.
	if children[2].Start%2 != 0 {
		t.Errorf("chunk after odd-size chunk starts at odd offset %d", children[2].Start)
	}
	// Payload of a composite excludes the secondary identifier.
	thumChildren, err := children[2].Children()
	if err != nil {
		t.Fatalf(" Children of FORM:THUM: %v", err)
	}
	if len(thumChildren) != 1 || thumChildren[0].ID != "TH44" {
		t.Fatalf("unexpected FORM:THUM children: %+v", thumChildren)
	}
	payload, err := thumChildren[0].ReadAll()
	if err != nil || !bytes.Equal(payload, []byte{1, 2, 3, 4}) {
		t.Fatalf("TH44 payload = %v, %v", payload, err)
	}
}

// pad appends a pad byte when the chunk encoding has odd length.
func pad(b ...[]byte) []byte {
	var all []byte
	for _, x := range b {
		all = append(all, x...)
	}
	if len(all)%2 != 0 {
		all = append(all, 0)
	}
	return all
}

func TestMissingFinalPadAtEOF(t *testing.T) {
	// An odd-size chunk at the very end of the file without a pad byte
	// is tolerated, matching the reference implementation.
	data := chunk("ANTa", []byte("(x)")) // 3-byte payload, no pad
	chunks := mustParse(t, data)
	if len(chunks) != 1 || chunks[0].Length != 3 {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
}

func TestPadInsideComposite(t *testing.T) {
	odd := chunk("AAAz", []byte{1, 2, 3})
	even := chunk("BBBz", []byte{4})
	content := append(append([]byte{}, odd...), append([]byte{0}, even...)...) // manual pad
	data := form("DJVU", content)
	chunks := mustParse(t, data)
	children, err := chunks[0].Children()
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(children) != 2 || children[1].ID != "BBBz" {
		t.Fatalf("expected AAAz then BBBz, got %+v", children)
	}
	if want := int64(len(data) - len(even)); children[1].Start != want {
		t.Errorf("BBBz starts at %d, want %d", children[1].Start, want)
	}
}

func TestTruncatedIdentifier(t *testing.T) {
	data := chunk("INFO", make([]byte, 10))
	_, err := ParseChunks(bytes.NewReader(data[:3])) // partial identifier
	if err == nil {
		t.Fatal("expected error for truncated identifier")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("error = %v, want wrapped io.ErrUnexpectedEOF", err)
	}
}

func TestTruncatedLength(t *testing.T) {
	data := chunk("INFO", make([]byte, 10))
	_, err := ParseChunks(bytes.NewReader(data[:6])) // id but partial length
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("error = %v, want wrapped io.ErrUnexpectedEOF", err)
	}
}

func TestTruncatedPayload(t *testing.T) {
	// At the top level the length field is trusted until the payload is
	// actually read, as in the reference implementation.
	data := chunk("INFO", make([]byte, 10))
	chunks := mustParse(t, data[:10])
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks", len(chunks))
	}
	if _, err := chunks[0].ReadAll(); err == nil {
		t.Error("expected error reading past the end of the input")
	}
}

func TestChildOverrunsParent(t *testing.T) {
	// A child chunk declaring more data than the FORM contains.
	bad := chunk("BG44", make([]byte, 64))
	bad[7] = 0xff // length field now 4286578688-ish, way past the parent
	data := form("DJVU", bad)
	chunks := mustParse(t, data)
	if _, err := chunks[0].Children(); err == nil {
		t.Error("expected error for chunk length overrunning the parent")
	}
}

func TestCompositeTooSmallForSecondary(t *testing.T) {
	// The FORM header declares a length that does not even cover the
	// secondary identifier. The header parses (the reference library
	// behaves the same at the top level) but descending fails.
	data := []byte("FORM\x00\x00\x00\x02\x00\x00")
	chunks := mustParse(t, data)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks", len(chunks))
	}
	if _, err := chunks[0].Children(); err == nil {
		t.Error("expected error descending into FORM with length < 4")
	}
}

func TestMagicSequencesSkipped(t *testing.T) {
	data := append([]byte("AT&T"), chunk("INFO", make([]byte, 10))...)
	// Also between chunks and the SDJV variant.
	second := chunk("Bg44", []byte{1})
	data = pad(data)
	data = append(data, []byte("SDJV")...)
	data = append(data, second...)
	chunks := mustParse(t, data)
	if len(chunks) != 2 || chunks[0].ID != "INFO" || chunks[1].ID != "Bg44" {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
}

func TestIllegalIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"control byte in id", chunk("\x01NVU", nil)},
		{"reserved FOR4", chunk("FOR4", nil)},
		{"reserved LIS2", chunk("LIS2", nil)},
		{"reserved CAT9", chunk("CAT9", nil)},
		{"composite secondary", form("FORM", nil)},
		{"reserved secondary", form("CAT2", nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseChunks(bytes.NewReader(tt.data)); err == nil {
				t.Error("expected identifier error")
			}
		})
	}
}

func TestLegalUnusualIdentifiers(t *testing.T) {
	// CAT0 is not in the reserved range (digit must be 1-9); lowercase
	// identifiers are fine; "LIST" is a valid composite.
	for _, id := range []string{"CAT0", "djvu", "LIST"} {
		data := chunk(id, nil)
		chunks := mustParse(t, data)
		if chunks[0].ID != id {
			t.Errorf("chunk id = %q, want %q", chunks[0].ID, id)
		}
	}
}

func TestEmptyInput(t *testing.T) {
	chunks, err := ParseChunks(bytes.NewReader(nil))
	if err != nil || len(chunks) != 0 {
		t.Fatalf("ParseChunks(empty) = %v, %v", chunks, err)
	}
}

func TestNonSeekableInput(t *testing.T) {
	data := form("DJVU", chunk("INFO", make([]byte, 10)))
	r := struct{ io.Reader }{bytes.NewReader(data)}
	chunks, err := ParseChunks(r)
	if err != nil {
		t.Fatalf("ParseChunks: %v", err)
	}
	if len(chunks) != 1 || chunks[0].FullID() != "FORM:DJVU" {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
	children, err := chunks[0].Children()
	if err != nil || len(children) != 1 {
		t.Fatalf("Children: %v, %+v", err, children)
	}
}

func TestIndependentPayloadReaders(t *testing.T) {
	data := pad(chunk("AAAA", []byte{1, 2, 3, 4}), chunk("BBBB", []byte{5, 6}))
	chunks := mustParse(t, data)
	r1 := chunks[0].Payload()
	r2 := chunks[1].Payload()
	b1 := make([]byte, 4)
	if _, err := io.ReadFull(r1, b1); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, []byte{1, 2, 3, 4}) {
		t.Fatalf("b1 = %v", b1)
	}
	b2, err := io.ReadAll(r2)
	if err != nil || !bytes.Equal(b2, []byte{5, 6}) {
		t.Fatalf("b2 = %v, %v", b2, err)
	}
}

func TestDepthLimit(t *testing.T) {
	// Build MaxDepth+10 nested FORMs.
	var data []byte
	for i := 0; i < MaxDepth+10; i++ {
		data = form("DJVU", data)
	}
	chunks := mustParse(t, data)
	depth := 0
	cur := chunks[0]
	for {
		children, err := cur.Children()
		if err != nil {
			if depth > MaxDepth-2 { // the error must be the depth limit
				return
			}
			t.Fatalf("unexpected error at depth %d: %v", depth, err)
		}
		if len(children) != 1 {
			t.Fatalf("expected 1 child at depth %d, got %d", depth, len(children))
		}
		cur = children[0]
		depth++
		if depth > MaxDepth+20 {
			t.Fatal("descended beyond the depth limit without an error")
		}
	}
}

func TestOffsetsAcrossLevels(t *testing.T) {
	inner := chunk("LEAF", []byte{9})
	mid := form("MIDX", pad(inner))
	outer := form("OTRX", pad(mid))
	chunks := mustParse(t, outer)
	var leaves []*Chunk
	walk(t, chunks, func(c *Chunk) {
		if c.ID == "LEAF" {
			leaves = append(leaves, c)
		}
	})
	if len(leaves) != 1 {
		t.Fatalf("got %d leaves", len(leaves))
	}
	leaf := leaves[0]
	// The leaf payload must be a single 0x09 byte at the recorded offset.
	r := leaf.Payload()
	b, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(b, []byte{9}) {
		t.Fatalf("leaf payload = %v, %v", b, err)
	}
	if want := int64(bytes.Index(outer, []byte("LEAF"))); leaf.Start != want {
		t.Errorf("leaf start = %d, want %d", leaf.Start, want)
	}
}
