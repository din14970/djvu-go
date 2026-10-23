// Package zptest provides a reference-compatible ZP encoder for tests.
// It is a port of the encoding half of ZPCodec.cpp (ZPCODER variant,
// djvucompat mode) and exists so that decoder tests can produce streams
// without depending on external tools. It is not used by the library
// itself.
package zptest

import (
	"io"

	"codeberg.org/din14970/djvu-go/internal/bzz"
)

// Encoder implements the ZP arithmetic encoder.
type Encoder struct {
	w      io.Writer
	a      uint32
	subend uint32
	buffer uint32
	scount int
	delay  int
	cur    byte
	nrun   uint32
	p, m   [256]uint32
	up, dn [256]uint8
}

// NewEncoder creates an encoder writing code bytes to w.
func NewEncoder(w io.Writer) *Encoder {
	e := &Encoder{w: w, delay: 25, buffer: 0xffffff}
	for i := 0; i < 256; i++ {
		e.p[i] = bzz.TableProb(i)
		e.m[i] = bzz.TableThreshold(i)
		e.up[i] = bzz.TableUp(i)
		e.dn[i] = bzz.TableDown(i)
	}
	return e
}

func (e *Encoder) outbit(bit int) {
	if e.delay > 0 {
		if e.delay < 0xff {
			e.delay--
		}
	} else {
		e.cur = e.cur<<1 | byte(bit)
		e.scount++
		if e.scount == 8 {
			if _, err := e.w.Write([]byte{e.cur}); err != nil {
				panic(err) // writing to a bytes.Buffer never fails
			}
			e.scount = 0
			e.cur = 0
		}
	}
}

func (e *Encoder) zemit(bit uint32) {
	e.buffer = e.buffer<<1 + bit
	b := e.buffer >> 24
	e.buffer &= 0xffffff
	switch b {
	case 1:
		e.outbit(1)
		for e.nrun > 0 {
			e.outbit(0)
			e.nrun--
		}
		e.nrun = 0
	case 0xff:
		e.outbit(0)
		for e.nrun > 0 {
			e.outbit(1)
			e.nrun--
		}
		e.nrun = 0
	case 0:
		e.nrun++
	}
}

func (e *Encoder) clamp(z *uint32) {
	if d := 0x6000 + ((*z + e.a) >> 2); *z > d {
		*z = d
	}
}

// Encode encodes one message bit using (and adapting) the context
// variable pointed to by ctx.
func (e *Encoder) Encode(bit int, ctx *uint8) {
	z := e.a + e.p[*ctx]
	if bit != int(*ctx&1) {
		// LPS
		e.clamp(&z)
		*ctx = e.dn[*ctx]
		z = 0x10000 - z
		e.subend += z
		e.a += z
		for e.a >= 0x8000 {
			e.zemit(1 - (e.subend >> 15))
			e.subend = e.subend << 1 & 0xffff
			e.a = e.a << 1 & 0xffff
		}
	} else if z >= 0x8000 {
		// MPS
		e.clamp(&z)
		if e.a >= e.m[*ctx] {
			*ctx = e.up[*ctx]
		}
		e.a = z
		if e.a >= 0x8000 {
			e.zemit(1 - (e.subend >> 15))
			e.subend = e.subend << 1 & 0xffff
			e.a = e.a << 1 & 0xffff
		}
	} else {
		e.a = z
	}
}

// EncodePass encodes one message bit without a context.
func (e *Encoder) EncodePass(bit int) {
	z := uint32(0x8000 + (e.a >> 1))
	if bit != 0 {
		z = 0x10000 - z
		e.subend += z
		e.a += z
		for e.a >= 0x8000 {
			e.zemit(1 - (e.subend >> 15))
			e.subend = e.subend << 1 & 0xffff
			e.a = e.a << 1 & 0xffff
		}
	} else {
		e.a = z
		if e.a >= 0x8000 {
			e.zemit(1 - (e.subend >> 15))
			e.subend = e.subend << 1 & 0xffff
			e.a = e.a << 1 & 0xffff
		}
	}
}

// Flush terminates the code stream (ZPCodec::Encode::~Encode).
func (e *Encoder) Flush() {
	if e.subend > 0x8000 {
		e.subend = 0x10000
	} else if e.subend > 0 {
		e.subend = 0x8000
	}
	for e.buffer != 0xffffff || e.subend != 0 {
		e.zemit(1 - (e.subend >> 15))
		e.subend = e.subend << 1 & 0xffff
	}
	e.outbit(1)
	for e.nrun > 0 {
		e.outbit(0)
		e.nrun--
	}
	for e.scount > 0 {
		e.outbit(1)
	}
	e.delay = 0xff
}
