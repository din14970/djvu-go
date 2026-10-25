// Go port of djvulibre's libdjvu/GBitmap.cpp (packed bitmap operations).
// Upstream copyright (c) 2002 Leon Bottou and Yann Le Cun; derived
// from the DjVu Reference Library (c) 1999-2001 LizardTech, Inc. This
// derivative work is licensed GPL-2.0-or-later; see the LICENSE file.

// Package bitmap provides a packed 1 bit-per-pixel bitmap with clipped
// blitting, the working representation for decoded JB2 page masks and
// for MRC compositing.
//
// Bits are packed row-major, most significant bit first, one row per
// stride bytes. Rows are stored top to bottom, x grows to the right, so
// bitmaps map directly onto image dimensions.
package bitmap

// Bitmap is a packed 1bpp bitmap.
type Bitmap struct {
	// W and H are the dimensions in pixels.
	W, H int
	// Stride is the number of bytes per row, at least (W+7)/8.
	Stride int
	// Bits holds the packed rows, top to bottom.
	Bits []byte
}

// New creates a zero (all white) bitmap of the given size.
func New(w, h int) *Bitmap {
	if w < 0 || h < 0 {
		w, h = 0, 0
	}
	b := &Bitmap{
		W:      w,
		H:      h,
		Stride: (w + 7) >> 3,
	}
	b.Bits = make([]byte, h*b.Stride)
	return b
}

// Get reports whether the pixel at (x, y) is set. Coordinates outside
// the bitmap are never set.
func (b *Bitmap) Get(x, y int) bool {
	if x < 0 || y < 0 || x >= b.W || y >= b.H {
		return false
	}
	return b.Bits[y*b.Stride+(x>>3)]&(0x80>>(x&7)) != 0
}

// Set sets the pixel at (x, y). Coordinates outside the bitmap are
// ignored.
func (b *Bitmap) Set(x, y int) {
	if x < 0 || y < 0 || x >= b.W || y >= b.H {
		return
	}
	b.Bits[y*b.Stride+(x>>3)] |= 0x80 >> (x & 7)
}

// Blit ORs the pixels of src into b, with the top left corner of src at
// (left, top) in b coordinates. Parts outside b are clipped.
func (b *Bitmap) Blit(src *Bitmap, left, top int) {
	if src.W == 0 || src.H == 0 {
		return
	}
	// Clip vertically.
	sy0 := 0
	if top < 0 {
		sy0 = -top
		top = 0
	}
	sy1 := src.H
	if top+src.H > b.H {
		sy1 = b.H - top
	}
	if sy0 >= sy1 {
		return
	}
	// Clip horizontally.
	sx0 := 0
	if left < 0 {
		sx0 = -left
		left = 0
	}
	sx1 := src.W
	if left+src.W > b.W {
		sx1 = b.W - left
	}
	if sx0 >= sx1 {
		return
	}
	for sy := sy0; sy < sy1; sy++ {
		dstRow := b.Bits[(top+sy-sy0)*b.Stride:]
		srcRow := src.Bits[sy*src.Stride:]
		orRow(dstRow, srcRow, left, sx0, sx1)
	}
}

// orRow ORs the source row bits [sx0, sx1) into dst at pixel offset
// left (the destination pixel of source bit x is left+x-sx0). The caller
// guarantees that all touched destination bits stay within the row.
func orRow(dst, src []byte, left, sx0, sx1 int) {
	if left&7 == 0 && sx0&7 == 0 {
		// Byte aligned fast path.
		db := left >> 3
		for x := sx0; x+8 <= sx1; x += 8 {
			dst[db] |= src[x>>3]
			db++
		}
		for x := (sx1 &^ 7); x < sx1; x++ {
			if x < sx0 {
				continue
			}
			dx := left + x - sx0
			if src[x>>3]&(0x80>>(x&7)) != 0 {
				dst[dx>>3] |= 0x80 >> (dx & 7)
			}
		}
		return
	}
	// General path, bit by bit.
	for x := sx0; x < sx1; x++ {
		if src[x>>3]&(0x80>>(x&7)) == 0 {
			continue
		}
		dx := left + x - sx0
		dst[dx>>3] |= 0x80 >> (dx & 7)
	}
}
