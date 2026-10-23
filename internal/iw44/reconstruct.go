package iw44

// This file implements the coefficient reconstruction: the inverse IW44
// lifting transform (filter_bv, filter_bh, backward) and the full and
// subsampled image paths of IW44Image::Map::image.

// filterBV applies the vertical lifting step. p is the coefficient
// matrix, base the index of the first processed row, w the width, h the
// (scaled) number of rows, rowsize the row stride of the matrix and
// scale the spacing of processed rows (1, 2, 4...).
func filterBV(p []int16, base, w, h, rowsize, scale int) {
	s := scale * rowsize
	s3 := s + s + s
	hh := ((h - 1) / scale) + 1
	y := 0
	idx := base
	for y-3 < hh {
		// 1-Lifting on row y.
		if y >= 3 && y+3 < hh {
			// Generic case.
			for q := idx; q < idx+w; q += scale {
				a := int(p[q-s]) + int(p[q+s])
				b := int(p[q-s3]) + int(p[q+s3])
				p[q] -= int16(((a << 3) + a - b + 16) >> 5)
			}
		} else if y < hh {
			// Special cases near the borders.
			q1, hasQ1 := 0, false
			if y+1 < hh {
				q1, hasQ1 = idx+s, true
			}
			q3, hasQ3 := 0, false
			if y+3 < hh {
				q3, hasQ3 = idx+s3, true
			}
			for q := idx; q < idx+w; q += scale {
				var a, b int
				if y >= 3 {
					a = int(p[q-s])
					if hasQ1 {
						a += int(p[q1])
					}
					b = int(p[q-s3])
					if hasQ3 {
						b += int(p[q3])
					}
				} else if y >= 1 {
					a = int(p[q-s])
					if hasQ1 {
						a += int(p[q1])
					}
					if hasQ3 {
						b = int(p[q3])
					}
				} else {
					if hasQ1 {
						a = int(p[q1])
					}
					if hasQ3 {
						b = int(p[q3])
					}
				}
				p[q] -= int16(((a << 3) + a - b + 16) >> 5)
				if hasQ1 {
					q1 += scale
				}
				if hasQ3 {
					q3 += scale
				}
			}
		}
		// 2-Interpolation on row y-3.
		qbase := idx - s3
		if y >= 6 && y < hh {
			// Generic case.
			for q := qbase; q < qbase+w; q += scale {
				a := int(p[q-s]) + int(p[q+s])
				b := int(p[q-s3]) + int(p[q+s3])
				p[q] += int16(((a << 3) + a - b + 8) >> 4)
			}
		} else if y >= 3 {
			// Special cases.
			q1off := s
			if y-2 >= hh {
				q1off = -s
			}
			for q := qbase; q < qbase+w; q += scale {
				a := int(p[q-s]) + int(p[q+q1off])
				p[q] += int16((a + 1) >> 1)
			}
		}
		y += 2
		idx += s + s
	}
}

// filterBH applies the horizontal lifting step. See filterBV.
func filterBH(p []int16, base, w, h, rowsize, scale int) {
	s := scale
	s3 := s + s + s
	rowstep := rowsize * scale
	for y := 0; y < h; y += scale {
		// Process the row starting at base.
		row := base + (y/scale)*rowstep
		var a0, a1, a2, a3 int
		var b0, b1, b2, b3 int
		q := row
		e := row + w
		if q < e {
			// Special case: x=0.
			a1, a0 = 0, 0
			a2, a3 = 0, 0
			if q+s < e {
				a2 = int(p[q+s])
			}
			if q+s3 < e {
				a3 = int(p[q+s3])
			}
			b2 = int(p[q]) - ((((a1 + a2) << 3) + (a1 + a2) - a0 - a3 + 16) >> 5)
			b3 = b2
			p[q] = int16(b3)
			q += s + s
		}
		if q < e {
			// Special case: x=2.
			a0, a1 = a1, a2
			a2 = a3
			if q+s3 < e {
				a3 = int(p[q+s3])
			}
			b3 = int(p[q]) - ((((a1 + a2) << 3) + (a1 + a2) - a0 - a3 + 16) >> 5)
			p[q] = int16(b3)
			q += s + s
		}
		if q < e {
			// Special case: x=4.
			b1, b2 = b2, b3
			a0, a1 = a1, a2
			a2 = a3
			if q+s3 < e {
				a3 = int(p[q+s3])
			}
			b3 = int(p[q]) - ((((a1 + a2) << 3) + (a1 + a2) - a0 - a3 + 16) >> 5)
			p[q] = int16(b3)
			p[q-s3] += int16((b1 + b2 + 1) >> 1)
			q += s + s
		}
		for q+s3 < e {
			// Generic case.
			a0, a1 = a1, a2
			a2 = a3
			a3 = int(p[q+s3])
			b0, b1 = b1, b2
			b2 = b3
			b3 = int(p[q]) - ((((a1 + a2) << 3) + (a1 + a2) - a0 - a3 + 16) >> 5)
			p[q] = int16(b3)
			p[q-s3] += int16((((b1 + b2) << 3) + (b1 + b2) - b0 - b3 + 8) >> 4)
			q += s + s
		}
		for q < e {
			// Special case: w-3 <= x < w.
			a0, a1 = a1, a2
			a2 = a3
			a3 = 0
			b0, b1 = b1, b2
			b2 = b3
			b3 = int(p[q]) - ((((a1 + a2) << 3) + (a1 + a2) - a0 - a3 + 16) >> 5)
			p[q] = int16(b3)
			p[q-s3] += int16((((b1 + b2) << 3) + (b1 + b2) - b0 - b3 + 8) >> 4)
			q += s + s
		}
		for q-s3 < e {
			// Special case: w <= x < w+3.
			b1, b2 = b2, b3
			if q-s3 >= row {
				p[q-s3] += int16((b1 + b2 + 1) >> 1)
			}
			q += s + s
		}
	}
}

// backward applies the inverse transform between scale intervals
// [begin, end): IW44Image::Transform::Decode::backward.
func backward(p []int16, base, w, h, rowsize, begin, end int) {
	for scale := begin >> 1; scale >= end; scale >>= 1 {
		filterBV(p, base, w, h, rowsize, scale)
		filterBH(p, base, w, h, rowsize, scale)
	}
}

// writeLiftblock copies the sparse block coefficients into a dense
// 32x32 liftblock applying the zigzag ordering.
func (im *Image) writeLiftblock(blk *block, liftblock *[1024]int16, bmin, bmax int) {
	for i := range liftblock {
		liftblock[i] = 0
	}
	n := bmin << 4
	for n1 := bmin; n1 < bmax; n1++ {
		d := blk.data(n1)
		if d == nil {
			n += 16
			continue
		}
		for n2 := 0; n2 < 16; n2++ {
			liftblock[zigzag[n]] = d[n2]
			n++
		}
	}
}

// reconstruct renders the coefficient map into an 8 bit component plane
// at full resolution. fast skips the finest reconstruction level by
// 2x2 duplication (used for reduced chroma).
func (im *Image) reconstruct(fast int) []int8 {
	data := make([]int16, im.bw*im.bh)
	// Copy coefficients.
	blockno := 0
	var liftblock [1024]int16
	for i := 0; i < im.bh; i += 32 {
		for j := 0; j < im.bw; j += 32 {
			im.writeLiftblock(&im.blocks[blockno], &liftblock, 0, 64)
			blockno++
			pp := i*im.bw + j
			pl := 0
			for ii := 0; ii < 32; ii++ {
				copy(data[pp:pp+32], liftblock[pl:pl+32])
				pp += im.bw
				pl += 32
			}
		}
	}
	// Reconstruction.
	if fast != 0 {
		backward(data, 0, im.w, im.h, im.bw, 32, 2)
		// Duplicate every other sample into 2x2 blocks.
		for i := 0; i < im.bh; i += 2 {
			row := i * im.bw
			for j := 0; j < im.bw; j += 2 {
				v := data[row+j]
				data[row+j+1] = v
				data[row+j+im.bw] = v
				data[row+j+im.bw+1] = v
			}
		}
	} else {
		backward(data, 0, im.w, im.h, im.bw, 32, 1)
	}
	// Copy result, rounding and clamping.
	out := make([]int8, im.w*im.h)
	for i := 0; i < im.h; i++ {
		row := i * im.bw
		orow := i * im.w
		for j := 0; j < im.w; j++ {
			x := (int(data[row+j]) + iwRound) >> iwShift
			if x < -128 {
				x = -128
			} else if x > 127 {
				x = 127
			}
			out[orow+j] = int8(x)
		}
	}
	return out
}

// reconstructSub renders the coefficient map subsampled by a factor of
// 2, 4, 8 or 16, using the multiresolution reconstruction of
// IW44Image::Map::image(int subsample, ...). fast applies the coarse
// chroma shortcut.
func (im *Image) reconstructSub(subsample int, fast int) []int8 {
	// Number of decomposition levels.
	nlevel := 0
	for nlevel < 5 && (32>>nlevel) > subsample {
		nlevel++
	}
	if subsample != 32>>nlevel {
		// Only power of two subsampling factors up to 16 are supported.
		return im.reconstruct(fast)
	}
	boxsize := 1 << nlevel
	iw, ih := im.w, im.h
	irectW := (iw + subsample - 1) / subsample
	irectH := (ih + subsample - 1) / subsample

	type rect struct{ xmin, ymin, xmax, ymax int }
	// Multiresolution rectangles (indices by level).
	needed := make([]rect, nlevel+1)
	recomp := make([]rect, nlevel+1)
	r := 1
	needed[nlevel] = rect{0, 0, irectW, irectH}
	recomp[nlevel] = rect{0, 0, irectW, irectH}
	for i := nlevel - 1; i >= 0; i-- {
		n := recomp[i+1]
		n.xmin -= iwBorder * r
		n.ymin -= iwBorder * r
		n.xmax += iwBorder * r
		n.ymax += iwBorder * r
		if n.xmin < 0 {
			n.xmin = 0
		}
		if n.ymin < 0 {
			n.ymin = 0
		}
		if n.xmax > irectW {
			n.xmax = irectW
		}
		if n.ymax > irectH {
			n.ymax = irectH
		}
		needed[i] = n
		r += r
		recomp[i].xmin = (n.xmin + r - 1) &^ (r - 1)
		recomp[i].xmax = n.xmax &^ (r - 1)
		recomp[i].ymin = (n.ymin + r - 1) &^ (r - 1)
		recomp[i].ymax = n.ymax &^ (r - 1)
	}
	// Working rectangle.
	var work rect
	work.xmin = needed[0].xmin &^ (boxsize - 1)
	work.ymin = needed[0].ymin &^ (boxsize - 1)
	work.xmax = ((needed[0].xmax - 1) &^ (boxsize - 1)) + boxsize
	work.ymax = ((needed[0].ymax - 1) &^ (boxsize - 1)) + boxsize
	dataw := work.xmax - work.xmin
	datah := work.ymax - work.ymin
	data := make([]int16, dataw*datah)

	// Fill working rectangle from the liftblocks.
	blkw := im.bw >> 5
	for by := work.ymin; by < work.ymax; by += boxsize {
		for bx := work.xmin; bx < work.xmax; bx += boxsize {
			// Decide how much to load.
			mlevel := nlevel
			if nlevel > 2 {
				if bx+31 < needed[2].xmin || bx > needed[2].xmax ||
					by+31 < needed[2].ymin || by > needed[2].ymax {
					mlevel = 2
				}
			}
			bmax := ((1 << (mlevel + mlevel)) + 15) >> 4
			ppinc := 1 << (nlevel - mlevel)
			ppmod1 := dataw << (nlevel - mlevel)
			ttmod0 := 32 >> mlevel
			ttmod1 := ttmod0 << 5
			block := &im.blocks[(by>>nlevel)*blkw+(bx>>nlevel)]
			var liftblock [1024]int16
			im.writeLiftblock(block, &liftblock, 0, bmax)
			// Copy liftblock into data.
			tt := 0
			pp := (by-work.ymin)*dataw + (bx - work.xmin)
			for ii := 0; ii < boxsize; ii, pp, tt = ii+ppinc, pp+ppmod1, tt+ttmod1-32 {
				for jj := 0; jj < boxsize; jj, tt = jj+ppinc, tt+ttmod0 {
					data[pp+jj] = liftblock[tt]
				}
			}
		}
	}
	// Perform reconstruction.
	r = boxsize
	for i := 0; i < nlevel; i++ {
		comp := needed[i]
		comp.xmin &= ^(r - 1)
		comp.ymin &= ^(r - 1)
		comp.xmin -= work.xmin
		comp.ymin -= work.ymin
		// Fast mode shortcuts the finest resolution.
		if fast != 0 && i >= 4 {
			pp := comp.ymin*dataw + comp.xmin
			for ii := comp.ymin; ii < comp.ymax; ii += 2 {
				for jj := comp.xmin; jj < comp.xmax; jj += 2 {
					v := data[pp+jj]
					data[pp+jj+1] = v
					data[pp+jj+dataw] = v
					data[pp+jj+dataw+1] = v
				}
				pp += 2 * dataw
			}
			break
		}
		backward(data, comp.ymin*dataw+comp.xmin, comp.xmax-comp.xmin, comp.ymax-comp.ymin, dataw, r, r>>1)
		r >>= 1
	}
	// Copy result (the requested rect is the whole subsampled image).
	out := make([]int8, irectW*irectH)
	for i := 0; i < irectH; i++ {
		row := i * dataw
		orow := i * irectW
		for j := 0; j < irectW; j++ {
			x := (int(data[row+j]) + iwRound) >> iwShift
			if x < -128 {
				x = -128
			} else if x > 127 {
				x = 127
			}
			out[orow+j] = int8(x)
		}
	}
	return out
}

// component renders one component at the given subsampling factor
// (1, 2, 4, 8 or 16).
func (im *Image) component(subsample, fast int) []int8 {
	if im.w == 0 {
		return nil
	}
	if subsample <= 1 {
		return im.reconstruct(fast)
	}
	return im.reconstructSub(subsample, fast)
}
