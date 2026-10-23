package djvu

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

// readPNM parses a binary P5 or P6 file, returning its dimensions and
// its pixels as three bytes per pixel.
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
		if _, err := fmt.Sscanf(string(data[start:pos]), "%d", &fields[i]); err != nil {
			t.Fatalf("%s: bad PNM header: %v", path, err)
		}
	}
	w, h, maxval := fields[0], fields[1], fields[2]
	if maxval != 255 {
		t.Fatalf("%s: unsupported max value %d", path, maxval)
	}
	pos++
	raw := data[pos:]
	if data[1] == '5' {
		pix = make([]byte, 3*w*h)
		for i := 0; i < w*h; i++ {
			pix[3*i], pix[3*i+1], pix[3*i+2] = raw[i], raw[i], raw[i]
		}
	} else {
		pix = raw
	}
	return w, h, pix
}

// compareGolden checks an RGBA image against a PNM golden file and
// reports the first differing pixel.
func compareGolden(t *testing.T, img *image.RGBA, gold string) {
	t.Helper()
	w, h, pix := readPNM(t, gold)
	if w != img.Rect.Dx() || h != img.Rect.Dy() {
		t.Fatalf("%s: dimensions: got %dx%d want %dx%d", gold, img.Rect.Dx(), img.Rect.Dy(), w, h)
	}
	for i := 0; i < w*h; i++ {
		o := i * 4
		for k := 0; k < 3; k++ {
			if img.Pix[o+k] != pix[3*i+k] {
				t.Fatalf("%s: pixel (%d,%d): got %d,%d,%d want %d,%d,%d",
					gold, i%w, i/w,
					img.Pix[o], img.Pix[o+1], img.Pix[o+2],
					pix[3*i], pix[3*i+1], pix[3*i+2])
			}
		}
	}
}

// openTest opens a test document from disk.
func openTest(t *testing.T, path string) *Document {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestRenderPhoto renders a single page photo document (BG44 only)
// and compares with the ddjvu rendering.
func TestRenderPhoto(t *testing.T) {
	d := openTest(t, "internal/iw44/testdata/gray.djvu")
	img, err := d.Render(0, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, img, "internal/iw44/testdata/gray_bg.pgm")
}

// TestRenderCompound renders a compound document (Sjbz stencil + FGbz
// palette + reduced BG44 background), full and subsampled.
func TestRenderCompound(t *testing.T) {
	d := openTest(t, "internal/render/testdata/compound.djvu")
	img, err := d.Render(0, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, img, "internal/render/testdata/compound_full.ppm")
	img, err = d.Render(0, RenderOptions{Subsample: 4})
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, img, "internal/render/testdata/compound_s4.ppm")
}

// TestRenderThreeLayer renders a document with a color FG44
// foreground.
func TestRenderThreeLayer(t *testing.T) {
	d := openTest(t, "internal/render/testdata/threelayer.djvu")
	img, err := d.Render(0, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, img, "internal/render/testdata/threelayer_full.ppm")
}

// TestRenderBitonal renders a bilevel document (Sjbz only) as
// anti-aliased black on white and compares with ddjvu's color mode.
func TestRenderBitonal(t *testing.T) {
	gold := "testdata/render/bitonal.ppm"
	if _, err := os.Stat(gold); err != nil {
		t.Skipf("golden %s missing (render with ddjvu)", gold)
	}
	d := openTest(t, "testdata/render/bitonal.djvu")
	img, err := d.Render(0, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, img, gold)
}

// TestRenderBundled renders a page of a bundled multipage document
// with a shared dictionary included through INCL and compares with
// ddjvu. Uses the real world corpus when present.
func TestRenderBundled(t *testing.T) {
	if _, err := os.Stat("realdata/test2.djvu"); err != nil {
		t.Skip("realdata corpus not present")
	}
	gold := "/tmp/test2_p1.ppm"
	if _, err := os.Stat(gold); err != nil {
		t.Skipf("golden %s missing (render with ddjvu)", gold)
	}
	d := openTest(t, "realdata/test2.djvu")
	img, err := d.Render(0, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, img, gold)
}

// TestRenderCache checks that rendering the same page twice returns
// identical results (the decoded layers are cached).
func TestRenderCache(t *testing.T) {
	d := openTest(t, "internal/render/testdata/compound.djvu")
	a, err := d.Render(0, RenderOptions{Subsample: 2})
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Render(0, RenderOptions{Subsample: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("repeated render differs")
	}
}

// TestRenderPNG checks the PNG helper round trip.
func TestRenderPNG(t *testing.T) {
	d := openTest(t, "internal/iw44/testdata/gray.djvu")
	img, err := d.Render(0, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := d.RenderPNG(&buf, 0, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds() != img.Bounds() {
		t.Fatalf("png bounds: got %v want %v", decoded.Bounds(), img.Bounds())
	}
	got, ok := decoded.(*image.NRGBA)
	_ = got
	if !ok {
		// png.Decode may return other formats; compare via At.
		for y := 0; y < img.Bounds().Dy(); y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				r1, g1, b1, _ := img.At(x, y).RGBA()
				r2, g2, b2, _ := decoded.At(x, y).RGBA()
				if r1 != r2 || g1 != g2 || b1 != b2 {
					t.Fatalf("pixel (%d,%d) differs after png round trip", x, y)
				}
			}
		}
	}
}

// TestRenderTooLarge checks that oversized pages are rejected before
// any allocation. The fixture is a photo document whose INFO chunk
// is patched to claim a 30000 pixel wide page.
func TestRenderTooLarge(t *testing.T) {
	data, err := os.ReadFile("internal/iw44/testdata/gray.djvu")
	if err != nil {
		t.Fatal(err)
	}
	// Find the INFO chunk and patch its dimensions: the INFO payload
	// starts after the AT&T magic, the FORM header and the DJVU id.
	off := bytes.Index(data, []byte("INFO"))
	if off < 0 {
		t.Fatal("no INFO chunk in fixture")
	}
	patched := append([]byte(nil), data...)
	// INFO payload: width and height as big endian 16 bit values.
	patched[off+8] = 0x75
	patched[off+9] = 0x30 // 30000
	d, err := Open(bytes.NewReader(patched), int64(len(patched)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Render(0, RenderOptions{}); err == nil {
		t.Fatal("expected an error for a page exceeding the dimension limit")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRenderCorpus renders every page of every document in the real
// world corpus at a reduced resolution, checking only for errors and
// panics. It skips when the corpus directory is absent.
func TestRenderCorpus(t *testing.T) {
	entries, err := os.ReadDir("realdata")
	if err != nil {
		t.Skip("realdata corpus not present")
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".djvu") {
			continue
		}
		data, err := os.ReadFile("realdata/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		d, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue // indirect documents are not renderable yet
		}
		n, err := d.PageCount()
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			img, err := d.Render(i, RenderOptions{Subsample: 12})
			if err != nil {
				t.Errorf("%s page %d: %v", e.Name(), i, err)
				continue
			}
			if img == nil {
				t.Errorf("%s page %d: no image", e.Name(), i)
			}
		}
	}
}

// BenchmarkRender measures a full page render at native resolution and
// at a typical browsing subsampling.
func BenchmarkRender(b *testing.B) {
	data, err := os.ReadFile("internal/render/testdata/compound.djvu")
	if err != nil {
		b.Fatal(err)
	}
	d, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		b.Fatal(err)
	}
	b.Run("sub1", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := d.Render(0, RenderOptions{}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("sub4", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := d.Render(0, RenderOptions{Subsample: 4}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
