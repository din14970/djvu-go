package jb2

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"codeberg.org/din14970/djvu-go/iff"
)

func findChunks(chunks []*iff.Chunk, id string, out *[]*iff.Chunk) {
	for _, c := range chunks {
		if c.ID == id {
			*out = append(*out, c)
		}
		if c.Composite() {
			children, err := c.Children()
			if err == nil {
				findChunks(children, id, out)
			}
		}
	}
}

func TestSmokeReal(t *testing.T) {
	for _, name := range []string{"../../testdata/djvu/single.djvu", "../../realdata/test1.djvu"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Skip(err)
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
				t.Errorf("%s Djbz: %v", name, err)
				continue
			}
			fmt.Printf("%s: Djbz %d shapes\n", name, d.ShapeCount())
			if dict == nil {
				dict = d
			}
		}
		// Decode the first five pages, the first against the shared dict.
		pages := 0
		for _, c := range sjbz {
			if pages == 5 {
				break
			}
			pages++
			cb := func() *Dict { return dict }
			im, err := DecodeImage(c.Payload(), cb)
			if err != nil {
				t.Errorf("%s Sjbz page %d: %v", name, pages, err)
				continue
			}
			ink := 0
			bm := im.PageBitmap()
			for y := 0; y < bm.H; y++ {
				for x := 0; x < bm.W; x++ {
					if bm.Get(x, y) {
						ink++
					}
				}
			}
			fmt.Printf("%s: Sjbz page %d: %dx%d %d blits %d shapes ink=%d\n",
				name, pages, im.Width, im.Height, len(im.Blits), im.ShapeCount(), ink)
		}
	}
}
