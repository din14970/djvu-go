package djvu

import (
	"bytes"
	"os"
	"testing"
)

// FuzzOpenRender feeds arbitrary data to the whole document pipeline:
// parsing, page access, text, annotations, metadata and rendering.
// Nothing may panic; malformed files must produce errors.
func FuzzOpenRender(f *testing.F) {
	for _, path := range []string{
		"internal/iw44/testdata/gray.djvu",
		"internal/render/testdata/compound.djvu",
		"internal/render/testdata/threelayer.djvu",
		"testdata/render/bitonal.djvu",
	} {
		data, err := os.ReadFile(path)
		if err == nil {
			f.Add(data)
		}
	}
	f.Add([]byte{})
	f.Add([]byte("AT&T"))
	f.Add([]byte("FORM\x00\x00\x00\x0cDJVUDIRM\x00\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		d, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		_, _ = d.Pages()
		_, _ = d.Metadata()
		n, err := d.PageCount()
		if err != nil {
			return
		}
		for i := 0; i < n && i < 16; i++ {
			_, _ = d.PageText(i)
			_, _ = d.Annotations(i)
			// Render at a heavy subsampling to keep the fuzz fast; the
			// pipeline exercises every decoder on the page.
			_, _ = d.Render(i, RenderOptions{Subsample: 16})
		}
	})
}
