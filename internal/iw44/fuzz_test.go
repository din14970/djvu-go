package iw44

import (
	"bytes"
	"os"
	"testing"

	"codeberg.org/din14970/djvu-go/iff"
)

// seedBG44 collects the BG44 chunk payloads of a fixture document.
func seedBG44(t testing.TB, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	chunks, err := iff.ParseChunks(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	children, err := chunks[0].Children()
	if err != nil {
		return nil
	}
	var out [][]byte
	for _, c := range children {
		if c.ID != "BG44" {
			continue
		}
		payload, err := c.ReadAll()
		if err == nil {
			out = append(out, payload)
		}
	}
	return out
}

// FuzzDecodeChunk feeds arbitrary data to the IW44 chunk decoder. The
// decoder must never panic; malformed chunks must return errors. When
// the decoded image is small, the full reconstruction runs as well.
func FuzzDecodeChunk(f *testing.F) {
	for _, path := range []string{
		"testdata/gray.djvu",
		"testdata/color.djvu",
		"testdata/small.djvu",
	} {
		for _, payload := range seedBG44(f, path) {
			f.Add(payload)
		}
	}
	// Minimal chunk: serial 0, one slice, v1.2 color, 1x1, no crcb delay.
	f.Add([]byte{0x00, 0x01, 0x01, 0x02, 0x00, 0x01, 0x00, 0x01, 0x00})
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xff}, 32))
	f.Fuzz(func(_ *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		// Skip inputs that declare very large images: decoding them
		// is legitimate but slow, which trips the fuzzing engine's
		// hang detection. The decoder logic is dimension independent.
		if len(data) >= 8 {
			w := int(data[4])<<8 | int(data[5])
			h := int(data[6])<<8 | int(data[7])
			if w > 512 || h > 512 {
				return
			}
			slices := int(data[1])
			if slices*w*h > 128<<20 {
				return
			}
		}
		img := NewColor()
		if _, err := img.DecodeChunk(bytes.NewReader(data)); err != nil {
			return
		}
		// Small images are reconstructed fully; larger declared sizes
		// would allocate huge buffers, which the dimension checks of
		// the public API reject anyway.
		if img.w > 0 && img.w <= 256 && img.h <= 256 {
			_ = img.Pixmap(1)
		}
	})
}
