// Package jb2 implements the decoder for JB2 coded bilevel data: the
// Sjbz chunks of DjVu pages (foreground masks) and the Djbz chunks of
// shared shape dictionaries.
//
// JB2 is a soft pattern matching coder: pages are a list of blits, each
// placing a shape (a small bitmap such as a character) at a position.
// Shapes are coded either directly with a 10-pixel context arithmetic
// coder, or by "cross coding" against a similar library shape with an
// 11-pixel context. The number coder (adaptive binary tree over the ZP
// arithmetic coder) codes record types, sizes, positions and library
// indices.
//
// The implementation is a port of the decoding half of JB2Image.cpp
// from the DjVu reference library. Deviations from the reference are
// marked with comments: all structural errors raise Go errors instead
// of the undefined behavior the C++ would exhibit on corrupt data.
package jb2

import (
	"fmt"
	"io"

	"github.com/din14970/djvu-go/internal/bzz"
)

// Numeric range of the number coder.
const (
	bigPositive = 262142
	bigNegative = -262143
)

// JB2 record types.
const (
	recStartOfData              = 0
	recNewMark                  = 1
	recNewMarkLibraryOnly       = 2
	recNewMarkImageOnly         = 3
	recMatchedRefine            = 4
	recMatchedRefineLibraryOnly = 5
	recMatchedRefineImageOnly   = 6
	recMatchedCopy              = 7
	recNonMarkData              = 8
	recRequiredDictOrReset      = 9
	recPreservedComment         = 10
	recEndOfData                = 11
)

// libRect is the bounding box of a library shape
// (JB2Dict::LibRect::compute_bounding_box).
type libRect struct {
	top, left, right, bottom int
}

func (l libRect) width() int  { return l.right - l.left + 1 }
func (l libRect) height() int { return l.top - l.bottom + 1 }

// Shape is one decoded shape: a small bitmap plus the parent shape it
// was refined from (or -1; -2 marks NON_MARK_DATA shapes).
type Shape struct {
	// Parent is the shape number of the parent shape, or a negative
	// value when the shape has no parent.
	Parent int
	// Bits is the shape bitmap. Rows are stored bottom to top.
	Bits *grayMap
}

// Blit places a shape on the page: the coordinates of the bottom left
// corner of the shape bitmap, relative to the bottom left corner of the
// page.
type Blit struct {
	Left    int
	Bottom  int
	ShapeNo int
}

// Dict is a JB2 shape dictionary (Djbz chunk): a list of shapes that
// images can inherit.
type Dict struct {
	// Comment is the comment record of the dictionary, if any.
	Comment string

	inherited       *Dict
	inheritedShapes int
	shapes          []*Shape
	boxes           []libRect
}

// Image is a decoded JB2 image (Sjbz chunk): a shape dictionary plus a
// page size and a list of blits.
type Image struct {
	Dict
	// Width and Height are the page dimensions in pixels.
	Width, Height int
	// Blits are the decoded blits, in decoding order.
	Blits []Blit
	// ReproduceOldBug reproduces the location coding of format version
	// 18 and older, where MATCHED_COPY blits are positioned relative to
	// the full shape bitmap instead of its bounding box. Set it when the
	// enclosing INFO chunk reports version 18 or less.
	ReproduceOldBug bool
}

// ShapeCount returns the total number of shapes, including inherited
// shapes.
func (d *Dict) ShapeCount() int { return d.inheritedShapes + len(d.shapes) }

// Shape returns shape i, which must be in [0, ShapeCount).
func (d *Dict) Shape(i int) (*Shape, error) {
	if i >= d.inheritedShapes {
		idx := i - d.inheritedShapes
		if idx >= len(d.shapes) {
			return nil, fmt.Errorf("jb2: shape %d out of range (%d shapes)", i, d.ShapeCount())
		}
		return d.shapes[idx], nil
	}
	if d.inherited == nil {
		return nil, fmt.Errorf("jb2: inherited shape %d without dictionary", i)
	}
	return d.inherited.Shape(i)
}

// boundingBox returns the bounding box of shape i.
func (d *Dict) boundingBox(i int) (libRect, error) {
	if i < d.inheritedShapes {
		if d.inherited == nil {
			return libRect{}, fmt.Errorf("jb2: inherited shape %d without dictionary", i)
		}
		return d.inherited.boundingBox(i)
	}
	idx := i - d.inheritedShapes
	if idx >= len(d.shapes) {
		return libRect{}, fmt.Errorf("jb2: shape %d out of range (%d shapes)", i, d.ShapeCount())
	}
	if idx < len(d.boxes) {
		return d.boxes[idx], nil
	}
	// No cached box (the dictionary was built by hand rather than
	// decoded): compute it.
	return d.shapes[idx].Bits.boundingBox(), nil
}

// cellChunk is the growth increment of the number coder cell arrays.
const (
	cellChunk = 20000
	cellExtra = 500
)

// codec holds the state of one JB2 decode.
type codec struct {
	zp *bzz.ZPDecoder
	// Number coder cells: a binary tree of ZP contexts. Index 0 is the
	// null cell; a NumContext of 0 means "allocate on first use".
	bitcells  []uint8
	leftcell  []uint32
	rightcell []uint32
	curNCell  uint32
	// Distribution contexts (all number coder tree roots).
	distCommentByte, distCommentLength uint32
	distRecordType, distMatchIndex     uint32
	absLocX, absLocY                   uint32
	absSizeX, absSizeY                 uint32
	imageSizeDist                      uint32
	inheritedShapeCountDist            uint32
	relLocXCurrent, relLocXLast        uint32
	relLocYCurrent, relLocYLast        uint32
	relSizeX, relSizeY                 uint32
	offsetTypeDist                     uint8
	refinementFlag                     uint8
	// Library mapping.
	lib2shape []int
	libinfo   []libRect
	// Location coding state.
	gotStart      bool
	lastBottom    int
	lastLeft      int
	lastRight     int
	lastRowBottom int
	lastRowLeft   int
	imageColumns  int
	imageRows     int
	shortList     [3]int
	shortListPos  int
	// Bitmap coding contexts.
	bitdist  [1024]uint8
	cbitdist [2048]uint8
	// Dictionary callback for REQUIRED_DICT records.
	dictCallback func() *Dict
}

func newCodec(r io.Reader) *codec {
	c := &codec{
		zp:        bzz.NewZPDecoder(r),
		bitcells:  make([]uint8, cellChunk+cellExtra),
		leftcell:  make([]uint32, cellChunk+cellExtra),
		rightcell: make([]uint32, cellChunk+cellExtra),
		curNCell:  1,
	}
	return c
}

// resetNumCoder clears the number coder contexts
// (JB2Dict::JB2Codec::reset_numcoder).
func (c *codec) resetNumCoder() {
	c.distCommentByte = 0
	c.distCommentLength = 0
	c.distRecordType = 0
	c.distMatchIndex = 0
	c.absLocX = 0
	c.absLocY = 0
	c.absSizeX = 0
	c.absSizeY = 0
	c.imageSizeDist = 0
	c.inheritedShapeCountDist = 0
	c.relLocXCurrent = 0
	c.relLocXLast = 0
	c.relLocYCurrent = 0
	c.relLocYLast = 0
	c.relSizeX = 0
	c.relSizeY = 0
	c.bitcells = c.bitcells[:0]
	c.leftcell = c.leftcell[:0]
	c.rightcell = c.rightcell[:0]
	c.curNCell = 1
}

// codeNum decodes a number in [low, high] with the number coder
// (JB2Dict::JB2Codec::CodeNum).
func (c *codec) codeNum(low, high int, pctx *uint32) (int, error) {
	if low > high {
		return 0, fmt.Errorf("jb2: empty number range [%d,%d]", low, high)
	}
	negative := false
	cutoff := 0
	phase := 1
	// The reference uses a signed int initialized to 0xffffffff (-1);
	// the loop runs until the range collapses to 1.
	rng := uint32(0xffffffff)
	for rng != 1 {
		if *pctx == 0 {
			if int(c.curNCell) >= len(c.bitcells) {
				n := len(c.bitcells) + cellChunk
				nb := make([]uint8, n)
				nl := make([]uint32, n)
				nr := make([]uint32, n)
				copy(nb, c.bitcells)
				copy(nl, c.leftcell)
				copy(nr, c.rightcell)
				c.bitcells, c.leftcell, c.rightcell = nb, nl, nr
			}
			*pctx = c.curNCell
			c.curNCell++
			c.bitcells[*pctx] = 0
			c.leftcell[*pctx] = 0
			c.rightcell[*pctx] = 0
		}
		decision := low >= cutoff
		if !decision && high >= cutoff {
			decision = c.zp.Decode(&c.bitcells[*pctx]) != 0
		}
		if decision {
			pctx = &c.rightcell[*pctx]
		} else {
			pctx = &c.leftcell[*pctx]
		}
		switch phase {
		case 1:
			negative = !decision
			if negative {
				temp := -low - 1
				low = -high - 1
				high = temp
			}
			phase = 2
			cutoff = 1
		case 2:
			if !decision {
				phase = 3
				rng = uint32((cutoff + 1) / 2)
				if rng == 1 {
					cutoff = 0
				} else {
					cutoff -= int(rng) / 2
				}
			} else {
				cutoff += cutoff + 1
			}
		case 3:
			rng /= 2
			if rng != 1 {
				if !decision {
					cutoff -= int(rng) / 2
				} else {
					cutoff += int(rng) / 2
				}
			} else if !decision {
				cutoff--
			}
		}
	}
	if negative {
		return -cutoff - 1, nil
	}
	return cutoff, nil
}

// codeBit decodes a single context coded bit.
func (c *codec) codeBit(ctx *uint8) int {
	return c.zp.Decode(ctx)
}

// codeComment decodes a PRESERVED_COMMENT record.
func (c *codec) codeComment() (string, error) {
	size, err := c.codeNum(0, bigPositive, &c.distCommentLength)
	if err != nil {
		return "", err
	}
	if size < 0 || size > 1<<20 {
		return "", fmt.Errorf("jb2: comment length %d out of range", size)
	}
	buf := make([]byte, size)
	for i := range buf {
		b, err := c.codeNum(0, 255, &c.distCommentByte)
		if err != nil {
			return "", err
		}
		buf[i] = byte(b)
	}
	return string(buf), nil
}

// initLibrary initializes the library mapping from the inherited shapes.
func (c *codec) initLibrary(d *Dict) error {
	n := d.inheritedShapes
	c.lib2shape = make([]int, n)
	c.libinfo = make([]libRect, n)
	for i := 0; i < n; i++ {
		c.lib2shape[i] = i
		box, err := d.boundingBox(i)
		if err != nil {
			return err
		}
		c.libinfo[i] = box
	}
	return nil
}

// addLibrary adds a newly decoded shape to the library.
func (c *codec) addLibrary(shapeno int, shape *Shape) {
	c.lib2shape = append(c.lib2shape, shapeno)
	c.libinfo = append(c.libinfo, shape.Bits.boundingBox())
}

// codeMatchIndex decodes a library index.
func (c *codec) codeMatchIndex() (match, shapeno int, err error) {
	m, err := c.codeNum(0, len(c.lib2shape)-1, &c.distMatchIndex)
	if err != nil {
		return 0, 0, err
	}
	if m < 0 || m >= len(c.lib2shape) {
		return 0, 0, fmt.Errorf("jb2: library index %d out of range (%d entries)", m, len(c.lib2shape))
	}
	return m, c.lib2shape[m], nil
}

// codeInheritedShapeCount handles REQUIRED_DICT records that appear
// before the start record.
func (c *codec) codeInheritedShapeCount(d *Dict, dict *Dict) error {
	size, err := c.codeNum(0, bigPositive, &c.inheritedShapeCountDist)
	if err != nil {
		return err
	}
	if size > 0 && dict == nil && c.dictCallback != nil {
		dict = c.dictCallback()
		if dict != nil {
			if err := d.setInherited(dict); err != nil {
				return err
			}
		}
	}
	if size > 0 && dict == nil {
		return fmt.Errorf("jb2: data requires a shape dictionary of %d shapes, none available", size)
	}
	if dict != nil && size != dict.ShapeCount() {
		return fmt.Errorf("jb2: data requires a shape dictionary of %d shapes, got %d", size, dict.ShapeCount())
	}
	return nil
}

// setInherited attaches an inherited dictionary.
func (d *Dict) setInherited(dict *Dict) error {
	if len(d.shapes) > 0 || d.inherited != nil {
		return fmt.Errorf("jb2: inherited dictionary set after decoding started")
	}
	d.inherited = dict
	d.inheritedShapes = dict.ShapeCount()
	return nil
}

// fillShortList and updateShortList implement the "short list" median
// used for the current-row position prediction.
func (c *codec) fillShortList(v int) {
	c.shortList[0] = v
	c.shortList[1] = v
	c.shortList[2] = v
	c.shortListPos = 0
}

func (c *codec) updateShortList(v int) int {
	c.shortListPos++
	if c.shortListPos == 3 {
		c.shortListPos = 0
	}
	c.shortList[c.shortListPos] = v
	s := &c.shortList
	// Median of three.
	if s[0] >= s[1] {
		if s[0] > s[2] {
			if s[1] >= s[2] {
				return s[1]
			}
			return s[2]
		}
		return s[0]
	}
	if s[0] < s[2] {
		if s[1] >= s[2] {
			return s[2]
		}
		return s[1]
	}
	return s[0]
}

// codeRelativeLocation decodes the position of a blit relative to the
// previously decoded blits (JB2Dict::JB2Codec::code_relative_location).
func (c *codec) codeRelativeLocation(b *Blit, rows, columns int) error {
	if !c.gotStart {
		return fmt.Errorf("jb2: location record before start of data")
	}
	var left, bottom, top, right int
	newRow := c.codeBit(&c.offsetTypeDist) != 0
	if newRow {
		// Begin a new row.
		xd, err := c.codeNum(bigNegative, bigPositive, &c.relLocXLast)
		if err != nil {
			return err
		}
		yd, err := c.codeNum(bigNegative, bigPositive, &c.relLocYLast)
		if err != nil {
			return err
		}
		left = c.lastRowLeft + xd
		top = c.lastRowBottom + yd
		right = left + columns - 1
		bottom = top - rows + 1
		c.lastLeft = left
		c.lastRowLeft = left
		c.lastRight = right
		c.lastBottom = bottom
		c.lastRowBottom = bottom
		c.fillShortList(bottom)
	} else {
		// Same row.
		xd, err := c.codeNum(bigNegative, bigPositive, &c.relLocXCurrent)
		if err != nil {
			return err
		}
		yd, err := c.codeNum(bigNegative, bigPositive, &c.relLocYCurrent)
		if err != nil {
			return err
		}
		left = c.lastRight + xd
		bottom = c.lastBottom + yd
		right = left + columns - 1
		c.lastLeft = left
		c.lastRight = right
		c.lastBottom = c.updateShortList(bottom)
	}
	b.Left = left - 1
	b.Bottom = bottom - 1
	return nil
}

// codeAbsoluteLocation decodes an absolute blit position (NON_MARK_DATA).
func (c *codec) codeAbsoluteLocation(b *Blit, rows, _ int) error {
	if !c.gotStart {
		return fmt.Errorf("jb2: location record before start of data")
	}
	left, err := c.codeNum(1, c.imageColumns, &c.absLocX)
	if err != nil {
		return err
	}
	top, err := c.codeNum(1, c.imageRows, &c.absLocY)
	if err != nil {
		return err
	}
	b.Bottom = top - rows
	b.Left = left - 1
	return nil
}

// codeAbsoluteMarkSize decodes the size of a directly coded shape.
func (c *codec) codeAbsoluteMarkSize(_ int) (w, h int, err error) {
	w, err = c.codeNum(0, bigPositive, &c.absSizeX)
	if err != nil {
		return 0, 0, err
	}
	h, err = c.codeNum(0, bigPositive, &c.absSizeY)
	if err != nil {
		return 0, 0, err
	}
	if w < 0 || h < 0 || w > 65535 || h > 65535 {
		return 0, 0, fmt.Errorf("jb2: shape size %dx%d out of range", w, h)
	}
	return w, h, nil
}

// codeRelativeMarkSize decodes the size of a cross coded shape.
func (c *codec) codeRelativeMarkSize(cw, ch, _ int) (w, h int, err error) {
	xd, err := c.codeNum(bigNegative, bigPositive, &c.relSizeX)
	if err != nil {
		return 0, 0, err
	}
	yd, err := c.codeNum(bigNegative, bigPositive, &c.relSizeY)
	if err != nil {
		return 0, 0, err
	}
	w = cw + xd
	h = ch + yd
	if w < 0 || h < 0 || w > 65535 || h > 65535 {
		return 0, 0, fmt.Errorf("jb2: shape size %dx%d out of range", w, h)
	}
	return w, h, nil
}

// codeBitmapDirectly decodes a shape bitmap with the 10 pixel context
// coder.
func (c *codec) codeBitmapDirectly(bm *grayMap) error {
	bm.ensureBorder(3)
	dw := bm.w
	b := bm.border
	for dy := bm.h - 1; dy >= 0; dy-- {
		if c.zp.Err() != nil {
			return fmt.Errorf("jb2: unexpected end of shape data: %w", c.zp.Err())
		}
		up2 := bm.row(dy + 2)
		up1 := bm.row(dy + 1)
		up0 := bm.rowWrite(dy)
		context := directContext(up2, up1, up0, b, 0)
		for dx := 0; dx < dw; dx++ {
			n := c.zp.Decode(&c.bitdist[context])
			up0[b+dx] = byte(n)
			context = directContext(up2, up1, up0, b, dx+1)
		}
	}
	return nil
}

// directContext computes the 10 bit context for direct bitmap coding.
// The reference uses a cheaper incremental shift; recomputing the full
// context is equivalent and verified against the shift masks 0x37a.
func directContext(up2, up1, up0 []byte, b, col int) int {
	return int(up2[b+col-1])<<9 |
		int(up2[b+col])<<8 |
		int(up2[b+col+1])<<7 |
		int(up1[b+col-2])<<6 |
		int(up1[b+col-1])<<5 |
		int(up1[b+col])<<4 |
		int(up1[b+col+1])<<3 |
		int(up1[b+col+2])<<2 |
		int(up0[b+col-2])<<1 |
		int(up0[b+col-1])
}

// codeBitmapByCrossCoding decodes a shape bitmap relative to a library
// shape with the 11 pixel context coder. libno indexes the library
// mapping (matching the reference call sites: image records pass the
// library index, dict records pass the shape number).
func (c *codec) codeBitmapByCrossCoding(bm, cbm *grayMap, libno int) error {
	if libno < 0 || libno >= len(c.libinfo) {
		return fmt.Errorf("jb2: cross coding references library entry %d out of range (%d entries)", libno, len(c.libinfo))
	}
	dw, dh := bm.w, bm.h
	cw := cbm.w
	l := c.libinfo[libno]
	xd2c := (dw/2 - dw + 1) - (l.width()/2 - l.right)
	yd2c := (dh/2 - dh + 1) - (l.height()/2 - l.top)
	bm.ensureBorder(2)
	// The reference extends the bitmap of the library shape so that all
	// context window accesses stay inside allocated memory.
	cbm.ensureBorder(2 - xd2c)
	cbm.ensureBorder(2 + dw + xd2c - cw)

	bo := bm.border
	co := cbm.border + xd2c
	for dy, cy := dh-1, dh-1+yd2c; dy >= 0; dy, cy = dy-1, cy-1 {
		if c.zp.Err() != nil {
			return fmt.Errorf("jb2: unexpected end of shape data: %w", c.zp.Err())
		}
		up1 := bm.row(dy + 1)
		up0 := bm.rowWrite(dy)
		xup1 := cbm.row(cy + 1)
		xup0 := cbm.row(cy)
		xdn1 := cbm.row(cy - 1)
		context := crossContext(up1, up0, xup1, xup0, xdn1, bo, co, 0)
		for dx := 0; dx < dw; dx++ {
			n := c.zp.Decode(&c.cbitdist[context])
			up0[bo+dx] = byte(n)
			context = crossContext(up1, up0, xup1, xup0, xdn1, bo, co, dx+1)
		}
	}
	return nil
}

// crossContext computes the 11 bit context for cross coding. Recomputing
// the full context is equivalent to the reference's incremental shift
// (mask 0x636).
func crossContext(up1, up0, xup1, xup0, xdn1 []byte, bo, co, col int) int {
	return int(up1[bo+col-1])<<10 |
		int(up1[bo+col])<<9 |
		int(up1[bo+col+1])<<8 |
		int(up0[bo+col-1])<<7 |
		int(xup1[co+col])<<6 |
		int(xup0[co+col-1])<<5 |
		int(xup0[co+col])<<4 |
		int(xup0[co+col+1])<<3 |
		int(xdn1[co+col-1])<<2 |
		int(xdn1[co+col])<<1 |
		int(xdn1[co+col+1])
}

// DecodeDict decodes a JB2 shape dictionary (Djbz chunk). The callback
// supplies an inherited dictionary when the data requires one; it may
// return nil.
func DecodeDict(r io.Reader, cb func() *Dict) (*Dict, error) {
	d := &Dict{}
	c := newCodec(r)
	c.dictCallback = cb
	if err := c.codeDict(d); err != nil {
		return nil, err
	}
	return d, nil
}

// DecodeImage decodes a JB2 image (Sjbz chunk). The callback supplies the
// shared shape dictionary when the data requires one; it may return nil.
func DecodeImage(r io.Reader, cb func() *Dict) (*Image, error) {
	im := &Image{}
	c := newCodec(r)
	c.dictCallback = cb
	if err := c.codeImage(im); err != nil {
		return nil, err
	}
	return im, nil
}

// codeDict decodes the record sequence of a dictionary.
func (c *codec) codeDict(d *Dict) error {
	for {
		if c.zp.Err() != nil {
			return fmt.Errorf("jb2: unexpected end of dictionary data: %w", c.zp.Err())
		}
		rectype, err := c.codeNum(recStartOfData, recEndOfData, &c.distRecordType)
		if err != nil {
			return err
		}
		done, err := c.codeDictRecord(d, rectype)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}
	if !c.gotStart {
		return fmt.Errorf("jb2: dictionary without start of data record")
	}
	// Cache the bounding boxes of the dictionary shapes.
	d.boxes = make([]libRect, len(d.shapes))
	for i := range d.shapes {
		d.boxes[i] = c.libinfo[len(c.libinfo)-len(d.shapes)+i]
	}
	return nil
}

// codeDictRecord decodes one dictionary record. Records other than
// the start of data record and the two header records may only appear
// after the start of data record; earlier ones would be wiped by the
// library reinitialization (the reference behaves the same way, by
// accident of its array resizing), so they are rejected instead.
func (c *codec) codeDictRecord(d *Dict, rectype int) (bool, error) {
	if !c.gotStart &&
		rectype != recStartOfData &&
		rectype != recRequiredDictOrReset &&
		rectype != recPreservedComment &&
		rectype != recEndOfData {
		return false, fmt.Errorf("jb2: record type %d before start of data record", rectype)
	}
	switch rectype {
	case recStartOfData:
		if c.gotStart {
			return false, fmt.Errorf("jb2: repeated start of data record")
		}
		w, err := c.codeNum(0, bigPositive, &c.imageSizeDist)
		if err != nil {
			return false, err
		}
		h, err := c.codeNum(0, bigPositive, &c.imageSizeDist)
		if err != nil {
			return false, err
		}
		if w != 0 || h != 0 {
			return false, fmt.Errorf("jb2: dictionary declares a nonzero image size %dx%d", w, h)
		}
		c.lastLeft, c.lastRowLeft, c.lastRowBottom, c.lastRight = 1, 0, 0, 0
		c.fillShortList(0)
		c.gotStart = true
		// Eventual lossless refinement flag: consumed, but its value is
		// only used by encoders in this format version.
		_ = c.codeBit(&c.refinementFlag)
		if err := c.initLibrary(d); err != nil {
			return false, err
		}
	case recNewMarkLibraryOnly:
		shape := &Shape{Parent: -1}
		w, h, err := c.codeAbsoluteMarkSize(4)
		if err != nil {
			return false, err
		}
		shape.Bits = newGrayMap(w, h, 4)
		if err := c.codeBitmapDirectly(shape.Bits); err != nil {
			return false, err
		}
		d.addShape(shape)
		c.addLibrary(d.ShapeCount()-1, shape)
	case recMatchedRefineLibraryOnly:
		shape := &Shape{Parent: -1}
		match, parent, err := c.codeMatchIndex()
		if err != nil {
			return false, err
		}
		shape.Parent = parent
		cbm, err := d.Shape(parent)
		if err != nil {
			return false, err
		}
		l := c.libinfo[match]
		w, h, err := c.codeRelativeMarkSize(l.width(), l.height(), 4)
		if err != nil {
			return false, err
		}
		shape.Bits = newGrayMap(w, h, 4)
		// The reference passes the shape number here, which coincides
		// with the library index for dictionaries in practice.
		if err := c.codeBitmapByCrossCoding(shape.Bits, cbm.Bits, parent); err != nil {
			return false, err
		}
		d.addShape(shape)
		c.addLibrary(d.ShapeCount()-1, shape)
	case recPreservedComment:
		comment, err := c.codeComment()
		if err != nil {
			return false, err
		}
		d.Comment = comment
	case recRequiredDictOrReset:
		if !c.gotStart {
			if err := c.codeInheritedShapeCount(d, d.inherited); err != nil {
				return false, err
			}
		} else {
			c.resetNumCoder()
		}
	case recEndOfData:
		return true, nil
	default:
		return false, fmt.Errorf("jb2: illegal record type %d in dictionary", rectype)
	}
	return false, nil
}

// addShape appends a shape to the dictionary.
func (d *Dict) addShape(s *Shape) {
	d.shapes = append(d.shapes, s)
}

// codeImage decodes the record sequence of an image.
func (c *codec) codeImage(im *Image) error {
	for {
		if c.zp.Err() != nil {
			return fmt.Errorf("jb2: unexpected end of image data: %w", c.zp.Err())
		}
		rectype, err := c.codeNum(recStartOfData, recEndOfData, &c.distRecordType)
		if err != nil {
			return err
		}
		done, err := c.codeImageRecord(im, rectype)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}
	if !c.gotStart {
		return fmt.Errorf("jb2: image without start of data record")
	}
	return nil
}

// codeImageRecord decodes one image record.
func (c *codec) codeImageRecord(im *Image, rectype int) (bool, error) {
	d := &im.Dict
	var shape *Shape
	var blit Blit
	inLibrary := false
	blitsPage := false

	if !c.gotStart &&
		rectype != recStartOfData &&
		rectype != recRequiredDictOrReset &&
		rectype != recPreservedComment &&
		rectype != recEndOfData {
		return false, fmt.Errorf("jb2: record type %d before start of data record", rectype)
	}
	switch rectype {
	case recStartOfData:
		if c.gotStart {
			return false, fmt.Errorf("jb2: repeated start of data record")
		}
		w, err := c.codeNum(0, bigPositive, &c.imageSizeDist)
		if err != nil {
			return false, err
		}
		h, err := c.codeNum(0, bigPositive, &c.imageSizeDist)
		if err != nil {
			return false, err
		}
		if w == 0 || h == 0 {
			return false, fmt.Errorf("jb2: image has a zero dimension (%dx%d)", w, h)
		}
		im.Width, im.Height = w, h
		c.imageColumns, c.imageRows = w, h
		c.lastLeft = 1 + w
		c.lastRowLeft = 0
		c.lastRowBottom = h
		c.lastRight = 0
		c.fillShortList(h)
		c.gotStart = true
		// Eventual lossless refinement flag: consumed, but its value is
		// only used by encoders in this format version.
		_ = c.codeBit(&c.refinementFlag)
		if err := c.initLibrary(d); err != nil {
			return false, err
		}
	case recNewMark, recNewMarkImageOnly, recNonMarkData:
		shape = &Shape{Parent: -1}
		if rectype == recNonMarkData {
			shape.Parent = -2
		}
		border := 4
		if rectype == recNewMarkImageOnly || rectype == recNonMarkData {
			border = 3
		}
		w, h, err := c.codeAbsoluteMarkSize(border)
		if err != nil {
			return false, err
		}
		shape.Bits = newGrayMap(w, h, border)
		if err := c.codeBitmapDirectly(shape.Bits); err != nil {
			return false, err
		}
		if rectype == recNonMarkData {
			err = c.codeAbsoluteLocation(&blit, h, w)
		} else {
			err = c.codeRelativeLocation(&blit, h, w)
		}
		if err != nil {
			return false, err
		}
		inLibrary = rectype == recNewMark
		blitsPage = true
	case recMatchedRefine, recMatchedRefineImageOnly:
		shape = &Shape{Parent: -1}
		match, parent, err := c.codeMatchIndex()
		if err != nil {
			return false, err
		}
		shape.Parent = parent
		cbm, err := d.Shape(parent)
		if err != nil {
			return false, err
		}
		l := c.libinfo[match]
		w, h, err := c.codeRelativeMarkSize(l.width(), l.height(), 4)
		if err != nil {
			return false, err
		}
		shape.Bits = newGrayMap(w, h, 4)
		if err := c.codeBitmapByCrossCoding(shape.Bits, cbm.Bits, match); err != nil {
			return false, err
		}
		if err := c.codeRelativeLocation(&blit, h, w); err != nil {
			return false, err
		}
		inLibrary = rectype == recMatchedRefine
		blitsPage = true
	case recMatchedCopy:
		match, parent, err := c.codeMatchIndex()
		if err != nil {
			return false, err
		}
		cbm, err := d.Shape(parent)
		if err != nil {
			return false, err
		}
		blit.ShapeNo = parent
		l := c.libinfo[match]
		rows, columns := l.height(), l.width()
		if im.ReproduceOldBug {
			rows, columns = cbm.Bits.h, cbm.Bits.w
		}
		if err := c.codeRelativeLocation(&blit, rows, columns); err != nil {
			return false, err
		}
		// The location was coded for the bounding box rectangle; shift
		// it back to the corner of the shape bitmap.
		blit.Left -= l.left
		blit.Bottom -= l.bottom
		blitsPage = true
	case recPreservedComment:
		comment, err := c.codeComment()
		if err != nil {
			return false, err
		}
		d.Comment = comment
	case recRequiredDictOrReset:
		if !c.gotStart {
			if err := c.codeInheritedShapeCount(d, d.inherited); err != nil {
				return false, err
			}
		} else {
			c.resetNumCoder()
		}
	case recEndOfData:
		return true, nil
	default:
		return false, fmt.Errorf("jb2: unknown record type %d", rectype)
	}

	if shape != nil {
		d.addShape(shape)
		shapeno := d.ShapeCount() - 1
		if inLibrary {
			c.addLibrary(shapeno, shape)
		}
		if blitsPage {
			blit.ShapeNo = shapeno
		}
	}
	if blitsPage {
		im.Blits = append(im.Blits, blit)
	}
	return false, nil
}

// ShapeWidth returns the width of the shape bitmap in pixels, or 0
// when the shape has no bitmap.
func (s *Shape) ShapeWidth() int {
	if s.Bits == nil {
		return 0
	}
	return s.Bits.w
}

// ShapeHeight returns the height of the shape bitmap in pixels, or 0
// when the shape has no bitmap.
func (s *Shape) ShapeHeight() int {
	if s.Bits == nil {
		return 0
	}
	return s.Bits.h
}

// ShapeRow returns row y of the shape bitmap: sw bytes of 0 or 1,
// ordered bottom to top like the blit coordinates. It returns nil
// when the shape has no bitmap or the row is outside the shape; the
// caller must treat missing rows as all zero.
func (s *Shape) ShapeRow(y int) []byte {
	if s.Bits == nil || y < 0 || y >= s.Bits.h {
		return nil
	}
	return s.Bits.row(y)[s.Bits.border : s.Bits.border+s.Bits.w]
}
