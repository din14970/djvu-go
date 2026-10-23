package djvu

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSecurityPoCs replays the proof of concept files for the two
// security issues opened against djvulibre (upstream issues #1 and #2,
// both against the IW44 decoder):
//
//  1. A crafted IW44 stream declaring 65535x65535 overflows the 32 bit
//     block count in the reference's Map constructor; nb wraps to zero
//     and the decoder writes through null block pointers (CWE-190,
//     CWE-476). Unpatched djvulibre (< 3.5.30) segfaults on it.
//  2. A stream declaring a 0x0 image divides by zero during
//     reconstruction (CWE-369). Unpatched djvulibre segfaults on it.
//
// Both must be rejected with an error here. The guards live in
// internal/iw44.initMap: dimensions are validated (positive and within
// the limit) before any allocation, and the block count is computed in
// 64 bit integers, so the overflow of the reference cannot occur.
func TestSecurityPoCs(t *testing.T) {
	files, err := filepath.Glob("testdata/security/*.djvu")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no security fixtures found")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Logf("%s: rejected at parse: %v", filepath.Base(f), err)
			continue
		}
		if _, err := d.Render(0, RenderOptions{}); err == nil {
			t.Errorf("%s: corrupt file rendered without error", filepath.Base(f))
		} else {
			t.Logf("%s: rejected at render: %v", filepath.Base(f), err)
		}
	}
}
