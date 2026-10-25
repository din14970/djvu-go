// Go port of djvulibre's libdjvu/JB2Image.cpp and libdjvu/GBitmap.cpp (bordered bitmaps).
// Upstream copyright (c) 2002 Leon Bottou and Yann Le Cun; derived
// from the DjVu Reference Library (c) 1999-2001 LizardTech, Inc. This
// derivative work is licensed GPL-2.0-or-later; see the LICENSE file.

package jb2

// grayMap is a small grayscale bitmap used to hold JB2 shapes while
// decoding. Pixel values are 0 or 1 bytes.
//
// It replicates the memory layout guarantees the JB2 context functions
// rely on in the reference implementation (GBitmap):
//
//   - Each row is padded with border zero bytes on both sides.
//   - Accessing a row outside [0, height) yields an all-zero row, like
//     GBitmap's zerobuffer.
//
// Rows are allocated lazily on first write. A corrupt stream can
// therefore declare huge shape dimensions without causing a huge
// allocation: decoding stops at the end of the input after only a few
// rows have been touched.
//
// The row accessors return a slice r such that r[border+x] is content
// column x; this keeps all context window accesses inside the slice and
// makes padding reads on the left work naturally.
type grayMap struct {
	w, h    int
	border  int
	bpr     int // bytes per row: w + 2*border
	rows    [][]byte
	zeroRow []byte
}

// newGrayMap creates a lazily allocated bitmap with the given border.
func newGrayMap(w, h, border int) *grayMap {
	if w < 0 || h < 0 {
		w, h = 0, 0
	}
	return &grayMap{
		w:       w,
		h:       h,
		border:  border,
		bpr:     w + 2*border,
		rows:    make([][]byte, h),
		zeroRow: make([]byte, w+2*border),
	}
}

// row returns the row slice for reading: r[g.border+x] addresses content
// column x. Unwritten and out-of-range rows are all zero.
func (g *grayMap) row(y int) []byte {
	if y < 0 || y >= g.h {
		return g.zeroRow
	}
	if g.rows[y] == nil {
		return g.zeroRow
	}
	return g.rows[y]
}

// rowWrite returns the row slice for writing, allocating the row on
// first use.
func (g *grayMap) rowWrite(y int) []byte {
	if y < 0 || y >= g.h {
		return g.zeroRow
	}
	if g.rows[y] == nil {
		g.rows[y] = make([]byte, g.bpr)
	}
	return g.rows[y]
}

// ensureBorder grows the horizontal border of the bitmap, preserving
// content and zero-filling the new padding (GBitmap::minborder).
func (g *grayMap) ensureBorder(minborder int) {
	if g.border >= minborder {
		return
	}
	old := *g
	ng := newGrayMap(g.w, g.h, minborder)
	for y := 0; y < g.h; y++ {
		if old.rows[y] == nil {
			continue
		}
		nr := ng.rowWrite(y)
		copy(nr[minborder:], old.rows[y][old.border:old.border+old.w])
	}
	*g = *ng
}

// boundingBox returns the tight bounding box of the non-zero pixels,
// following JB2Dict::LibRect::compute_bounding_box. For an empty bitmap
// it returns left=w, right=-1, bottom=h, top=-1 (the reference leaves
// the fields in a state that yields empty rectangles).
func (g *grayMap) boundingBox() libRect {
	var l libRect
	w, h := g.w, g.h
	// Right border.
	for l.right = w - 1; l.right >= 0; l.right-- {
		found := false
		for y := 0; y < h; y++ {
			if g.row(y)[g.border+l.right] != 0 {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	// Top border.
	for l.top = h - 1; l.top >= 0; l.top-- {
		found := false
		r := g.row(l.top)
		for x := 0; x < w; x++ {
			if r[g.border+x] != 0 {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	// Left border.
	for l.left = 0; l.left <= l.right; l.left++ {
		found := false
		for y := 0; y < h; y++ {
			if g.row(y)[g.border+l.left] != 0 {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	// Bottom border.
	for l.bottom = 0; l.bottom <= l.top; l.bottom++ {
		found := false
		r := g.row(l.bottom)
		for x := 0; x < w; x++ {
			if r[g.border+x] != 0 {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	return l
}

// packedBits converts the shape to a packed 1bpp representation, MSB
// first, with rows stored bottom-to-top (the order of grayMap rows).
// Each row uses (w+7)/8 bytes.
func (g *grayMap) packedBits() []byte {
	stride := (g.w + 7) >> 3
	out := make([]byte, g.h*stride)
	for y := 0; y < g.h; y++ {
		r := g.row(y)
		dst := out[y*stride:]
		for x := 0; x < g.w; x++ {
			if r[g.border+x] != 0 {
				dst[x>>3] |= 1 << (7 - uint(x&7))
			}
		}
	}
	return out
}
