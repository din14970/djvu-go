package iw44

import (
	"codeberg.org/din14970/djvu-go/internal/bzz"
)

// Coefficient states (IW44Image::Codec).
const (
	coefZero   = 1 // this coeff never hits this bit
	coefActive = 2 // this coeff is already active
	coefNew    = 4 // this coeff is becoming active
	coefUnk    = 8 // this coeff may become active
)

// codec decodes the slices of one component. Its state persists across
// the chunks of an image.
type codec struct {
	img     *Image
	curband int
	curbit  int
	quantLo [16]int
	quantHi [10]int
	// Per-slice scratch state.
	coeffstate  [256]int8
	bucketstate [16]int8
	// Coding contexts.
	ctxStart  [32]uint8
	ctxBucket [10][8]uint8
	ctxMant   uint8
	ctxRoot   uint8
}

func newCodec(img *Image) *codec {
	c := &codec{img: img, curbit: 1}
	i := 0
	q := 0
	for ; i < 4; i++ {
		c.quantLo[i] = iwQuant[q]
		q++
	}
	for j := 0; j < 4; j++ {
		c.quantLo[i] = iwQuant[q]
		i++
	}
	q++
	for j := 0; j < 4; j++ {
		c.quantLo[i] = iwQuant[q]
		i++
	}
	q++
	for j := 0; j < 4; j++ {
		c.quantLo[i] = iwQuant[q]
		i++
	}
	q++
	c.quantHi[0] = 0
	for j := 1; j < 10; j++ {
		c.quantHi[j] = iwQuant[q]
		q++
	}
	return c
}

// isNullSlice checks whether the current band and quantization can
// produce data, and prepares the coefficient states for band zero.
func (c *codec) isNullSlice(bit, band int) bool {
	if band == 0 {
		isNull := true
		for i := 0; i < 16; i++ {
			threshold := c.quantLo[i]
			c.coeffstate[i] = coefZero
			if threshold > 0 && threshold < 0x8000 {
				c.coeffstate[i] = coefUnk
				isNull = false
			}
		}
		return isNull
	}
	threshold := c.quantHi[band]
	return !(threshold > 0 && threshold < 0x8000)
}

// codeSlice decodes one slice (one bit of one band over all blocks).
// It returns 0 when no further slices can be decoded.
func (c *codec) codeSlice(zp *bzz.ZPDecoder) int {
	if c.curbit < 0 {
		return 0
	}
	if !c.isNullSlice(c.curbit, c.curband) {
		fbucket := bandBuckets[c.curband].start
		nbucket := bandBuckets[c.curband].size
		for blockno := 0; blockno < c.img.nb; blockno++ {
			c.decodeBuckets(zp, c.curbit, c.curband, &c.img.blocks[blockno], fbucket, nbucket)
		}
	}
	return c.finishCodeSlice()
}

// finishCodeSlice halves the quantization thresholds and advances to
// the next band or bit.
func (c *codec) finishCodeSlice() int {
	c.quantHi[c.curband] >>= 1
	if c.curband == 0 {
		for i := 0; i < 16; i++ {
			c.quantLo[i] >>= 1
		}
	}
	c.curband++
	if c.curband >= len(bandBuckets) {
		c.curband = 0
		c.curbit++
		if c.quantHi[len(bandBuckets)-1] == 0 {
			// All quantization thresholds are null.
			c.curbit = -1
			return 0
		}
	}
	return 1
}

// decodePrepare computes the bucket states before decoding.
func (c *codec) decodePrepare(fbucket, nbucket int, blk *block) int {
	bbstate := 0
	if fbucket != 0 {
		// Bands other than zero.
		for buckno := 0; buckno < nbucket; buckno++ {
			bstatetmp := 0
			pcoeff := blk.data(fbucket + buckno)
			if pcoeff == nil {
				bstatetmp = coefUnk
			} else {
				for i := 0; i < 16; i++ {
					cstatetmp := coefUnk
					if pcoeff[i] != 0 {
						cstatetmp = coefActive
					}
					c.coeffstate[buckno*16+i] = int8(cstatetmp)
					bstatetmp |= cstatetmp
				}
			}
			c.bucketstate[buckno] = int8(bstatetmp)
			bbstate |= bstatetmp
		}
	} else {
		// Band zero (fbucket==0 implies band==0 and nbucket==1).
		pcoeff := blk.data(0)
		if pcoeff == nil {
			bbstate = coefUnk
		} else {
			for i := 0; i < 16; i++ {
				cstatetmp := int(c.coeffstate[i])
				if cstatetmp != coefZero {
					cstatetmp = coefUnk
					if pcoeff[i] != 0 {
						cstatetmp = coefActive
					}
				}
				c.coeffstate[i] = int8(cstatetmp)
				bbstate |= cstatetmp
			}
		}
		c.bucketstate[0] = int8(bbstate)
	}
	return bbstate
}

// decodeBuckets decodes the buckets of one block in the current slice.
func (c *codec) decodeBuckets(zp *bzz.ZPDecoder, bit, band int, blk *block, fbucket, nbucket int) {
	bbstate := c.decodePrepare(fbucket, nbucket, blk)
	// Code root bit.
	if nbucket < 16 || bbstate&coefActive != 0 {
		bbstate |= coefNew
	} else if bbstate&coefUnk != 0 {
		if zp.Decode(&c.ctxRoot) != 0 {
			bbstate |= coefNew
		}
	}
	// Code bucket bits.
	if bbstate&coefNew != 0 {
		for buckno := 0; buckno < nbucket; buckno++ {
			if int(c.bucketstate[buckno])&coefUnk != 0 {
				ctx := 0
				if band > 0 {
					k := (fbucket + buckno) << 2
					b := blk.data(k >> 4)
					if b != nil {
						k &= 0xf
						if b[k] != 0 {
							ctx++
						}
						if b[k+1] != 0 {
							ctx++
						}
						if b[k+2] != 0 {
							ctx++
						}
						if ctx < 3 && b[k+3] != 0 {
							ctx++
						}
					}
				}
				if bbstate&coefActive != 0 {
					ctx |= 4
				}
				if zp.Decode(&c.ctxBucket[band][ctx]) != 0 {
					c.bucketstate[buckno] |= coefNew
				}
			}
		}
	}
	// Code new active coefficients (with their sign).
	if bbstate&coefNew != 0 {
		thres := c.quantHi[band]
		for buckno := 0; buckno < nbucket; buckno++ {
			if int(c.bucketstate[buckno])&coefNew == 0 {
				continue
			}
			pcoeff := blk.data(fbucket + buckno)
			if pcoeff == nil {
				pcoeff = blk.dataAlloc(fbucket + buckno)
				// Time to fill the coefficient states.
				if fbucket == 0 {
					for i := 0; i < 16; i++ {
						if int(c.coeffstate[buckno*16+i]) != coefZero {
							c.coeffstate[buckno*16+i] = coefUnk
						}
					}
				} else {
					for i := 0; i < 16; i++ {
						c.coeffstate[buckno*16+i] = coefUnk
					}
				}
			}
			gotcha := 0
			const maxgotcha = 7
			for i := 0; i < 16; i++ {
				if int(c.coeffstate[buckno*16+i])&coefUnk != 0 {
					gotcha++
				}
			}
			for i := 0; i < 16; i++ {
				cstate := int(c.coeffstate[buckno*16+i])
				if cstate&coefUnk == 0 {
					continue
				}
				if band == 0 {
					thres = c.quantLo[i]
				}
				ctx := 0
				if gotcha >= maxgotcha {
					ctx = maxgotcha
				} else {
					ctx = gotcha
				}
				if int(c.bucketstate[buckno])&coefActive != 0 {
					ctx |= 8
				}
				if zp.Decode(&c.ctxStart[ctx]) != 0 {
					c.coeffstate[buckno*16+i] |= coefNew
					halfthres := thres >> 1
					coeff := thres + halfthres - (halfthres >> 2)
					if zp.IWDecodePass() != 0 {
						pcoeff[i] = int16(-coeff)
					} else {
						pcoeff[i] = int16(coeff)
					}
				}
				if int(c.coeffstate[buckno*16+i])&coefNew != 0 {
					gotcha = 0
				} else if gotcha > 0 {
					gotcha--
				}
			}
		}
	}
	// Code mantissa bits.
	if bbstate&coefActive != 0 {
		thres := c.quantHi[band]
		for buckno := 0; buckno < nbucket; buckno++ {
			if int(c.bucketstate[buckno])&coefActive == 0 {
				continue
			}
			pcoeff := blk.data(fbucket + buckno)
			if pcoeff == nil {
				continue
			}
			for i := 0; i < 16; i++ {
				if int(c.coeffstate[buckno*16+i])&coefActive == 0 {
					continue
				}
				coeff := int(pcoeff[i])
				if coeff < 0 {
					coeff = -coeff
				}
				if band == 0 {
					thres = c.quantLo[i]
				}
				if coeff <= 3*thres {
					// Second mantissa bit.
					coeff += thres >> 2
					if zp.Decode(&c.ctxMant) != 0 {
						coeff += thres >> 1
					} else {
						coeff = coeff - thres + (thres >> 1)
					}
				} else {
					if zp.IWDecodePass() != 0 {
						coeff += thres >> 1
					} else {
						coeff = coeff - thres + (thres >> 1)
					}
				}
				if pcoeff[i] > 0 {
					pcoeff[i] = int16(coeff)
				} else {
					pcoeff[i] = int16(-coeff)
				}
			}
		}
	}
}
