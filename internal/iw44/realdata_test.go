package iw44

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/din14970/djvu-go/iff"
)

// walkForms visits all chunks reachable below cs, calling fn on each.
func walkForms(cs []*iff.Chunk, fn func(c *iff.Chunk)) {
	for _, c := range cs {
		fn(c)
		if kids, err := c.Children(); err == nil && kids != nil {
			walkForms(kids, fn)
		}
	}
}

// TestRealdataSmoke decodes the BG44 chunks of every real world
// document found in realdata/ (skipped when the directory is absent).
// The check is for errors and panics only: the reference decoder
// accepts all of these files, so must this one.
func TestRealdataSmoke(t *testing.T) {
	dir := "../../realdata"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skip("realdata directory not present")
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".djvu" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := iff.ParseChunks(bytes.NewReader(data))
		if err != nil {
			continue // indirect documents are not handled by the iff walker
		}
		// Decode each page (FORM:DJVU) into a fresh image. Top level
		// single page documents are decoded as one image.
		decode := func(cs []*iff.Chunk) {
			img := NewColor()
			n := 0
			for _, c := range cs {
				if c.ID != "BG44" {
					continue
				}
				if _, err := img.DecodeChunk(c.Payload()); err != nil {
					t.Errorf("%s: BG44 chunk %d: %v", e.Name(), n, err)
				}
				n++
			}
			if n > 0 {
				if out := img.RGBA(1); out == nil {
					t.Errorf("%s: no image rendered", e.Name())
				} else {
					fmt.Printf("%s: %d BG44 chunks, %dx%d\n", e.Name(), n, img.Width(), img.Height())
				}
			}
		}
		if chunks[0].FullID() == "FORM:DJVU" {
			kids, err := chunks[0].Children()
			if err == nil {
				decode(kids)
			}
		}
		walkForms(chunks, func(c *iff.Chunk) {
			if c.FullID() != "FORM:DJVU" {
				return
			}
			kids, err := c.Children()
			if err != nil {
				return
			}
			decode(kids)
		})
	}
}
