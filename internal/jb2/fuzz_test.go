package jb2

import (
	"bytes"
	"os"
	"testing"

	"github.com/din14970/djvu-go/iff"
)

// seedChunks reads the Sjbz and Djbz chunk payloads of a document to
// seed the fuzz targets with valid streams.
func seedChunks(t testing.TB, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	chunks, err := iff.ParseChunks(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	var found []*iff.Chunk
	findChunks(chunks, "Sjbz", &found)
	findChunks(chunks, "Djbz", &found)
	var out [][]byte
	for _, c := range found {
		payload, err := c.ReadAll()
		if err == nil {
			out = append(out, payload)
		}
	}
	return out
}

// FuzzDecodeImage feeds arbitrary data to the Sjbz decoder and walks
// the decoded result. Decoding must never panic; corrupt streams must
// return errors instead.
func FuzzDecodeImage(f *testing.F) {
	for _, path := range []string{
		"../../testdata/djvu/single.djvu",
		"../../internal/render/testdata/compound.djvu",
	} {
		for _, payload := range seedChunks(f, path) {
			f.Add(payload)
		}
	}
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add(bytes.Repeat([]byte{0xff}, 64))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		im, err := DecodeImage(bytes.NewReader(data), nil)
		if err != nil {
			return
		}
		// Access every shape; the dimensions of corrupt streams can be
		// large but rows are allocated lazily, so this must stay cheap.
		for i := 0; i < im.ShapeCount(); i++ {
			s, err := im.Shape(i)
			if err != nil {
				t.Fatalf("shape %d: %v", i, err)
			}
			if s == nil {
				t.Fatalf("shape %d is nil", i)
			}
			h := s.ShapeHeight()
			w := s.ShapeWidth()
			if w > 1<<20 || h > 1<<20 {
				continue
			}
			for y := 0; y < h; y++ {
				_ = s.ShapeRow(y)
			}
		}
	})
}

// FuzzDecodeDict feeds arbitrary data to the Djbz decoder.
func FuzzDecodeDict(f *testing.F) {
	for _, path := range []string{
		"../../testdata/djvu/single.djvu",
		"../../realdata/test1.djvu",
	} {
		for _, payload := range seedChunks(f, path) {
			f.Add(payload)
		}
	}
	f.Add([]byte{})
	f.Add([]byte{0x01, 0x00, 0x00, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		d, err := DecodeDict(bytes.NewReader(data), nil)
		if err != nil {
			return
		}
		for i := 0; i < d.ShapeCount(); i++ {
			s, err := d.Shape(i)
			if err != nil {
				t.Fatalf("shape %d: %v", i, err)
			}
			if s == nil {
				t.Fatalf("shape %d is nil", i)
			}
		}
	})
}

// TestDecodeDictGarbage replays the fuzz finding that made the
// decoder panic: a shape record before the start of data record used
// to wipe the library and drive the bounding box loop out of range.
func TestDecodeDictGarbage(t *testing.T) {
	if _, err := DecodeDict(bytes.NewReader([]byte("VC0")), nil); err == nil {
		t.Fatal("expected an error for garbage dictionary data")
	}
	if _, err := DecodeImage(bytes.NewReader([]byte("VC0")), nil); err == nil {
		t.Fatal("expected an error for garbage image data")
	}
}
