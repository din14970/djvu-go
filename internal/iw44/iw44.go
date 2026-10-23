// Package iw44 implements the decoder for IW44 wavelet coded images:
// the BG44 (background), FG44 (foreground color) and TH44 (thumbnail)
// chunks of DjVu files, as well as the obsolete PM44/BM44 image files.
//
// An IW44 image is a sequence of chunks, each refining the image with
// additional "slices" of arithmetic coded wavelet coefficients. The
// coefficients are organized in 32x32 blocks, zigzag ordered, quantized
// with a per-band threshold that halves at every refinement round, and
// coded with a hierarchical set difference scheme (root bit, bucket
// bits, coefficient start bits, mantissa bits) on top of the ZP
// arithmetic coder. Color images code luma and two delayed chroma
// components. Reconstruction applies the inverse IW44 lifting transform
// and, for color, the "Pigeon" YCbCr to RGB conversion.
//
// The implementation is a port of the decoding half of IW44Image.cpp
// from the DjVu reference library.
package iw44

import (
	"fmt"
	"io"

	"codeberg.org/din14970/djvu-go/internal/bzz"
)

// Codec version accepted (IWCODEC_MAJOR/MINOR).
const (
	codecMajor = 1
	codecMinor = 2
)

// maxDim is the largest dimension accepted for an IW44 image. The
// chunk headers allow up to 65535; this limit prevents hostile files
// from requesting gigantic reconstruction buffers.
const maxDim = 20000

// iwQuant is the initial quantization threshold of the sixteen
// coefficient subbands.
var iwQuant = [16]int{
	0x004000,
	0x008000, 0x008000, 0x010000,
	0x010000, 0x010000, 0x020000,
	0x020000, 0x020000, 0x040000,
	0x040000, 0x040000, 0x080000,
	0x040000, 0x040000, 0x080000,
}

// Reconstruction constants (iw_shift, iw_round, iw_border).
const (
	iwShift  = 6
	iwRound  = 1 << (iwShift - 1)
	iwBorder = 3
)

// bandBuckets gives the first bucket and bucket count of each of the
// ten coding bands.
var bandBuckets = [10]struct{ start, size int }{
	{0, 1}, // band zero contains all lores info
	{1, 1}, {2, 1}, {3, 1},
	{4, 4}, {8, 4}, {12, 4},
	{16, 16}, {32, 16}, {48, 16},
}

// Image is a decoded (or partially decoded) IW44 image.
type Image struct {
	// pixmap marks images decoded in a color container (PM44): gray
	// data inside them renders with inverted polarity (127-y), like
	// the reference implementation's IWPixmap fallback.
	pixmap    bool
	w, h      int
	bw, bh    int // dimensions rounded up to 32
	blocks    []block
	nb        int
	crcbDelay int // -1 for gray images
	crcbHalf  int
	crb, cbb  *Image // chroma maps (nil for gray)
	cslice    int
	cserial   int
	ycodec    *codec
	cbcodec   *codec
	crcodec   *codec
}

// NewGray creates an empty grayscale IW44 image (BM44 style).
func NewGray() *Image {
	return &Image{crcbDelay: -1}
}

// NewColor creates an empty color IW44 image (PM44 style): the
// container of BG44, FG44 and TH44 chunks. Gray data inside a color
// container renders with inverted polarity, matching IWPixmap.
func NewColor() *Image {
	return &Image{crcbDelay: 0, pixmap: true}
}

// Width returns the width of the image in pixels, or 0 before the
// first chunk has been decoded.
func (im *Image) Width() int { return im.w }

// Height returns the height of the image in pixels.
func (im *Image) Height() int { return im.h }

// initMap creates the coefficient maps for a w x h image.
func (im *Image) initMap(w, h int) error {
	if w <= 0 || h <= 0 || w > maxDim || h > maxDim {
		return fmt.Errorf("iw44: image size %dx%d out of range", w, h)
	}
	im.w, im.h = w, h
	im.bw = (w + 0x20 - 1) &^ 0x1f
	im.bh = (h + 0x20 - 1) &^ 0x1f
	im.nb = (im.bw * im.bh) / (32 * 32)
	im.blocks = make([]block, im.nb)
	return nil
}

// DecodeChunk decodes one IW44 chunk (BG44, FG44, TH44, PM44 or BM44).
// Chunks must be decoded in serial order; each chunk refines the image
// with the number of slices declared in its header. It returns the
// total number of slices decoded so far.
func (im *Image) DecodeChunk(r io.Reader) (int, error) {
	// A closed (or never opened) codec restarts the slice counter; an
	// open codec continues across the chunks of an image.
	if im.ycodec == nil {
		im.cslice, im.cserial = 0, 0
	}
	// Primary header: serial and slice count.
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return 0, fmt.Errorf("iw44: reading chunk header: %w", err)
	}
	serial := int(hdr[0])
	nslices := im.cslice + int(hdr[1])
	if serial != im.cserial {
		return 0, fmt.Errorf("iw44: chunk serial %d, expected %d", serial, im.cserial)
	}
	// Secondary and tertiary headers on the first chunk.
	if im.cserial == 0 {
		var sh [2]byte
		if _, err := io.ReadFull(r, sh[:]); err != nil {
			return 0, fmt.Errorf("iw44: reading codec version: %w", err)
		}
		major, minor := int(sh[0]), int(sh[1])
		if major&0x7f != codecMajor {
			return 0, fmt.Errorf("iw44: incompatible codec version %d.%d", major&0x7f, minor)
		}
		if minor > codecMinor {
			return 0, fmt.Errorf("iw44: codec version %d.%d is too recent", major&0x7f, minor)
		}
		var th [4]byte
		if _, err := io.ReadFull(r, th[:]); err != nil {
			return 0, fmt.Errorf("iw44: reading image size: %w", err)
		}
		w := int(th[0])<<8 | int(th[1])
		h := int(th[2])<<8 | int(th[3])
		crcbdelay := 0
		if minor >= 2 {
			// The tertiary header of version 1.2 files carries the chroma
			// delay byte for gray and color data alike.
			var d [1]byte
			if _, err := io.ReadFull(r, d[:]); err != nil {
				return 0, fmt.Errorf("iw44: reading chroma delay: %w", err)
			}
			crcbdelay = int(d[0]) & 0x7f
			if d[0]&0x80 == 0 {
				im.crcbHalf = 1
			}
		}
		if major&0x80 != 0 {
			// Gray data.
			crcbdelay = -1
		}
		if im.crcbDelay >= 0 && crcbdelay >= 0 {
			im.crcbDelay = crcbdelay
		} else {
			im.crcbDelay = -1
		}
		if err := im.initMap(w, h); err != nil {
			return 0, err
		}
		im.ycodec = newCodec(im)
		if im.crcbDelay >= 0 {
			im.cbb = NewColor()
			im.crb = NewColor()
			if err := im.cbb.initMap(w, h); err != nil {
				return 0, err
			}
			if err := im.crb.initMap(w, h); err != nil {
				return 0, err
			}
			im.cbcodec = newCodec(im.cbb)
			im.crcodec = newCodec(im.crb)
		}
	}
	if im.ycodec == nil {
		return 0, fmt.Errorf("iw44: chunk before first header")
	}
	// Slice data.
	zp := bzz.NewZPDecoder(r)
	flag := 1
	for flag != 0 && im.cslice < nslices {
		flag = im.ycodec.codeSlice(zp)
		if im.cbcodec != nil && im.crcodec != nil && im.crcbDelay <= im.cslice {
			f := im.cbcodec.codeSlice(zp)
			if f != 0 {
				flag = 1
			}
			f = im.crcodec.codeSlice(zp)
			if f != 0 {
				flag = 1
			}
		}
		im.cslice++
	}
	// Note: the ZP decoder routinely over-reads a few bytes past the
	// last code byte of the chunk and fills with 0xff, exactly like the
	// reference implementation; this is not an error.
	im.cserial++
	return nslices, nil
}

// block is a 32x32 block of coefficients. Buckets (groups of sixteen
// coefficients) are allocated on demand, one array per bucket, matching
// the two level sparse structure of the reference implementation:
// allocating one bucket must not make its neighbors appear.
type block struct {
	pdata [4]*[16]*[16]int16
}

// data returns bucket n of the block, or nil if it was never written.
func (b *block) data(n int) []int16 {
	if p := b.pdata[n>>4]; p != nil {
		if bk := (*p)[n&15]; bk != nil {
			return bk[:]
		}
	}
	return nil
}

// dataAlloc returns bucket n of the block, allocating it on demand.
func (b *block) dataAlloc(n int) []int16 {
	if b.pdata[n>>4] == nil {
		b.pdata[n>>4] = &[16]*[16]int16{}
	}
	if (*b.pdata[n>>4])[n&15] == nil {
		(*b.pdata[n>>4])[n&15] = &[16]int16{}
	}
	return (*b.pdata[n>>4])[n&15][:]
}

// zigzag maps the linear coefficient index to the position in the 32x32
// liftblock (y*32+x), generated by interleaving the bits of the index
// into x and y.
var zigzag [1024]int

func init() {
	for n := 0; n < 1024; n++ {
		x, y := 0, 0
		m := n
		for i := 0; i < 5; i++ {
			x = x<<1 | m&1
			m >>= 1
			y = y<<1 | m&1
			m >>= 1
		}
		zigzag[n] = y*32 + x
	}
}
