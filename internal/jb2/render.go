package jb2

import "github.com/din14970/djvu-go/internal/bitmap"

// PageBitmap composites every blit of the image into a page mask of
// Width x Height pixels: bit set means ink. Blits referencing shapes
// that failed to decode are skipped; blits are clipped at the page
// bounds.
func (im *Image) PageBitmap() *bitmap.Bitmap {
	page := bitmap.New(im.Width, im.Height)
	packed := make([]*bitmap.Bitmap, im.ShapeCount())
	for i := range packed {
		s, err := im.Shape(i)
		if err == nil && s.Bits != nil {
			packed[i] = shapeBitmap(s.Bits)
		}
	}
	for _, b := range im.Blits {
		if b.ShapeNo < 0 || b.ShapeNo >= len(packed) {
			continue
		}
		src := packed[b.ShapeNo]
		if src == nil {
			continue
		}
		// The blit gives the bottom left corner in page coordinates with
		// the origin at the bottom left of the page; the page bitmap is
		// top down.
		top := im.Height - (b.Bottom + src.H)
		page.Blit(src, b.Left, top)
	}
	return page
}

// shapeBitmap converts a shape from the grayscale coder representation
// (rows bottom to top) into a packed top-down bitmap.
func shapeBitmap(g *grayMap) *bitmap.Bitmap {
	b := bitmap.New(g.w, g.h)
	stride := b.Stride
	for sy := 0; sy < g.h; sy++ {
		r := g.row(sy)
		dst := b.Bits[(g.h-1-sy)*stride:]
		for x := 0; x < g.w; x++ {
			if r[g.border+x] != 0 {
				dst[x>>3] |= 1 << (7 - uint(x&7))
			}
		}
	}
	return b
}
