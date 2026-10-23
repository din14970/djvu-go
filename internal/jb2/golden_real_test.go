package jb2

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/din14970/djvu-go/iff"
)

// TestGoldenBlackRealdata compares decoded page masks against ddjvu
// renderings of the real-world corpus, when the corpus is present.
func TestGoldenBlackRealdata(t *testing.T) {
	if _, err := os.Stat("../../realdata"); err != nil {
		t.Skip("realdata corpus not present")
	}
	data, err := os.ReadFile("../../realdata/test1.djvu")
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := iff.ParseChunks(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var djbz, sjbz []*iff.Chunk
	findChunks(chunks, "Djbz", &djbz)
	findChunks(chunks, "Sjbz", &sjbz)
	var dict *Dict
	for _, c := range djbz {
		d, err := DecodeDict(c.Payload(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if dict == nil {
			dict = d
		}
	}
	// The first pages use the first dictionary; ddjvu pages are 1-based.
	for i, c := range sjbz[:4] {
		im, err := DecodeImage(c.Payload(), func() *Dict { return dict })
		if err != nil {
			t.Fatalf("page %d: %v", i+1, err)
		}
		page := im.PageBitmap()
		goldenPath := fmt.Sprintf("/tmp/test1_p%d.pbm", i+1)
		if _, err := os.Stat(goldenPath); err != nil {
			t.Skipf("golden %s missing (render with ddjvu)", goldenPath)
		}
		w, h, bits, stride := readPBM(t, goldenPath)
		if w != im.Width || h != im.Height {
			t.Errorf("page %d: dimensions differ: %dx%d vs %dx%d", i+1, im.Width, im.Height, w, h)
			continue
		}
		if !bytes.Equal(page.Bits, bits[:h*stride]) {
			n := 0
			for j := range page.Bits {
				if page.Bits[j] != bits[j] {
					n++
				}
			}
			t.Errorf("page %d: mask differs from ddjvu in %d of %d bytes", i+1, n, len(page.Bits))
		} else {
			t.Logf("page %d: identical to ddjvu (%d bytes)", i+1, len(page.Bits))
		}
	}
}
