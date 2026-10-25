// Go port of djvulibre's libdjvu/ZPCodec.cpp.
// Upstream copyright (c) 2002 Leon Bottou and Yann Le Cun; derived
// from the DjVu Reference Library (c) 1999-2001 LizardTech, Inc. This
// derivative work is licensed GPL-2.0-or-later; see the LICENSE file.

package bzz

import (
	"fmt"
	"io"
)

// ZPDecoder implements the decoding side of the ZP binary quasi-arithmetic
// coder used by BZZ, JB2 and IW44 data. It follows ZPCodec.cpp from the
// DjVu reference library (ZPCODER variant, djvucompat mode): a fixed
// adaptation table (zpTable), a 16 bit code register refilled from the
// input through a 32 bit preload buffer, and a "fence" fast path for the
// most probable symbol.
//
// The decoder tolerates reading up to 25 bytes past the end of the input
// before reporting an error, exactly like the reference implementation
// (the BZZ decoder routinely over-reads a few bytes at the end of a
// stream).
type ZPDecoder struct {
	src    io.Reader
	err    error
	a      uint32
	code   uint32
	buffer uint32
	scount int
	delay  int
	fence  uint32
	ffzt   [256]int
	p, m   [256]uint32
	up, dn [256]uint8
}

// NewZPDecoder creates a decoder reading code bytes from src.
func NewZPDecoder(src io.Reader) *ZPDecoder {
	z := &ZPDecoder{src: src}
	// Machine independent find-first-zero table: number of consecutive
	// one bits starting at the most significant bit of a byte.
	for i := 0; i < 256; i++ {
		n := 0
		for j := i; j&0x80 != 0; j <<= 1 {
			n++
		}
		z.ffzt[i] = n
	}
	for i := 0; i < 256; i++ {
		z.p[i] = zpTable[i].p
		z.m[i] = zpTable[i].m
		z.up[i] = zpTable[i].up
		z.dn[i] = zpTable[i].dn
	}
	// ZPCodec::Decode::init.
	z.a = 0
	z.code = uint32(z.readByte())<<8 | uint32(z.readByte())
	z.delay = 25
	z.scount = 0
	z.preload()
	z.fence = z.code
	if z.code >= 0x8000 {
		z.fence = 0x7fff
	}
	return z
}

// readByte reads one code byte, substituting 0xff at the end of the input.
// The delay counter is not touched here; only preload counts end-of-input
// encounters, following the reference implementation.
func (z *ZPDecoder) readByte() byte {
	var b [1]byte
	if n, _ := z.src.Read(b[:]); n < 1 {
		return 0xff
	}
	return b[0]
}

// preload refills the bit buffer until at least 25 bits are available.
// It counts end-of-input encounters against the 25 byte delay budget
// and records an error once it is exhausted.
func (z *ZPDecoder) preload() {
	for z.scount <= 24 {
		var b [1]byte
		n, _ := z.src.Read(b[:])
		if n < 1 {
			b[0] = 0xff
			z.delay--
			if z.delay < 1 {
				if z.err == nil {
					z.err = fmt.Errorf("bzz: unexpected end of compressed data: %w", io.ErrUnexpectedEOF)
				}
				return
			}
		}
		z.buffer = z.buffer<<8 | uint32(b[0])
		z.scount += 8
	}
}

// Decode decodes one message bit using (and adapting) the context
// variable pointed to by ctx. It returns 0 or 1.
func (z *ZPDecoder) Decode(ctx *uint8) int {
	zz := z.a + z.p[*ctx]
	if zz <= z.fence {
		z.a = zz
		return int(*ctx & 1)
	}
	return z.decodeSub(ctx, zz)
}

// DecodePass decodes one message bit without a context (pass-through).
func (z *ZPDecoder) DecodePass() int {
	return z.decodeSubSimple(0, 0x8000+(z.a>>1))
}

// decodeSub is ZPCodec::decode_sub: the core interval update for a coded
// bit, including context adaptation and renormalization.
func (z *ZPDecoder) decodeSub(ctx *uint8, zz uint32) int {
	bit := int(*ctx & 1)
	// Avoid interval reversion.
	if d := 0x6000 + ((zz + z.a) >> 2); zz > d {
		zz = d
	}
	if zz > z.code {
		// LPS branch.
		zz = 0x10000 - zz
		z.a += zz
		z.code += zz
		*ctx = z.dn[*ctx]
		// Renormalize.
		shift := z.ffz(z.a)
		z.scount -= shift
		z.a = (z.a << uint(shift)) & 0xffff
		z.code = ((z.code << uint(shift)) & 0xffff) | ((z.buffer >> uint(z.scount)) & uint32((1<<uint(shift))-1))
		if z.scount < 16 {
			z.preload()
		}
		z.fence = z.code
		if z.code >= 0x8000 {
			z.fence = 0x7fff
		}
		return bit ^ 1
	}
	// MPS branch.
	if z.a >= z.m[*ctx] {
		*ctx = z.up[*ctx]
	}
	z.scount--
	z.a = (zz << 1) & 0xffff
	z.code = ((z.code << 1) & 0xffff) | ((z.buffer >> uint(z.scount)) & 1)
	if z.scount < 16 {
		z.preload()
	}
	z.fence = z.code
	if z.code >= 0x8000 {
		z.fence = 0x7fff
	}
	return bit
}

// decodeSubSimple is ZPCodec::decode_sub_simple for context-less bits.
func (z *ZPDecoder) decodeSubSimple(mps int, zz uint32) int {
	if zz > z.code {
		zz = 0x10000 - zz
		z.a += zz
		z.code += zz
		shift := z.ffz(z.a)
		z.scount -= shift
		z.a = (z.a << uint(shift)) & 0xffff
		z.code = ((z.code << uint(shift)) & 0xffff) | ((z.buffer >> uint(z.scount)) & uint32((1<<uint(shift))-1))
		if z.scount < 16 {
			z.preload()
		}
		z.fence = z.code
		if z.code >= 0x8000 {
			z.fence = 0x7fff
		}
		return mps ^ 1
	}
	z.scount--
	z.a = (zz << 1) & 0xffff
	z.code = ((z.code << 1) & 0xffff) | ((z.buffer >> uint(z.scount)) & 1)
	if z.scount < 16 {
		z.preload()
	}
	z.fence = z.code
	if z.code >= 0x8000 {
		z.fence = 0x7fff
	}
	return mps
}

// Err reports the first end-of-input error encountered by the preload
// buffer once the 25 byte delay budget is exhausted. Decoders with no
// natural end marker should check it periodically: after it is set,
// decoding continues but yields deterministic garbage.
func (z *ZPDecoder) Err() error {
	return z.err
}

// ffz returns the number of consecutive one bits at the top of the
// 16 bit value x (ZPCodec::ffz).
func (z *ZPDecoder) ffz(x uint32) int {
	if x >= 0xff00 {
		return z.ffzt[x&0xff] + 8
	}
	return z.ffzt[(x>>8)&0xff]
}

// IWDecodePass decodes one message bit with the IW44 variant of the
// context-less decoder (ZPCodec::IWdecoder), used for coefficient signs
// and mantissa adjustments in IW44 data.
func (z *ZPDecoder) IWDecodePass() int {
	return z.decodeSubSimple(0, 0x8000+((z.a+z.a+z.a)>>3))
}
