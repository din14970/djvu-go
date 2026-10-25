// Go port of djvulibre's libdjvu/GPixmap.cpp and libdjvu/GBitmap.cpp (alpha map and compositing).
// Upstream copyright (c) 2002 Leon Bottou and Yann Le Cun; derived
// from the DjVu Reference Library (c) 1999-2001 LizardTech, Inc. This
// derivative work is licensed GPL-2.0-or-later; see the LICENSE file.

// Package pixmap implements the color compositing primitives of the
// DjVu reference library: an 8 bit RGB pixmap with row origin at the
// bottom (like GPixmap), a byte-count alpha map (like a GBitmap with
// grays), the attenuation and color blending operations used by the
// MRC compositing model, and the bilinear pixmap scaler used to warp
// reduced backgrounds and foregrounds to page resolution.
package pixmap

// Pixmap is an 8 bit RGB image with rows ordered bottom to top, the
// coordinate convention of the DjVu reference library. Pixel (x, y)
// has its origin at the bottom left corner of the image.
type Pixmap struct {
	W, H int
	// Pix holds three bytes per pixel, row by row starting with the
	// bottom row: pixel (x, y) lives at Pix[(y*W+x)*3].
	Pix []byte
}

// New creates a black pixmap of the given size.
func New(w, h int) *Pixmap {
	if w < 0 || h < 0 {
		w, h = 0, 0
	}
	return &Pixmap{W: w, H: h, Pix: make([]byte, 3*w*h)}
}

// Fill sets every pixel to the given color.
func (p *Pixmap) Fill(r, g, b byte) {
	for i := 0; i < len(p.Pix); i += 3 {
		p.Pix[i], p.Pix[i+1], p.Pix[i+2] = r, g, b
	}
}

// Set writes the pixel at (x, y).
func (p *Pixmap) Set(x, y int, r, g, b byte) {
	i := (y*p.W + x) * 3
	p.Pix[i], p.Pix[i+1], p.Pix[i+2] = r, g, b
}

// At returns the pixel at (x, y).
func (p *Pixmap) At(x, y int) (r, g, b byte) {
	i := (y*p.W + x) * 3
	return p.Pix[i], p.Pix[i+1], p.Pix[i+2]
}

// Alpha is an alpha map holding per destination pixel the number of
// bilevel source pixels that fell into it while blitting shapes at a
// subsampling factor. Values range from 0 to Subsample squared, with
// the semantic of a GBitmap whose gray levels are 1+Subsample*Subsample.
// Counts wrap around 255 like the reference implementation. Rows are
// ordered bottom to top.
type Alpha struct {
	W, H      int
	Subsample int
	Grays     int // 1 + Subsample*Subsample
	C         []byte
}

// NewAlpha creates an alpha map of the given size. Subsample must be
// between 1 and 15, like the reductions supported by the reference.
func NewAlpha(w, h, subsample int) *Alpha {
	if subsample < 1 || subsample > 15 {
		subsample = 1
	}
	if w < 0 || h < 0 {
		w, h = 0, 0
	}
	return &Alpha{
		W:         w,
		H:         h,
		Subsample: subsample,
		Grays:     1 + subsample*subsample,
		C:         make([]byte, w*h),
	}
}

// Blit adds the counts of a bilevel shape into the map. The shape is
// given by its size in pixels and a row accessor returning sw bytes of
// 0 or 1 per row, ordered bottom to top; row access outside the shape
// must yield all zeros. The position (xh, yh) is the bottom left corner
// of the shape in the full resolution coordinate system of the map.
// This replicates GBitmap::blit(bm, xh, yh, subsample).
func (a *Alpha) Blit(row func(y int) []byte, sw, sh, xh, yh int) {
	s := a.Subsample
	if xh >= a.W*s || yh >= a.H*s || xh+sw < 0 || yh+sh < 0 {
		return
	}
	dr, dr1 := yh/s, yh%s
	dc0, dc10 := xh/s, xh%s
	for sr := 0; sr < sh; sr++ {
		if dr >= 0 && dr < a.H {
			srow := row(sr)
			dc, dc1 := dc0, dc10
			base := dr * a.W
			for sc := 0; sc < sw; sc++ {
				if dc >= 0 && dc < a.W {
					a.C[base+dc] += srow[sc]
				}
				dc1++
				if dc1 >= s {
					dc1 = 0
					dc++
				}
			}
		}
		dr1++
		if dr1 >= s {
			dr1 = 0
			dr++
		}
	}
}

// Attenuate darkens the pixmap through the alpha map: each pixel is
// multiplied by (maxgray-alpha)/maxgray. This replicates
// GPixmap::attenuate(bm, xpos, ypos).
func (p *Pixmap) Attenuate(a *Alpha, xpos, ypos int) {
	maxgray := a.Grays - 1
	var multiplier [256]uint32
	for i := 0; i < maxgray; i++ {
		multiplier[i] = uint32(0x10000*i) / uint32(maxgray)
	}
	xrows := min(ypos+a.H, p.H) - max(0, ypos)
	xcols := min(xpos+a.W, p.W) - max(0, xpos)
	if xrows <= 0 || xcols <= 0 {
		return
	}
	for y := 0; y < xrows; y++ {
		src := (y+max(0, -ypos))*a.W + max(0, -xpos)
		dst := (y+max(0, ypos))*p.W + max(0, xpos)
		for x := 0; x < xcols; x++ {
			c := a.C[src+x]
			if c > 0 {
				if int(c) >= maxgray {
					p.Pix[(dst+x)*3] = 0
					p.Pix[(dst+x)*3+1] = 0
					p.Pix[(dst+x)*3+2] = 0
				} else {
					level := multiplier[c]
					for k := 0; k < 3; k++ {
						i := (dst+x)*3 + k
						v := uint32(p.Pix[i])
						p.Pix[i] = byte(v - ((v * level) >> 16))
					}
				}
			}
		}
	}
}

// BlitColor blends a constant color through the alpha map, adding
// color*alpha/maxgray to each pixel with saturation. This replicates
// GPixmap::blit(bm, xpos, ypos, color).
func (p *Pixmap) BlitColor(a *Alpha, xpos, ypos int, col [3]byte) {
	maxgray := a.Grays - 1
	var multiplier [256]uint32
	for i := 1; i < maxgray; i++ {
		multiplier[i] = uint32(0x10000*i) / uint32(maxgray)
	}
	xrows := min(ypos+a.H, p.H) - max(0, ypos)
	xcols := min(xpos+a.W, p.W) - max(0, xpos)
	if xrows <= 0 || xcols <= 0 {
		return
	}
	for y := 0; y < xrows; y++ {
		src := (y+max(0, -ypos))*a.W + max(0, -xpos)
		dst := (y+max(0, ypos))*p.W + max(0, xpos)
		for x := 0; x < xcols; x++ {
			c := a.C[src+x]
			if c > 0 {
				for k := 0; k < 3; k++ {
					i := (dst+x)*3 + k
					v := uint32(p.Pix[i])
					if int(c) >= maxgray {
						v += uint32(col[k])
					} else {
						level := multiplier[c]
						v += (uint32(col[k]) * level) >> 16
					}
					if v > 255 {
						v = 255
					}
					p.Pix[i] = byte(v)
				}
			}
		}
	}
}

// Stencil blends a foreground pixmap through the alpha map,
// supersampling the foreground by the given factor, following
// GPixmap::stencil. The pmr rectangle restricts the operation to part
// of the foreground; the whole map is used when pmr is zero. This
// replicates the blending of the three layer MRC model.
func (p *Pixmap) Stencil(a *Alpha, fg *Pixmap, pms int, pmr Rect) {
	if pms < 1 {
		pms = 1
	}
	// The pmr rectangle selects the part of the foreground that
	// corresponds to this pixmap.
	fgrect := Rect{XMax: fg.W * pms, YMax: fg.H * pms}
	if pmr.XMax == 0 && pmr.YMax == 0 {
		pmr = fgrect
	}
	xrows := p.H
	if a.H < xrows {
		xrows = a.H
	}
	if pmr.Height() < xrows {
		xrows = pmr.Height()
	}
	xcols := p.W
	if a.W < xcols {
		xcols = a.W
	}
	if pmr.Width() < xcols {
		xcols = pmr.Width()
	}
	maxgray := a.Grays - 1
	var multiplier [256]uint32
	for i := 1; i < maxgray; i++ {
		multiplier[i] = uint32(0x10000*i) / uint32(maxgray)
	}
	// Starting point in the blown up foreground.
	fgy, fgy1 := pmr.YMin/pms, pmr.YMin%pms
	fgx0, fgx10 := pmr.XMin/pms, pmr.XMin%pms
	for y := 0; y < xrows; y++ {
		src := y * a.W
		dst := y * p.W
		fgx, fgx1 := fgx0, fgx10
		for x := 0; x < xcols; x++ {
			c := a.C[src+x]
			if c > 0 {
				fi := (fgy*fg.W + fgx) * 3
				di := (dst + x) * 3
				if int(c) >= maxgray {
					p.Pix[di] = fg.Pix[fi]
					p.Pix[di+1] = fg.Pix[fi+1]
					p.Pix[di+2] = fg.Pix[fi+2]
				} else {
					level := multiplier[c]
					for k := 0; k < 3; k++ {
						d := int(p.Pix[di+k])
						f := int(fg.Pix[fi+k])
						p.Pix[di+k] = byte(d - (((d - f) * int(level)) >> 16))
					}
				}
			}
			fgx1++
			if fgx1 >= pms {
				fgx1 = 0
				fgx++
			}
		}
		fgy1++
		if fgy1 >= pms {
			fgy1 = 0
			fgy++
		}
	}
}
