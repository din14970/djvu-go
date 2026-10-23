package render

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"testing"

	"codeberg.org/din14970/djvu-go/iff"
	"codeberg.org/din14970/djvu-go/internal/iw44"
	"codeberg.org/din14970/djvu-go/internal/jb2"
	"codeberg.org/din14970/djvu-go/internal/palette"
	"codeberg.org/din14970/djvu-go/internal/pixmap"
)

// readPNM parses a binary P5 or P6 file and returns its dimensions
// and pixels as three bytes per pixel.
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
	w, h, max := fields[0], fields[1], fields[2]
	if max != 255 {
		t.Fatalf("%s: unsupported max value %d", path, max)
	}
	pos++
	raw := data[pos:]
	switch data[1] {
	case '5':
		pix = make([]byte, 3*w*h)
		for i := 0; i < w*h; i++ {
			pix[3*i], pix[3*i+1], pix[3*i+2] = raw[i], raw[i], raw[i]
		}
	default:
		pix = raw
	}
	return w, h, pix
}

// toRGBA converts a bottom up pixmap to a top down RGBA image.
func toRGBA(pm *pixmap.Pixmap) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, pm.W, pm.H))
	for y := 0; y < pm.H; y++ {
		src := (pm.H - 1 - y) * pm.W
		for x := 0; x < pm.W; x++ {
			o := y*out.Stride + x*4
			out.Pix[o] = pm.Pix[(src+x)*3]
			out.Pix[o+1] = pm.Pix[(src+x)*3+1]
			out.Pix[o+2] = pm.Pix[(src+x)*3+2]
			out.Pix[o+3] = 0xff
		}
	}
	return out
}

// loadLayers decodes the layers of a single page compound fixture.
func loadLayers(t *testing.T, path string) *Layers {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := iff.ParseChunks(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	children, err := chunks[0].Children()
	if err != nil {
		t.Fatal(err)
	}
	l := &Layers{}
	for _, c := range children {
		switch c.ID {
		case "Sjbz":
			im, err := jb2.DecodeImage(c.Payload(), nil)
			if err != nil {
				t.Fatalf("%s: %v", c.ID, err)
			}
			l.Mask = im
			l.Width, l.Height = im.Width, im.Height
		case "FGbz":
			pal, err := palette.Decode(c.Payload())
			if err != nil {
				t.Fatal(err)
			}
			l.Pal = pal
		case "BG44":
			if l.BGImage == nil {
				l.BGImage = iw44.NewColor()
			}
			if _, err := l.BGImage.(*iw44.Image).DecodeChunk(c.Payload()); err != nil {
				t.Fatal(err)
			}
		case "FG44":
			fg := iw44.NewColor()
			if _, err := fg.DecodeChunk(c.Payload()); err != nil {
				t.Fatal(err)
			}
			l.FGPixmap = fg.Pixmap(1)
		}
	}
	return l
}

// TestGoldenCompound composites the compound fixture (JB2 stencil +
// FGbz palette + reduced BG44 background) and compares the result
// byte for byte with the ddjvu rendering.
func TestGoldenCompound(t *testing.T) {
	l := loadLayers(t, "testdata/compound.djvu")
	if l.Mask == nil || l.Pal == nil || l.BGImage == nil {
		t.Fatal("fixture is missing layers")
	}
	pm := Background(l, 1)
	Stencil(l, pm, 1)
	out := toRGBA(pm)
	w, h, gold := readPNM(t, "testdata/compound_full.ppm")
	if w != out.Rect.Dx() || h != out.Rect.Dy() {
		t.Fatalf("dimensions: got %dx%d want %dx%d", out.Rect.Dx(), out.Rect.Dy(), w, h)
	}
	diffs := 0
	first := ""
	for i := 0; i < w*h; i++ {
		o := i * 4
		for k := 0; k < 3; k++ {
			if out.Pix[o+k] != gold[3*i+k] {
				diffs++
				if first == "" {
					first = fmt.Sprintf("pixel (%d,%d): got %d,%d,%d want %d,%d,%d",
						i%w, i/w,
						out.Pix[o], out.Pix[o+1], out.Pix[o+2],
						gold[3*i], gold[3*i+1], gold[3*i+2])
				}
			}
		}
	}
	if diffs > 0 {
		t.Errorf("composite differs from ddjvu in %d bytes; first: %s", diffs, first)
	}
}

// TestGoldenCompoundSubsample composites the compound fixture at
// several subsampling factors and compares each result with the ddjvu
// rendering of the same reduction. This exercises the bilinear
// background scaler, the direct reduction paths and the alpha maps of
// every factor. The reference only renders integral reductions up to
// 15, so the list stops at 12.
func TestGoldenCompoundSubsample(t *testing.T) {
	l := loadLayers(t, "testdata/compound.djvu")
	for _, sub := range []int{2, 3, 4, 6, 8, 12} {
		pm := Background(l, sub)
		Stencil(l, pm, sub)
		out := toRGBA(pm)
		goldPath := fmt.Sprintf("testdata/compound_s%d.ppm", sub)
		w, h, gold := readPNM(t, goldPath)
		if w != out.Rect.Dx() || h != out.Rect.Dy() {
			t.Errorf("sub %d: dimensions: got %dx%d want %dx%d", sub, out.Rect.Dx(), out.Rect.Dy(), w, h)
			continue
		}
		diffs := 0
		for i := 0; i < w*h; i++ {
			o := i * 4
			for k := 0; k < 3; k++ {
				if out.Pix[o+k] != gold[3*i+k] {
					diffs++
				}
			}
		}
		if diffs > 0 {
			t.Errorf("sub %d: composite differs from ddjvu in %d of %d bytes", sub, diffs, w*h*3)
		}
	}
}

// comparePNM checks the composite against a golden PNM and reports
// the number of differing bytes.
func comparePNM(t *testing.T, pm interface {
	At(x, y int) (byte, byte, byte)
}, w, h int, gold []byte) (diffs int, first string) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b := pm.At(x, y)
			o := (y*w + x) * 3
			if r != gold[o] || g != gold[o+1] || b != gold[o+2] {
				diffs += 3
				if first == "" {
					first = fmt.Sprintf("pixel (%d,%d): got %d,%d,%d want %d,%d,%d",
						x, y, r, g, b, gold[o], gold[o+1], gold[o+2])
				}
			}
		}
	}
	return diffs, first
}

// TestGoldenThreeLayer composites the three layer fixture (JB2 stencil
// + FG44 color foreground + full size BG44 background) and compares
// with ddjvu at several subsampling factors. This exercises the
// supersampling foreground paths of the three layer model.
func TestGoldenThreeLayer(t *testing.T) {
	for _, sub := range []int{1, 2, 4, 8} {
		l := loadLayers(t, "testdata/threelayer.djvu")
		if l.Mask == nil || l.FGPixmap == nil || l.BGImage == nil {
			t.Fatal("fixture is missing layers")
		}
		pm := Background(l, sub)
		Stencil(l, pm, sub)
		goldPath := "testdata/threelayer_full.ppm"
		if sub > 1 {
			goldPath = fmt.Sprintf("testdata/threelayer_s%d.ppm", sub)
		}
		w, h, gold := readPNM(t, goldPath)
		if w != pm.W || h != pm.H {
			t.Errorf("sub %d: dimensions: got %dx%d want %dx%d", sub, pm.W, pm.H, w, h)
			continue
		}
		// Wrap the bottom up pixmap for the top down comparison.
		top := topDown(pm)
		diffs, first := comparePNM(t, top, w, h, gold)
		if diffs > 0 {
			t.Errorf("sub %d: composite differs from ddjvu in %d bytes; first: %s", sub, diffs, first)
		}
	}
}

// topDown adapts a bottom up pixmap to top down pixel access.
type topDownPix struct{ pm *pixmap.Pixmap }

func topDown(pm *pixmap.Pixmap) *topDownPix { return &topDownPix{pm: pm} }

func (t *topDownPix) At(x, y int) (byte, byte, byte) {
	return t.pm.At(x, t.pm.H-1-y)
}
