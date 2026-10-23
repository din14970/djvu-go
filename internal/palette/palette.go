// Package palette decodes FGbz chunks: the color palette (and optional
// per blit color indices) of compound DjVu documents, following
// DjVuPalette::decode of the reference implementation.
package palette

import (
	"fmt"
	"io"

	"github.com/din14970/djvu-go/internal/bzz"
)

// paletteVersion is the version number of the palette coding.
const paletteVersion = 0

// Palette is a decoded FGbz chunk: a list of RGB colors and, when the
// chunk carries them, one palette index per blit of the matching Sjbz
// chunk.
type Palette struct {
	// Colors holds palettesize RGB triples read from the chunk, in
	// the order of the palette entries.
	Colors [][3]byte
	// Index holds one color number per blit, in blit order, when the
	// document codes foreground colors (the version flag 0x80).
	Index []int
}

// Size returns the number of colors in the palette.
func (p *Palette) Size() int { return len(p.Colors) }

// Color returns the RGB triple of palette entry i.
func (p *Palette) Color(i int) [3]byte { return p.Colors[i] }

// Decode reads a palette from r. When the palette carries per blit
// color indices, they are read from a BZZ compressed stream embedded
// in the chunk.
func Decode(r io.Reader) (*Palette, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:1]); err != nil {
		return nil, fmt.Errorf("palette: reading version: %w", err)
	}
	if hdr[0]&0x7f != paletteVersion {
		return nil, fmt.Errorf("palette: unsupported version %d", hdr[0]&0x7f)
	}
	if _, err := io.ReadFull(r, hdr[1:]); err != nil {
		return nil, fmt.Errorf("palette: reading palette size: %w", err)
	}
	palettesize := int(hdr[1])<<8 | int(hdr[2])
	if palettesize > 65535 {
		return nil, fmt.Errorf("palette: palette size %d out of range", palettesize)
	}
	p := &Palette{Colors: make([][3]byte, palettesize)}
	rgb := make([]byte, 3*palettesize)
	if _, err := io.ReadFull(r, rgb); err != nil {
		return nil, fmt.Errorf("palette: reading %d colors: %w", palettesize, err)
	}
	// The reference stores palette entries in the memory layout of
	// GPixel, whose fields are ordered blue, green, red; the three
	// bytes of each entry are read in that order.
	for i := 0; i < palettesize; i++ {
		p.Colors[i] = [3]byte{rgb[3*i+2], rgb[3*i+1], rgb[3*i]}
	}
	if hdr[0]&0x80 == 0 {
		return p, nil
	}
	var sz [3]byte
	if _, err := io.ReadFull(r, sz[:]); err != nil {
		return nil, fmt.Errorf("palette: reading color data size: %w", err)
	}
	datasize := int(sz[0])<<16 | int(sz[1])<<8 | int(sz[2])
	if datasize > 1<<24 {
		return nil, fmt.Errorf("palette: color data size %d out of range", datasize)
	}
	// The color data is BZZ compressed.
	dec := bzz.NewReader(r)
	p.Index = make([]int, datasize)
	var buf [2]byte
	for d := 0; d < datasize; d++ {
		if _, err := io.ReadFull(dec, buf[:]); err != nil {
			return nil, fmt.Errorf("palette: reading color index %d: %w", d, err)
		}
		v := int(buf[0])<<8 | int(buf[1])
		if v < 0 || v >= palettesize {
			return nil, fmt.Errorf("palette: color index %d out of range (palette size %d)", v, palettesize)
		}
		p.Index[d] = v
	}
	return p, nil
}
