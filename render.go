package djvu

import (
	"fmt"
	"image"
	"image/png"
	"io"

	"github.com/din14970/djvu-go/internal/iw44"
	"github.com/din14970/djvu-go/internal/jb2"
	"github.com/din14970/djvu-go/internal/palette"
	"github.com/din14970/djvu-go/internal/render"
)

// maxDimension is the largest page dimension the renderer accepts.
// Larger pages are rejected before any allocation to prevent
// out-of-memory conditions on corrupt files.
const maxDimension = 20000

// RenderOptions configures page rendering.
type RenderOptions struct {
	// Subsample renders the page at 1/Subsample of its native
	// resolution: 2 renders at half resolution, 4 at a quarter, and so
	// on. Values below 1 mean 1. The reference implementation renders
	// integral reductions up to 15; higher values wrap like the
	// reference's byte sized alpha counts.
	Subsample int
}

// pageLayers are the decoded layers of one page, cached per document.
type pageLayers struct {
	width, height int
	mask          *jb2.Image
	pal           *palette.Palette
	fg            *iw44.Image // FG44
	bg            *iw44.Image // BG44
}

// render decodes the layers of page i and composites them at the
// requested subsampling factor.
func (d *Document) render(i, subsample int) (*image.RGBA, error) {
	l, err := d.layers(i)
	if err != nil {
		return nil, err
	}
	rl := &render.Layers{
		Width:  l.width,
		Height: l.height,
		Mask:   l.mask,
		Pal:    l.pal,
	}
	// Keep the interface nil when there is no background, so that the
	// compositor renders white.
	if l.bg != nil {
		rl.BGImage = l.bg
	}
	if l.fg != nil {
		rl.FGPixmap = l.fg.Pixmap(1)
	}
	if l.mask != nil && l.pal == nil && l.fg == nil && l.bg == nil {
		// Bitonal page: black content on white.
		return render.ToRGBA(render.Bitonal(rl, subsample)), nil
	}
	// Pages with no background render over white, like the reference
	// renders empty pages.
	out := render.Background(rl, subsample)
	render.Stencil(rl, out, subsample)
	return render.ToRGBA(out), nil
}

// layers decodes the layers of page i, using the per document cache.
func (d *Document) layers(i int) (*pageLayers, error) {
	d.renderMu.Lock()
	defer d.renderMu.Unlock()
	if l, ok := d.renderCache[i]; ok {
		return l, nil
	}
	p, err := d.page(i)
	if err != nil {
		return nil, err
	}
	if p.children == nil {
		return nil, fmt.Errorf("djvu: cannot render page %d of an indirect document: %w", i, errIndirect)
	}
	l := &pageLayers{width: p.info.Width, height: p.info.Height}
	// Shared JB2 dictionaries: the page's INCL chunks name components
	// whose Djbz chunks the page's Sjbz inherits.
	var dict *jb2.Dict
	visited := map[string]bool{}
	var findDict func(ids []string, depth int) error
	findDict = func(ids []string, depth int) error {
		if dict != nil || depth > 32 {
			return nil
		}
		for _, id := range ids {
			comp := d.comps[id]
			if comp == nil || visited[id] {
				continue
			}
			visited[id] = true
			if err := findDict(inclIDs(comp.children), depth+1); err != nil {
				return err
			}
			for _, c := range comp.children {
				if c.ID != "Djbz" || dict != nil {
					continue
				}
				d2, err := jb2.DecodeDict(c.Payload(), nil)
				if err != nil {
					return fmt.Errorf("djvu: decoding shared dictionary %s: %w", id, err)
				}
				dict = d2
			}
		}
		return nil
	}
	if err := findDict(inclIDs(p.children), 0); err != nil {
		return nil, err
	}
	for _, c := range p.children {
		switch c.ID {
		case "Sjbz":
			if l.mask != nil {
				return nil, fmt.Errorf("djvu: page %d has more than one Sjbz chunk", i)
			}
			im, err := jb2.DecodeImage(c.Payload(), func() *jb2.Dict { return dict })
			if err != nil {
				return nil, fmt.Errorf("djvu: sjbz decode page %d: %w", i, err)
			}
			l.mask = im
			if l.width == 0 && l.height == 0 {
				l.width, l.height = im.Width, im.Height
			}
		case "FGbz":
			if l.pal != nil {
				return nil, fmt.Errorf("djvu: page %d has more than one FGbz chunk", i)
			}
			pal, err := palette.Decode(c.Payload())
			if err != nil {
				return nil, fmt.Errorf("djvu: fgbz decode page %d: %w", i, err)
			}
			l.pal = pal
		case "BG44":
			if l.bg == nil {
				l.bg = iw44.NewColor()
			}
			if _, err := l.bg.DecodeChunk(c.Payload()); err != nil {
				return nil, fmt.Errorf("djvu: bg44 decode page %d: %w", i, err)
			}
			if l.width == 0 && l.height == 0 {
				l.width, l.height = l.bg.Width(), l.bg.Height()
			}
		case "FG44":
			if l.fg != nil {
				return nil, fmt.Errorf("djvu: page %d has more than one FG44 chunk", i)
			}
			fg := iw44.NewColor()
			if _, err := fg.DecodeChunk(c.Payload()); err != nil {
				return nil, fmt.Errorf("djvu: fg44 decode page %d: %w", i, err)
			}
			l.fg = fg
		}
	}
	if l.width <= 0 || l.height <= 0 {
		return nil, fmt.Errorf("djvu: page %d has no INFO chunk and no layer with dimensions", i)
	}
	if l.width > maxDimension || l.height > maxDimension {
		return nil, fmt.Errorf("djvu: page %d is too large to render (%dx%d; the limit is %d)",
			i, l.width, l.height, maxDimension)
	}
	if l.mask != nil && (l.mask.Width > maxDimension || l.mask.Height > maxDimension) {
		return nil, fmt.Errorf("djvu: page %d has a mask too large to render (%dx%d; the limit is %d)",
			i, l.mask.Width, l.mask.Height, maxDimension)
	}
	if d.renderCache == nil {
		d.renderCache = map[int]*pageLayers{}
	}
	d.renderCache[i] = l
	return l, nil
}

// Render decodes page i and composites its layers (JB2 stencil, FGbz
// palette or FG44 color foreground, BG44 background) into an RGBA
// image at the page's native resolution, or at a reduced resolution
// when opts.Subsample is set. The result matches the output of the
// reference ddjvu tool; rows are ordered top down like in the image
// package.
//
// Pages without a background render over white; bitonal pages (a mask
// without any color layer) render as anti-aliased black on white.
// Decode results are cached, so repeated renders of the same page do
// not decode again.
func (d *Document) Render(page int, opts RenderOptions) (*image.RGBA, error) {
	sub := opts.Subsample
	if sub < 1 {
		sub = 1
	}
	if sub > 16 {
		sub = 16
	}
	return d.render(page, sub)
}

// RenderPNG decodes and composites page i like [Document.Render] and
// writes the result to w as a PNG image.
func (d *Document) RenderPNG(w io.Writer, page int, opts RenderOptions) error {
	img, err := d.Render(page, opts)
	if err != nil {
		return err
	}
	if err := png.Encode(w, img); err != nil {
		return fmt.Errorf("djvu: encoding png: %w", err)
	}
	return nil
}
