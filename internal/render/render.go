// Package render composites the decoded layers of a DjVu page into an
// image, following the MRC model of the reference implementation
// (DjVuImage::get_pixmap and DjVuImage::stencil): a wavelet coded
// background is warped to page resolution, attenuated through an
// anti-aliased alpha map built from the bilevel stencil, and the
// foreground colors (a palette or a wavelet coded image) are blended
// through the same alpha map.
package render

import (
	"image"

	"codeberg.org/din14970/djvu-go/internal/jb2"
	"codeberg.org/din14970/djvu-go/internal/palette"
	"codeberg.org/din14970/djvu-go/internal/pixmap"
)

// Layers are the decoded layers of one page. Any layer may be nil;
// pages without a mask render as their background alone, pages without
// a background render over white.
type Layers struct {
	// Width and Height are the page dimensions in pixels (from the
	// INFO chunk).
	Width, Height int
	// Mask is the decoded Sjbz chunk.
	Mask *jb2.Image
	// Pal is the decoded FGbz chunk (two layer model).
	Pal *palette.Palette
	// FGPixmap is the decoded FG44 chunk (three layer model).
	FGPixmap *pixmap.Pixmap
	// BGImage renders the background at its native resolution.
	BGImage interface {
		// Pixmap renders the image at the given subsampling factor.
		Pixmap(subsample int) *pixmap.Pixmap
	}
}

// computeRed determines by which factor an image of size rw x rh is
// reduced relative to a page of size w x h
// (DjVuImage::compute_red).
func computeRed(w, h, rw, rh int) int {
	for red := 1; red < 16; red++ {
		if (w+red-1)/red == rw && (h+red-1)/red == rh {
			return red
		}
	}
	return 16
}

// rectOf returns the output rectangle for the full page at the given
// subsampling factor.
func rectOf(w, h, subsample int) pixmap.Rect {
	return pixmap.Rect{
		XMin: 0,
		YMin: 0,
		XMax: (w + subsample - 1) / subsample,
		YMax: (h + subsample - 1) / subsample,
	}
}

// Background produces the background layer at the given subsampling
// factor: the wavelet background warped to page resolution, or white
// when the page has no background.
func Background(l *Layers, subsample int) *pixmap.Pixmap {
	rect := rectOf(l.Width, l.Height, subsample)
	pm := pixmap.New(rect.Width(), rect.Height())
	if l.BGImage == nil {
		pm.Fill(255, 255, 255)
		return pm
	}
	bg := l.BGImage.Pixmap(1)
	if bg == nil {
		pm.Fill(255, 255, 255)
		return pm
	}
	red := computeRed(l.Width, l.Height, bg.W, bg.H)
	if red < 1 || red > 12 {
		pm.Fill(255, 255, 255)
		return pm
	}
	switch {
	case subsample == red:
		return bg
	case subsample == 2*red:
		return l.BGImage.Pixmap(2)
	case subsample == 4*red:
		return l.BGImage.Pixmap(4)
	case subsample == 8*red:
		return l.BGImage.Pixmap(8)
	}
	// All other cases go through the bilinear scaler: render the
	// background at a power of two subsampling and warp.
	po2 := 16
	for po2 > 1 && subsample < po2*red {
		po2 >>= 1
	}
	ipm := l.BGImage.Pixmap(po2)
	if ipm == nil {
		pm.Fill(255, 255, 255)
		return pm
	}
	ps := pixmap.NewScaler(ipm.W, ipm.H, rect.Width(), rect.Height())
	ps.SetHorzRatio(red*po2, subsample)
	ps.SetVertRatio(red*po2, subsample)
	ps.Scale(pixmap.Rect{XMax: ipm.W, YMax: ipm.H}, ipm, rect, pm)
	return pm
}

// visible returns the numbers of the blits that intersect the output
// rectangle, mirroring the clipping test of DjVuImage::stencil. The
// test runs in full resolution page coordinates.
func visible(l *Layers, rect pixmap.Rect, subsample int) []int {
	var out []int
	for i, b := range l.Mask.Blits {
		s, err := l.Mask.Shape(b.ShapeNo)
		if err != nil || s.Bits == nil {
			continue
		}
		w, h := s.ShapeWidth(), s.ShapeHeight()
		if b.Left <= rect.XMax*subsample &&
			b.Bottom <= rect.YMax*subsample &&
			b.Left+w >= rect.XMin*subsample &&
			b.Bottom+h >= rect.YMin*subsample {
			out = append(out, i)
		}
	}
	return out
}

// blitAlpha adds one blit into an alpha map. rxmin and rymin give the
// full resolution origin of the map relative to the page.
func blitAlpha(a *pixmap.Alpha, im *jb2.Image, blitNo, rxmin, rymin int) {
	b := im.Blits[blitNo]
	s, err := im.Shape(b.ShapeNo)
	if err != nil || s.Bits == nil {
		return
	}
	sw, sh := s.ShapeWidth(), s.ShapeHeight()
	a.Blit(func(y int) []byte {
		return s.ShapeRow(y)
	}, sw, sh, b.Left-rxmin, b.Bottom-rymin)
}

// Stencil superposes the foreground layers on the background pixmap,
// following DjVuImage::stencil. pm is modified in place.
func Stencil(l *Layers, pm *pixmap.Pixmap, subsample int) {
	if l.Mask == nil {
		return
	}
	if l.Mask.Width != l.Width || l.Mask.Height != l.Height {
		return
	}
	rect := rectOf(l.Width, l.Height, subsample)
	components := visible(l, rect, subsample)
	// Two layer model: palette colors, one per blit.
	if l.Pal != nil {
		stencilPalette(l, pm, subsample, rect, components)
		return
	}
	// Three layer model: wavelet coded color foreground.
	if l.FGPixmap != nil {
		stencilPixmap(l, pm, subsample, rect, components)
	}
}

// stencilPalette implements the two layer composite of
// DjVuImage::stencil: attenuate through the full alpha map, then
// blend each group of like colored blits through its own alpha map.
func stencilPalette(l *Layers, pm *pixmap.Pixmap, subsample int, rect pixmap.Rect, components []int) {
	if len(l.Pal.Index) != len(l.Mask.Blits) {
		return
	}
	// Full alpha map over every visible blit.
	bm := pixmap.NewAlpha(rect.Width(), rect.Height(), subsample)
	for _, blitno := range components {
		blitAlpha(bm, l.Mask, blitno, 0, 0)
	}
	pm.Attenuate(bm, 0, 0)
	// Blend the colors, one group of like colored blits at a time.
	for len(components) > 0 {
		colorindex := l.Pal.Index[components[0]]
		lastx := 0
		// Gather a group: same color blits within a run of blits
		// whose left coordinate does not decrease. The scan stops at
		// the first blit whose left coordinate decreases; that blit
		// and the ones after it remain for later groups.
		var compset []int
		var rest []int
		xmin, ymin, xmax, ymax := 1<<30, 1<<30, -1<<30, -1<<30
		broke := false
		for _, blitno := range components {
			b := l.Mask.Blits[blitno]
			if broke {
				rest = append(rest, blitno)
				continue
			}
			if b.Left < lastx {
				rest = append(rest, blitno)
				broke = true
				continue
			}
			lastx = b.Left
			if l.Pal.Index[blitno] == colorindex {
				s, err := l.Mask.Shape(b.ShapeNo)
				if err != nil || s.Bits == nil {
					continue
				}
				if b.Left < xmin {
					xmin = b.Left
				}
				if b.Bottom < ymin {
					ymin = b.Bottom
				}
				if b.Left+s.ShapeWidth() > xmax {
					xmax = b.Left + s.ShapeWidth()
				}
				if b.Bottom+s.ShapeHeight() > ymax {
					ymax = b.Bottom + s.ShapeHeight()
				}
				compset = append(compset, blitno)
			} else {
				rest = append(rest, blitno)
			}
		}
		components = rest
		if len(compset) == 0 {
			continue
		}
		// Round the group rectangle to output coordinates.
		cr := pixmap.Rect{
			XMin: xmin / subsample,
			YMin: ymin / subsample,
			XMax: (xmax + subsample - 1) / subsample,
			YMax: (ymax + subsample - 1) / subsample,
		}
		if cr.XMin < rect.XMin {
			cr.XMin = rect.XMin
		}
		if cr.YMin < rect.YMin {
			cr.YMin = rect.YMin
		}
		if cr.XMax > rect.XMax {
			cr.XMax = rect.XMax
		}
		if cr.YMax > rect.YMax {
			cr.YMax = rect.YMax
		}
		if cr.Width() <= 0 || cr.Height() <= 0 {
			continue
		}
		g := pixmap.NewAlpha(cr.Width(), cr.Height(), subsample)
		rxmin := cr.XMin * subsample
		rymin := cr.YMin * subsample
		for _, blitno := range compset {
			blitAlpha(g, l.Mask, blitno, rxmin, rymin)
		}
		col := l.Pal.Color(colorindex)
		pm.BlitColor(g, cr.XMin-rect.XMin, cr.YMin-rect.YMin, [3]byte{col[0], col[1], col[2]})
	}
}

// stencilPixmap implements the three layer composite of
// DjVuImage::stencil for FG44 foregrounds: the foreground pixmap is
// warped to page resolution and blended through the alpha map
// (GPixmap::stencil with the simple upsampling path).
func stencilPixmap(l *Layers, pm *pixmap.Pixmap, subsample int, rect pixmap.Rect, components []int) {
	bm := pixmap.NewAlpha(rect.Width(), rect.Height(), subsample)
	for _, blitno := range components {
		blitAlpha(bm, l.Mask, blitno, 0, 0)
	}
	fg := l.FGPixmap
	red := computeRed(l.Width, l.Height, fg.W, fg.H)
	if red < 1 || red > 12 {
		return
	}
	// Try supersampling the foreground by an integer factor.
	supersample := 1
	if red > subsample {
		supersample = red / subsample
	}
	wantedred := supersample * subsample
	if red == wantedred {
		pm.Stencil(bm, fg, supersample, rect)
		return
	}
	// Pre-warp the foreground with the bilinear scaler, then blend.
	desw := (fg.W*red + wantedred - 1) / wantedred
	desh := (fg.H*red + wantedred - 1) / wantedred
	ps := pixmap.NewScaler(fg.W, fg.H, desw, desh)
	ps.SetHorzRatio(red, wantedred)
	ps.SetVertRatio(red, wantedred)
	nfg := pixmap.New(desw, desh)
	ps.Scale(pixmap.Rect{XMax: fg.W, YMax: fg.H}, fg, pixmap.Rect{XMax: desw, YMax: desh}, nfg)
	pm.Stencil(bm, nfg, supersample, rect)
}

// ToRGBA converts a bottom up pixmap into a top down RGBA image, the
// orientation convention of the image package.
func ToRGBA(pm *pixmap.Pixmap) *image.RGBA {
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

// Bitonal renders a page that has a mask but neither palette nor color
// foreground: black content on white, anti-aliased through the alpha
// map. The conversion follows the bitmap branch of the reference's
// pixel conversion (ddjvu's fmt_convert for gray maps):
// level = white - (alpha*white + (grays-1)/2) / (grays-1).
func Bitonal(l *Layers, subsample int) *pixmap.Pixmap {
	if l.Mask == nil {
		return nil
	}
	rect := rectOf(l.Width, l.Height, subsample)
	pm := pixmap.New(rect.Width(), rect.Height())
	pm.Fill(255, 255, 255)
	bm := pixmap.NewAlpha(rect.Width(), rect.Height(), subsample)
	for _, blitno := range visible(l, rect, subsample) {
		blitAlpha(bm, l.Mask, blitno, 0, 0)
	}
	maxgray := bm.Grays - 1
	if maxgray < 1 {
		return pm
	}
	for i, c := range bm.C {
		if c > 0 {
			cc := int(c)
			if cc > maxgray {
				cc = maxgray
			}
			g := byte(255 - (cc*255+(maxgray-1)/2)/maxgray)
			pm.Pix[3*i], pm.Pix[3*i+1], pm.Pix[3*i+2] = g, g, g
		}
	}
	return pm
}
