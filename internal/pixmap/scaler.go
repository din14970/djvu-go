package pixmap

// fracBits is the fixed point precision of the scaler coordinates.
const (
	fracBits  = 4
	fracSize  = 1 << fracBits
	fracSize2 = fracSize / 2
	fracMask  = fracSize - 1
)

var interp [fracSize][512]int16

func init() {
	for i := 0; i < fracSize; i++ {
		for j := -255; j <= 255; j++ {
			interp[i][256+j] = int16((j*i + fracSize2) >> fracBits)
		}
	}
}

// Rect is a rectangle in the coordinate system of the scaler: the
// origin of a pixmap, x growing right and y growing up.
type Rect struct {
	XMin, YMin, XMax, YMax int
}

// Width returns the width of the rectangle.
func (r Rect) Width() int { return r.XMax - r.XMin }

// Height returns the height of the rectangle.
func (r Rect) Height() int { return r.YMax - r.YMin }

// Scaler resizes pixmaps with the bilinear interpolation of the
// reference implementation (GPixmapScaler): coordinates are computed
// with a Bresenham algorithm at 1/16 pixel precision, and large
// reductions first average 2^shift blocks.
type Scaler struct {
	inw, inh, outw, outh int
	xshift, yshift       int
	redw, redh           int
	hcoord, vcoord       []int
	// Reduced line cache for the pre-averaging path.
	gp1, gp2 []byte
	gl1, gl2 int
}

// NewScaler creates a scaler mapping an input of inw x inh pixels to
// an output of outw x outh pixels.
func NewScaler(inw, inh, outw, outh int) *Scaler {
	if inw < 1 || inh < 1 || outw < 1 || outh < 1 {
		inw, inh, outw, outh = 1, 1, 1, 1
	}
	return &Scaler{inw: inw, inh: inh, outw: outw, outh: outh}
}

// SetHorzRatio overrides the horizontal reduction ratio (the number of
// output pixels generated per input pixel is numer/denom). Zero values
// select the implicit ratio given by the sizes.
func (s *Scaler) SetHorzRatio(numer, denom int) {
	if numer == 0 && denom == 0 {
		numer, denom = s.outw, s.inw
	} else if numer <= 0 || denom <= 0 {
		numer, denom = s.outw, s.inw
	}
	s.xshift = 0
	s.redw = s.inw
	for numer+numer < denom {
		s.xshift++
		s.redw = (s.redw + 1) >> 1
		numer <<= 1
	}
	s.hcoord = make([]int, s.outw)
	prepareCoord(s.hcoord, s.redw, s.outw, denom, numer)
}

// SetVertRatio overrides the vertical reduction ratio. See SetHorzRatio.
func (s *Scaler) SetVertRatio(numer, denom int) {
	if numer == 0 && denom == 0 {
		numer, denom = s.outh, s.inh
	} else if numer <= 0 || denom <= 0 {
		numer, denom = s.outh, s.inh
	}
	s.yshift = 0
	s.redh = s.inh
	for numer+numer < denom {
		s.yshift++
		s.redh = (s.redh + 1) >> 1
		numer <<= 1
	}
	s.vcoord = make([]int, s.outh)
	prepareCoord(s.vcoord, s.redh, s.outh, denom, numer)
}

// prepareCoord computes the Bresenham coordinate table mapping output
// positions to fixed point input positions.
func prepareCoord(coord []int, inmax, outmax, in, out int) {
	length := in * fracSize
	beg := (length+out)/(2*out) - fracSize2
	y := beg
	z := out / 2
	inmaxlim := (inmax - 1) * fracSize
	for x := 0; x < outmax; x++ {
		coord[x] = min(y, inmaxlim)
		z += length
		y += z / out
		z %= out
	}
}

// makeRectangles computes the reduced and input rectangles required
// for a desired output rectangle.
func (s *Scaler) makeRectangles(desired Rect) (red, inp Rect) {
	if s.hcoord == nil {
		s.SetHorzRatio(0, 0)
	}
	if s.vcoord == nil {
		s.SetVertRatio(0, 0)
	}
	red.XMin = s.hcoord[desired.XMin] >> fracBits
	red.YMin = s.vcoord[desired.YMin] >> fracBits
	red.XMax = (s.hcoord[desired.XMax-1] + fracSize - 1) >> fracBits
	red.YMax = (s.vcoord[desired.YMax-1] + fracSize - 1) >> fracBits
	red.XMin = max(red.XMin, 0)
	red.XMax = min(red.XMax+1, s.redw)
	red.YMin = max(red.YMin, 0)
	red.YMax = min(red.YMax+1, s.redh)
	// Input rectangle borders.
	inp.XMin = max(red.XMin<<s.xshift, 0)
	inp.XMax = min(red.XMax<<s.xshift, s.inw)
	inp.YMin = max(red.YMin<<s.yshift, 0)
	inp.YMax = min(red.YMax<<s.yshift, s.inh)
	return red, inp
}

// Scale copies the input pixmap into the output, scaling it. provided
// is the rectangle of the input covered by in; desired is the
// rectangle of the output to compute. This replicates
// GPixmapScaler::scale, including the 2^shift pre-averaging for large
// reductions.
func (s *Scaler) Scale(provided Rect, in *Pixmap, desired Rect, out *Pixmap) {
	red, inp := s.makeRectangles(desired)
	if provided.Width() != in.W || provided.Height() != in.H {
		panic("pixmap: scaler input rectangle does not match pixmap")
	}
	if provided.XMin > inp.XMin || provided.YMin > inp.YMin ||
		provided.XMax < inp.XMax || provided.YMax < inp.YMax {
		panic("pixmap: scaler input rectangle too small")
	}
	if out.W != desired.Width() || out.H != desired.Height() {
		*out = *New(desired.Width(), desired.Height())
	}
	bufw := red.Width()
	lbuf := make([]byte, 3*(bufw+2))
	if s.xshift > 0 || s.yshift > 0 {
		s.gp1, s.gp2 = make([]byte, 3*bufw), make([]byte, 3*bufw)
		s.gl1, s.gl2 = -1, -1
	}
	for y := desired.YMin; y < desired.YMax; y++ {
		// Vertical interpolation.
		fy := s.vcoord[y]
		fy1 := fy >> fracBits
		fy2 := fy1 + 1
		var lower, upper []byte
		if s.xshift > 0 || s.yshift > 0 {
			lower = s.getLine(fy1, red, provided, in)
			upper = s.getLine(fy2, red, provided, in)
		} else {
			dx := red.XMin - provided.XMin
			fy1 = max(fy1, red.YMin)
			fy2 = min(fy2, red.YMax-1)
			lower = in.Pix[(fy1-provided.YMin)*3*in.W+dx*3 : (fy1-provided.YMin+1)*3*in.W]
			upper = in.Pix[(fy2-provided.YMin)*3*in.W+dx*3 : (fy2-provided.YMin+1)*3*in.W]
		}
		deltas := interp[fy&fracMask]
		for x := 0; x < bufw; x++ {
			for k := 0; k < 3; k++ {
				lv := int(lower[3*x+k])
				iv := int(upper[3*x+k])
				d := deltas[256+iv-lv]
				lbuf[3*(x+1)+k] = byte(lv + int(d))
			}
		}
		// Horizontal interpolation.
		lbuf[0], lbuf[1], lbuf[2] = lbuf[3], lbuf[4], lbuf[5]
		lbuf[3*(bufw+1)], lbuf[3*(bufw+1)+1], lbuf[3*(bufw+1)+2] =
			lbuf[3*bufw], lbuf[3*bufw+1], lbuf[3*bufw+2]
		lineOff := 3 * (1 - red.XMin)
		for x := desired.XMin; x < desired.XMax; x++ {
			n := s.hcoord[x]
			lowerX := lineOff + 3*(n>>fracBits)
			deltas := interp[n&fracMask]
			di := (y-desired.YMin)*out.W*3 + (x-desired.XMin)*3
			for k := 0; k < 3; k++ {
				lv := int(lbuf[lowerX+k])
				iv := int(lbuf[lowerX+3+k])
				d := deltas[256+iv-lv]
				out.Pix[di+k] = byte(lv + int(d))
			}
		}
	}
}

// getLine returns a pre-reduced line of the input, averaging 2^shift
// blocks. This replicates GPixmapScaler::get_line, including the two
// line cache.
func (s *Scaler) getLine(fy int, red, provided Rect, in *Pixmap) []byte {
	if fy < red.YMin {
		fy = red.YMin
	} else if fy >= red.YMax {
		fy = red.YMax - 1
	}
	if fy == s.gl2 {
		return s.gp2
	}
	if fy == s.gl1 {
		return s.gp1
	}
	// Rotate the cache before computing.
	s.gp1, s.gp2 = s.gp2, s.gp1
	s.gl1, s.gl2 = s.gl2, fy
	// Location of the line in the input.
	line := Rect{
		XMin: red.XMin << s.xshift,
		XMax: red.XMax << s.xshift,
		YMin: fy << s.yshift,
		YMax: (fy + 1) << s.yshift,
	}
	if line.XMin < provided.XMin {
		line.XMin = provided.XMin
	}
	if line.YMin < provided.YMin {
		line.YMin = provided.YMin
	}
	if line.XMax > provided.XMax {
		line.XMax = provided.XMax
	}
	if line.YMax > provided.YMax {
		line.YMax = provided.YMax
	}
	line.XMin -= provided.XMin
	line.YMin -= provided.YMin
	line.XMax -= provided.XMin
	line.YMax -= provided.YMin
	sw := 1 << s.xshift
	div := s.xshift + s.yshift
	rnd := 1 << (div - 1)
	botline := line.YMin
	x := line.XMin
	for p := 0; p < red.Width(); p, x = p+1, x+sw {
		var r, g, b, cnt int
		sy1 := min(line.Height(), 1<<s.yshift)
		for sy := 0; sy < sy1; sy++ {
			rowOff := (botline + sy) * in.W * 3
			end := min(x+sw, line.XMax)
			for xx := x; xx < end; xx++ {
				i := rowOff + xx*3
				r += int(in.Pix[i])
				g += int(in.Pix[i+1])
				b += int(in.Pix[i+2])
				cnt++
			}
		}
		if cnt == rnd+rnd {
			s.gp2[3*p] = byte((r + rnd) >> div)
			s.gp2[3*p+1] = byte((g + rnd) >> div)
			s.gp2[3*p+2] = byte((b + rnd) >> div)
		} else {
			s.gp2[3*p] = byte((r + cnt/2) / cnt)
			s.gp2[3*p+1] = byte((g + cnt/2) / cnt)
			s.gp2[3*p+2] = byte((b + cnt/2) / cnt)
		}
	}
	return s.gp2
}
