package jb2

import (
	"bytes"
	"os"
	"testing"

	"github.com/din14970/djvu-go/iff"
)

// readPBM parses a binary PBM (P4) file into bitmap dimensions and
// packed rows, top-down, MSB first - the same layout as bitmap.Bitmap.
func readPBM(t *testing.T, path string) (w, h int, bits []byte, stride int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 2 || data[0] != 'P' || data[1] != '4' {
		t.Fatalf("%s: not a binary PBM", path)
	}
	pos := 2
	fields := make([]int, 0, 2)
	for len(fields) < 2 {
		for pos < len(data) && (data[pos] == ' ' || data[pos] == '\n' || data[pos] == '\t' || data[pos] == '\r') {
			pos++
		}
		if pos < len(data) && data[pos] == '#' {
			for pos < len(data) && data[pos] != '\n' {
				pos++
			}
			continue
		}
		v := 0
		for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
			v = v*10 + int(data[pos]-'0')
			pos++
		}
		fields = append(fields, v)
	}
	// A single whitespace character separates the header from the data.
	pos++
	w, h = fields[0], fields[1]
	stride = (w + 7) >> 3
	if len(data)-pos < h*stride {
		t.Fatalf("%s: truncated PBM data", path)
	}
	return w, h, data[pos:], stride
}

// compareAgainstPBM decodes the Sjbz chunk of a single-page document and
// compares the composited page mask against a golden PBM rendered by
// ddjvu -mode=black.
func TestGoldenBlackSingle(t *testing.T) {
	data, err := os.ReadFile("../../testdata/djvu/single.djvu")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("../../testdata/render/single_black.pbm")
	if err != nil {
		t.Skipf("golden rendering missing: %v", err)
	}
	_ = golden
	chunks, err := iff.ParseChunks(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	children, err := chunks[0].Children()
	if err != nil {
		t.Fatal(err)
	}
	var sjbz *iff.Chunk
	for _, c := range children {
		if c.ID == "Sjbz" {
			sjbz = c
		}
	}
	if sjbz == nil {
		t.Fatal("no Sjbz chunk")
	}
	im, err := DecodeImage(sjbz.Payload(), nil)
	if err != nil {
		t.Fatal(err)
	}
	page := im.PageBitmap()
	w, h, bits, stride := readPBM(t, "../../testdata/render/single_black.pbm")
	if w != im.Width || h != im.Height {
		t.Fatalf("dimensions differ: decoded %dx%d, golden %dx%d", im.Width, im.Height, w, h)
	}
	if stride != page.Stride {
		t.Fatalf("stride differs: %d vs %d", page.Stride, stride)
	}
	if !bytes.Equal(page.Bits, bits[:h*stride]) {
		n := 0
		for i := range page.Bits {
			if page.Bits[i] != bits[i] {
				n++
			}
		}
		t.Errorf("page mask differs from ddjvu rendering in %d of %d bytes", n, len(page.Bits))
	}
}
