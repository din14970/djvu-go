package iw44

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"codeberg.org/din14970/djvu-go/iff"
)

// loadBG44 reads the BG44 chunks of the first FORM:DJVU in a single
// page DjVu document.
func loadBG44(t *testing.T, path string) []*iff.Chunk {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := iff.ParseChunks(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	children, err := chunks[0].Children()
	if err != nil {
		t.Fatal(err)
	}
	var out []*iff.Chunk
	for _, c := range children {
		if c.ID == "BG44" {
			out = append(out, c)
		}
	}
	return out
}

// readPNM parses a binary P5 or P6 file and returns its dimensions and
// pixel data with 3 bytes per pixel.
func readPNM(t *testing.T, path string) (w, h int, pix []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 2 || data[0] != 'P' || (data[1] != '5' && data[1] != '6') {
		t.Fatalf("%s: not a binary PNM file", path)
	}
	fields := make([]int, 3)
	pos := 2
	for i := 0; i < 3; i++ {
		for pos < len(data) && (data[pos] == ' ' || data[pos] == '\n' || data[pos] == '\t' || data[pos] == '\r') {
			pos++
		}
		if pos < len(data) && data[pos] == '#' {
			for pos < len(data) && data[pos] != '\n' {
				pos++
			}
			i--
			continue
		}
		start := pos
		for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
			pos++
		}
		if start == pos {
			t.Fatalf("%s: truncated PNM header", path)
		}
		_, err := fmt.Sscanf(string(data[start:pos]), "%d", &fields[i])
		if err != nil {
			t.Fatalf("%s: bad PNM header: %v", path, err)
		}
	}
	w, h, max := fields[0], fields[1], fields[2]
	if max != 255 {
		t.Fatalf("%s: unsupported max value %d", path, max)
	}
	pos++ // single whitespace after max
	raw := data[pos:]
	switch data[1] {
	case '5':
		if len(raw) < w*h {
			t.Fatalf("%s: truncated pixel data", path)
		}
		pix = make([]byte, 3*w*h)
		for i := 0; i < w*h; i++ {
			pix[3*i], pix[3*i+1], pix[3*i+2] = raw[i], raw[i], raw[i]
		}
	default:
		if len(raw) < 3*w*h {
			t.Fatalf("%s: truncated pixel data", path)
		}
		pix = raw
	}
	return w, h, pix
}

// TestGoldenGray renders the gray.djvu fixture (a 320x240 gray image
// stored in a color IW44 container) and compares the result byte for
// byte against the ddjvu rendering of the same file.
func TestGoldenGray(t *testing.T) {
	chunks := loadBG44(t, "testdata/gray.djvu")
	if len(chunks) != 3 {
		t.Fatalf("expected 3 BG44 chunks, got %d", len(chunks))
	}
	img := NewColor()
	for _, c := range chunks {
		if _, err := img.DecodeChunk(c.Payload()); err != nil {
			t.Fatal(err)
		}
	}
	if img.Width() != 320 || img.Height() != 240 {
		t.Fatalf("dimensions: got %dx%d want 320x240", img.Width(), img.Height())
	}
	out := img.RGBA(1)
	if out == nil {
		t.Fatal("no image rendered")
	}
	w, h, gold := readPNM(t, "testdata/gray_bg.pgm")
	if w != out.Rect.Dx() || h != out.Rect.Dy() {
		t.Fatalf("dimensions: got %dx%d want %dx%d", out.Rect.Dx(), out.Rect.Dy(), w, h)
	}
	for i := 0; i < w*h; i++ {
		o := i * 4
		for k := 0; k < 3; k++ {
			if out.Pix[o+k] != gold[3*i+k] {
				t.Fatalf("pixel (%d,%d): got %d,%d,%d want %d,%d,%d",
					i%w, i/w,
					out.Pix[o], out.Pix[o+1], out.Pix[o+2],
					gold[3*i], gold[3*i+1], gold[3*i+2])
			}
		}
	}
}

// TestDecodeChunkSerial verifies that out of order chunks are rejected
// by the serial number check, and that a decode of all chunks is
// deterministic across runs.
func TestDecodeChunkSerial(t *testing.T) {
	chunks := loadBG44(t, "testdata/gray.djvu")
	img := NewColor()
	// Feeding chunk 2 first must fail the serial check.
	if _, err := img.DecodeChunk(chunks[1].Payload()); err == nil {
		t.Fatal("expected serial mismatch error")
	}
	// Feeding chunk 1 twice must fail the second time.
	img = NewColor()
	if _, err := img.DecodeChunk(chunks[0].Payload()); err != nil {
		t.Fatal(err)
	}
	if _, err := img.DecodeChunk(chunks[0].Payload()); err == nil {
		t.Fatal("expected serial mismatch error")
	}
	// A full decode must produce identical pixels twice.
	render := func() *[4 * 320 * 240]byte {
		im := NewColor()
		for _, c := range chunks {
			if _, err := im.DecodeChunk(c.Payload()); err != nil {
				t.Fatal(err)
			}
		}
		out := im.RGBA(1)
		return (*[4 * 320 * 240]byte)(out.Pix)
	}
	a, b := render(), render()
	if *a != *b {
		t.Fatal("decode is not deterministic")
	}
}

// TestGoldenColor renders the color.djvu fixture (a 320x240 RGB image
// encoded with luma and chroma) and compares the result byte for byte
// against the ddjvu rendering of the same file.
func TestGoldenColor(t *testing.T) {
	chunks := loadBG44(t, "testdata/color.djvu")
	if len(chunks) != 3 {
		t.Fatalf("expected 3 BG44 chunks, got %d", len(chunks))
	}
	img := NewColor()
	for _, c := range chunks {
		if _, err := img.DecodeChunk(c.Payload()); err != nil {
			t.Fatal(err)
		}
	}
	out := img.RGBA(1)
	if out == nil {
		t.Fatal("no image rendered")
	}
	w, h, gold := readPNM(t, "testdata/color_bg.ppm")
	if w != out.Rect.Dx() || h != out.Rect.Dy() {
		t.Fatalf("dimensions: got %dx%d want %dx%d", out.Rect.Dx(), out.Rect.Dy(), w, h)
	}
	for i := 0; i < w*h; i++ {
		o := i * 4
		for k := 0; k < 3; k++ {
			if out.Pix[o+k] != gold[3*i+k] {
				t.Fatalf("pixel (%d,%d): got %d,%d,%d want %d,%d,%d",
					i%w, i/w,
					out.Pix[o], out.Pix[o+1], out.Pix[o+2],
					gold[3*i], gold[3*i+1], gold[3*i+2])
			}
		}
	}
}

// TestGoldenSmall renders the small.djvu fixture (100x75, a size that
// is not a multiple of the 32 pixel block size) and compares against
// ddjvu. This guards the border handling of the lifting filters.
func TestGoldenSmall(t *testing.T) {
	chunks := loadBG44(t, "testdata/small.djvu")
	if len(chunks) != 3 {
		t.Fatalf("expected 3 BG44 chunks, got %d", len(chunks))
	}
	img := NewColor()
	for _, c := range chunks {
		if _, err := img.DecodeChunk(c.Payload()); err != nil {
			t.Fatal(err)
		}
	}
	if img.Width() != 100 || img.Height() != 75 {
		t.Fatalf("dimensions: got %dx%d want 100x75", img.Width(), img.Height())
	}
	out := img.RGBA(1)
	w, h, gold := readPNM(t, "testdata/small_bg.pgm")
	if w != out.Rect.Dx() || h != out.Rect.Dy() {
		t.Fatalf("dimensions: got %dx%d want %dx%d", out.Rect.Dx(), out.Rect.Dy(), w, h)
	}
	for i := 0; i < w*h; i++ {
		o := i * 4
		for k := 0; k < 3; k++ {
			if out.Pix[o+k] != gold[3*i+k] {
				t.Fatalf("pixel (%d,%d): got %d,%d,%d want %d,%d,%d",
					i%w, i/w,
					out.Pix[o], out.Pix[o+1], out.Pix[o+2],
					gold[3*i], gold[3*i+1], gold[3*i+2])
			}
		}
	}
}
