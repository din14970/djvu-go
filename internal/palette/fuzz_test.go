package palette

import (
	"bytes"
	"os"
	"testing"

	"github.com/din14970/djvu-go/iff"
)

// seedFGbz collects the FGbz chunk payloads of a fixture document.
func seedFGbz(t testing.TB, path string) [][]byte {
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
		if c.ID != "FGbz" {
			continue
		}
		payload, err := c.ReadAll()
		if err == nil {
			out = append(out, payload)
		}
	}
	return out
}

// FuzzDecode feeds arbitrary data to the FGbz decoder. The decoder
// must never panic and must reject malformed chunks with errors.
func FuzzDecode(f *testing.F) {
	for _, payload := range seedFGbz(f, "../render/testdata/compound.djvu") {
		f.Add(payload)
	}
	f.Add([]byte{0x00})                               // empty palette, no color data
	f.Add([]byte{0x80, 0x00, 0x00})                   // color data flag, no size
	f.Add([]byte{0x80, 0x00, 0x02, 0x00, 0x00, 0x01}) // one color, huge data
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xff}, 16))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		p, err := Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		for i := 0; i < p.Size(); i++ {
			_ = p.Color(i)
		}
		for _, idx := range p.Index {
			if idx < 0 || idx >= p.Size() {
				t.Fatalf("color index %d out of range (palette size %d)", idx, p.Size())
			}
		}
	})
}
