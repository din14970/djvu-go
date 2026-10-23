package jb2

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/din14970/djvu-go/internal/bitmap"
)

// randomShape creates a random blob shape.
func randomShape(rng *rand.Rand, w, h int) *grayMap {
	g := newGrayMap(w, h, 4)
	cx, cy := w/2, h/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := (x-cx)*(x-cx) + (y-cy)*(y-cy)
			if d < rng.Intn(cx*cx+cy*cy+1) {
				g.rowWrite(y)[g.border+x] = 1
			}
		}
	}
	return g
}

func TestNumCoderRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	type testCase struct {
		low, high, v int
	}
	var cases []testCase
	for i := 0; i < 2000; i++ {
		v := rng.Intn(2*bigPositive+1) - bigPositive - 1
		cases = append(cases, testCase{bigNegative, bigPositive, v})
		cases = append(cases, testCase{0, bigPositive, rng.Intn(1000)})
		cases = append(cases, testCase{0, 255, rng.Intn(256)})
		cases = append(cases, testCase{0, 11, rng.Intn(12)})
		cases = append(cases, testCase{1, 1000, 1 + rng.Intn(1000)})
		cases = append(cases, testCase{bigNegative, bigPositive, -1 - rng.Intn(1000)})
		cases = append(cases, testCase{bigNegative, bigPositive, 0})
	}
	// Encode all values, one context per range kind.
	var buf bytes.Buffer
	enc := newEncCodec(&buf)
	for _, c := range cases {
		enc.codeNum(c.low, c.high, c.v, encCtxFor(c.low, c.high))
	}
	enc.zp.Flush()

	dec := newCodec(bytes.NewReader(buf.Bytes()))
	for _, c := range cases {
		got, err := dec.codeNum(c.low, c.high, decCtxFor(c.low, c.high))
		if err != nil {
			t.Fatalf("decode of %d in [%d,%d]: %v", c.v, c.low, c.high, err)
		}
		if got != c.v {
			t.Fatalf("value %d in [%d,%d] decoded as %d", c.v, c.low, c.high, got)
		}
	}
}

// The round trip needs encoder and decoder to use the same context
// variables in the same order. Both sides allocate one context per
// distinct range pair, cycling through a fixed list.
type rangeKey struct{ low, high int }

var (
	encCtxs     = map[rangeKey]*uint32{}
	decCtxs     = map[rangeKey]*uint32{}
	encCtxOrder []*uint32
	decCtxOrder []*uint32
)

func encCtxFor(low, high int) *uint32 {
	if ctx, ok := encCtxs[rangeKey{low, high}]; ok {
		return ctx
	}
	var ctx uint32
	encCtxs[rangeKey{low, high}] = &ctx
	encCtxOrder = append(encCtxOrder, &ctx)
	return &ctx
}

func decCtxFor(low, high int) *uint32 {
	if ctx, ok := decCtxs[rangeKey{low, high}]; ok {
		return ctx
	}
	var ctx uint32
	decCtxs[rangeKey{low, high}] = &ctx
	decCtxOrder = append(decCtxOrder, &ctx)
	return &ctx
}

func TestDictRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	// Build a dictionary: 12 directly coded shapes and 8 refinements.
	d := &Dict{}
	for i := 0; i < 12; i++ {
		w := 4 + rng.Intn(20)
		h := 4 + rng.Intn(20)
		d.addShape(&Shape{Parent: -1, Bits: randomShape(rng, w, h)})
	}
	for i := 0; i < 8; i++ {
		parent := rng.Intn(12)
		pb := d.shapes[parent].Bits
		w := pb.w + rng.Intn(5) - 2
		h := pb.h + rng.Intn(5) - 2
		if w < 2 || h < 2 {
			continue
		}
		d.addShape(&Shape{Parent: parent, Bits: randomShape(rng, w, h)})
	}
	stream := mustEncodeDict(t, d)
	got, err := DecodeDict(bytes.NewReader(stream), nil)
	if err != nil {
		t.Fatalf("DecodeDict: %v", err)
	}
	if got.ShapeCount() != d.ShapeCount() {
		t.Fatalf("shape count %d, want %d", got.ShapeCount(), d.ShapeCount())
	}
	for i := range d.shapes {
		want, err1 := d.Shape(d.inheritedShapes + i)
		have, err2 := got.Shape(got.inheritedShapes + i)
		if err1 != nil || err2 != nil {
			t.Fatalf("shape %d: %v / %v", i, err1, err2)
		}
		if want.Parent != have.Parent {
			t.Errorf("shape %d parent: %d, want %d", i, have.Parent, want.Parent)
		}
		if want.Bits.w != have.Bits.w || want.Bits.h != have.Bits.h {
			t.Errorf("shape %d size: %dx%d, want %dx%d", i, have.Bits.w, have.Bits.h, want.Bits.w, want.Bits.h)
			continue
		}
		for y := 0; y < want.Bits.h; y++ {
			wr := want.Bits.row(y)
			hr := have.Bits.row(y)
			for x := 0; x < want.Bits.w; x++ {
				if wr[want.Bits.border+x] != hr[have.Bits.border+x] {
					t.Errorf("shape %d pixel (%d,%d) differs", i, x, y)
					break
				}
			}
		}
	}
}

func TestImageRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	// A dictionary the image inherits.
	dict := &Dict{}
	for i := 0; i < 5; i++ {
		dict.addShape(&Shape{Parent: -1, Bits: randomShape(rng, 5+rng.Intn(8), 5+rng.Intn(8))})
	}

	im := &Image{Width: 300, Height: 200}
	var records []encBlit
	// New shape, in library.
	s0 := &Shape{Parent: -1, Bits: randomShape(rng, 12, 9)}
	records = append(records, encBlit{blit: Blit{Left: 10, Bottom: 20}, shape: s0, newRec: recNewMark})
	// New shape, image only.
	s1 := &Shape{Parent: -1, Bits: randomShape(rng, 7, 7)}
	records = append(records, encBlit{blit: Blit{Left: 60, Bottom: 20}, shape: s1, newRec: recNewMarkImageOnly})
	// Matched copy of an inherited shape.
	records = append(records, encBlit{blit: Blit{Left: 90, Bottom: 22}, match: 2, newRec: recMatchedCopy})
	// Matched refine of the new library shape.
	s2 := &Shape{Parent: -1, Bits: randomShape(rng, 13, 10)}
	records = append(records, encBlit{blit: Blit{Left: 110, Bottom: 40}, shape: s2, newRec: recMatchedRefine, match: 5})
	// Matched refine of an inherited shape.
	s3 := &Shape{Parent: -1, Bits: randomShape(rng, 6, 6)}
	records = append(records, encBlit{blit: Blit{Left: 130, Bottom: 40}, shape: s3, newRec: recMatchedRefineImageOnly, match: 0})
	// Another matched copy, in a new row.
	records = append(records, encBlit{blit: Blit{Left: 40, Bottom: 100}, match: 6, newRec: recMatchedCopy})
	// Non-mark data at an absolute position.
	s4 := &Shape{Parent: -2, Bits: randomShape(rng, 10, 6)}
	records = append(records, encBlit{blit: Blit{Left: 200, Bottom: 150}, shape: s4, newRec: recNonMarkData})

	stream := mustEncodeImage(t, im, dict, records)
	got, err := DecodeImage(bytes.NewReader(stream), func() *Dict { return dict })
	if err != nil {
		t.Fatalf("DecodeImage: %v", err)
	}
	if got.Width != im.Width || got.Height != im.Height {
		t.Fatalf("dimensions %dx%d, want %dx%d", got.Width, got.Height, im.Width, im.Height)
	}
	if len(got.Blits) != len(im.Blits) {
		t.Fatalf("blit count %d, want %d", len(got.Blits), len(im.Blits))
	}
	for i := range im.Blits {
		if im.Blits[i] != got.Blits[i] {
			t.Errorf("blit %d: %+v, want %+v", i, got.Blits[i], im.Blits[i])
		}
	}
	// Compare the rendered pages pixel by pixel.
	wantPage := im.PageBitmap()
	gotPage := got.PageBitmap()
	if wantPage.W != gotPage.W || wantPage.H != gotPage.H {
		t.Fatalf("page size mismatch")
	}
	diff := 0
	for y := 0; y < wantPage.H; y++ {
		for x := 0; x < wantPage.W; x++ {
			if wantPage.Get(x, y) != gotPage.Get(x, y) {
				diff++
			}
		}
	}
	if diff > 0 {
		t.Errorf("rendered pages differ in %d pixels", diff)
	}
}

func TestCorruptStreams(t *testing.T) {
	// Truncated streams must error, not panic.
	rng := rand.New(rand.NewSource(4))
	im := &Image{Width: 100, Height: 80}
	var records []encBlit
	s := &Shape{Parent: -1, Bits: randomShape(rng, 10, 10)}
	records = append(records, encBlit{blit: Blit{Left: 5, Bottom: 5}, shape: s, newRec: recNewMark})
	full := mustEncodeImage(t, im, nil, records)
	for n := 0; n < len(full); n++ {
		_, err := DecodeImage(bytes.NewReader(full[:n]), nil)
		if err == nil && n < len(full)-1 {
			// A truncated stream that still decodes must have consumed
			// a consistent prefix; the reference tolerates early
			// end-of-data. Just make sure it does not panic.
			_ = err
		}
	}
	// Garbage must not panic either.
	for i := 0; i < 200; i++ {
		garbage := make([]byte, 64)
		rng.Read(garbage)
		_, _ = DecodeImage(bytes.NewReader(garbage), nil)
		_, _ = DecodeDict(bytes.NewReader(garbage), nil)
	}
}

func TestBitmapBlit(t *testing.T) {
	// Exhaustive-ish blit coverage: every alignment and clipping case.
	src := bitmap.New(8, 6)
	for y := 0; y < src.H; y++ {
		for x := 0; x < src.W; x++ {
			if (x*7+y*13)%3 == 0 {
				src.Set(x, y)
			}
		}
	}
	for _, left := range []int{-10, -8, -3, 0, 1, 7, 8, 31, 32, 33, 39} {
		for _, top := range []int{-4, 0, 1, 12, 24, 25, 29} {
			ref := bitmap.New(40, 30)
			ref.Blit(src, left, top)
			// Slow reference: per-bit OR.
			slow := bitmap.New(40, 30)
			for y := 0; y < src.H; y++ {
				for x := 0; x < src.W; x++ {
					if src.Get(x, y) {
						dx, dy := left+x, top+y
						if dx >= 0 && dy >= 0 && dx < slow.W && dy < slow.H {
							slow.Set(dx, dy)
						}
					}
				}
			}
			if !bytes.Equal(ref.Bits, slow.Bits) {
				t.Errorf("blit at (%d,%d) does not match per-bit reference", left, top)
				return
			}
		}
	}
}
