// Package bzz implements the decoding side of the BZZ general purpose
// compressor used throughout the DjVu format (DIRM directories, ANTz
// annotations, TXTz hidden text, NAVM outlines, FGbz palettes).
//
// BZZ is a Burrows-Wheeler block sorting coder closely modeled on bzip2.
// The compressed stream is a sequence of blocks. Each block starts with
// a 24 bit big endian stored size (coding "size-1" payload bytes: the
// encoder appends one dummy byte to every block), followed by one or two
// estimation-speed flag bits and the move-to-front coded, ZP arithmetic
// coded Burrows-Wheeler transform of the block. A stored size of zero
// marks the end of the stream.
//
// This package is a faithful Go port of the decoding half of
// BSByteStream.cpp from the DjVu reference library. It does not
// implement BZZ compression.
package bzz

import (
	"bufio"
	"fmt"
	"io"
)

// Limits from BSByteStream.h in the reference implementation.
const (
	maxBlock = 4096 * 1024 // maximum stored block size (MAXBLOCK*1024)
	freqMax  = 4           // FREQMAX
	ctxIDs   = 3           // CTXIDS
	numCtx   = 300         // size of the context array
)

// Reader decompresses a BZZ stream. It implements io.Reader.
type Reader struct {
	zp    *ZPDecoder
	ctx   [numCtx]uint8
	data  []byte // current block
	bptr  int    // read offset within data
	avail int    // bytes remaining in the current block
	eof   bool
	err   error
}

// NewReader returns a Reader that decompresses the BZZ stream read from r.
func NewReader(r io.Reader) *Reader {
	// The ZP decoder consumes its input one byte at a time; buffer it so
	// that unbuffered sources do not pay a syscall or interface call per
	// byte. Over-reading at the end of the stream is harmless: the ZP
	// decoder is designed to read a few bytes past the last code byte.
	return &Reader{zp: NewZPDecoder(bufio.NewReader(r))}
}

// Read decompresses up to len(p) bytes. It returns io.EOF once the end of
// the BZZ stream has been reached, or an error if the stream is corrupt
// or truncated.
func (r *Reader) Read(p []byte) (int, error) {
	copied := 0
	for len(p) > 0 && !r.eof {
		if r.avail == 0 {
			r.bptr = 0
			n, err := r.decodeBlock()
			if err != nil {
				r.err = err
				break
			}
			if n == 0 {
				r.eof = true
				break
			}
			// The encoder stores one byte more than the block payload.
			r.avail = n - 1
			continue
		}
		n := r.avail
		if n > len(p) {
			n = len(p)
		}
		copy(p, r.data[r.bptr:r.bptr+n])
		p = p[n:]
		r.bptr += n
		r.avail -= n
		copied += n
	}
	if copied > 0 {
		return copied, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	if r.eof {
		return 0, io.EOF
	}
	return 0, nil
}

// ReadAll decompresses the whole stream. It is a convenience wrapper
// around NewReader and io.ReadAll.
func ReadAll(src io.Reader) ([]byte, error) {
	return io.ReadAll(NewReader(src))
}

// decodeBlock decodes the next block into r.data. It returns the stored
// size of the block; zero marks the end of the stream.
func (r *Reader) decodeBlock() (int, error) {
	size := decodeRaw(r.zp, 24)
	if size == 0 {
		return 0, nil
	}
	if size > maxBlock || r.zp.err != nil {
		return 0, fmt.Errorf("bzz: corrupt block header")
	}
	if cap(r.data) < size {
		r.data = make([]byte, size)
	}
	r.data = r.data[:size]

	// Decode the estimation speed flag bits.
	fshift := 0
	if r.zp.DecodePass() != 0 {
		fshift++
		if r.zp.DecodePass() != 0 {
			fshift++
		}
	}

	// Move-to-front decoding with frequency-ranked rotation.
	var mtf [256]uint8
	for i := range mtf {
		mtf[i] = uint8(i)
	}
	var freq [freqMax]uint32
	fadd := uint32(4)
	mtfno := 3
	markerpos := -1

	for i := 0; i < size; i++ {
		ctxid := ctxIDs - 1
		if ctxid > mtfno {
			ctxid = mtfno
		}
		cx := 0
		c := -1
		if r.zp.Decode(&r.ctx[cx+ctxid]) != 0 {
			mtfno = 0
			c = int(mtf[0])
		} else {
			cx += ctxIDs
			if r.zp.Decode(&r.ctx[cx+ctxid]) != 0 {
				mtfno = 1
				c = int(mtf[1])
			} else {
				cx += ctxIDs
				if r.zp.Decode(&r.ctx[cx]) != 0 {
					mtfno = 2 + decodeBinary(r.zp, r.ctx[cx+1:], 1)
					c = int(mtf[mtfno])
				} else {
					cx += 2
					if r.zp.Decode(&r.ctx[cx]) != 0 {
						mtfno = 4 + decodeBinary(r.zp, r.ctx[cx+1:], 2)
						c = int(mtf[mtfno])
					} else {
						cx += 4
						if r.zp.Decode(&r.ctx[cx]) != 0 {
							mtfno = 8 + decodeBinary(r.zp, r.ctx[cx+1:], 3)
							c = int(mtf[mtfno])
						} else {
							cx += 8
							if r.zp.Decode(&r.ctx[cx]) != 0 {
								mtfno = 16 + decodeBinary(r.zp, r.ctx[cx+1:], 4)
								c = int(mtf[mtfno])
							} else {
								cx += 16
								if r.zp.Decode(&r.ctx[cx]) != 0 {
									mtfno = 32 + decodeBinary(r.zp, r.ctx[cx+1:], 5)
									c = int(mtf[mtfno])
								} else {
									cx += 32
									if r.zp.Decode(&r.ctx[cx]) != 0 {
										mtfno = 64 + decodeBinary(r.zp, r.ctx[cx+1:], 6)
										c = int(mtf[mtfno])
									} else {
										cx += 64
										if r.zp.Decode(&r.ctx[cx]) != 0 {
											mtfno = 128 + decodeBinary(r.zp, r.ctx[cx+1:], 7)
											c = int(mtf[mtfno])
										} else {
											// Zero marker: end of the block
											// transformed string.
											mtfno = 256
											markerpos = i
										}
									}
								}
							}
						}
					}
				}
			}
		}
		if c >= 0 {
			r.data[i] = uint8(c)
			// Rotate the mtf list according to empirical frequencies.
			fadd += fadd >> uint(fshift)
			if fadd > 0x10000000 {
				fadd >>= 24
				freq[0] >>= 24
				freq[1] >>= 24
				freq[2] >>= 24
				freq[3] >>= 24
			}
			fc := fadd
			if mtfno < freqMax {
				fc += freq[mtfno]
			}
			k := mtfno
			for ; k >= freqMax; k-- {
				mtf[k] = mtf[k-1]
			}
			for ; k > 0 && fc >= freq[k-1]; k-- {
				mtf[k] = mtf[k-1]
				freq[k] = freq[k-1]
			}
			mtf[k] = uint8(c)
			freq[k] = fc
		} else {
			r.data[i] = 0
		}
	}
	if r.zp.err != nil {
		return 0, r.zp.err
	}
	return size, r.invertBWT(size, markerpos)
}

// invertBWT undoes the Burrows-Wheeler transform on r.data, reconstructing
// the original block content in place.
func (r *Reader) invertBWT(size, markerpos int) error {
	if markerpos < 1 || markerpos >= size {
		return fmt.Errorf("bzz: corrupt block: invalid marker position %d", markerpos)
	}
	posn := make([]uint32, size)
	var count [256]int
	for i := 0; i < markerpos; i++ {
		c := r.data[i]
		posn[i] = uint32(c)<<24 | uint32(count[c]&0xffffff)
		count[c]++
	}
	for i := markerpos + 1; i < size; i++ {
		c := r.data[i]
		posn[i] = uint32(c)<<24 | uint32(count[c]&0xffffff)
		count[c]++
	}
	// Sorted character positions.
	last := 1
	for i := 0; i < 256; i++ {
		tmp := count[i]
		count[i] = last
		last += tmp
	}
	// Undo the sort transform.
	i := 0
	last = size - 1
	for last > 0 {
		n := posn[i]
		c := byte(n >> 24)
		last--
		r.data[last] = c
		i = count[c] + int(n&0xffffff)
	}
	if i != markerpos {
		return fmt.Errorf("bzz: corrupt block: reconstructed stream is inconsistent")
	}
	return nil
}

// decodeRaw decodes an n-bit number with the pass-through (context-less)
// decoder.
func decodeRaw(zp *ZPDecoder, bits int) int {
	n := 1
	m := 1 << bits
	for n < m {
		n = n<<1 | zp.DecodePass()
	}
	return n - m
}

// decodeBinary decodes an n-bit number using the tree of contexts rooted
// at ctx. It mirrors decode_binary in BSByteStream.cpp.
func decodeBinary(zp *ZPDecoder, ctx []uint8, bits int) int {
	n := 1
	m := 1 << bits
	for n < m {
		b := zp.Decode(&ctx[n-1])
		n = n<<1 | b
	}
	return n - m
}
