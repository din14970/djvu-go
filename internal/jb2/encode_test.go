package jb2

// This file contains a test-only JB2 encoder mirroring the decoder, so
// that round-trip tests can generate streams without external tools. It
// follows the encoding path of JB2Image.cpp. It is never compiled into
// the library.

import (
	"bytes"
	"testing"

	"github.com/din14970/djvu-go/internal/zptest"
)

// encCodec is the encoder twin of codec.
type encCodec struct {
	zp *zptest.Encoder

	bitcells  []uint8
	leftcell  []uint32
	rightcell []uint32
	curNCell  uint32

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

	lib2shape []int
	libinfo   []libRect

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

	bitdist  [1024]uint8
	cbitdist [2048]uint8
}

func newEncCodec(buf *bytes.Buffer) *encCodec {
	return &encCodec{
		zp:        zptest.NewEncoder(buf),
		bitcells:  make([]uint8, cellChunk+cellExtra),
		leftcell:  make([]uint32, cellChunk+cellExtra),
		rightcell: make([]uint32, cellChunk+cellExtra),
		curNCell:  1,
	}
}

// codeNum encodes v in [low, high] with the number coder.
func (e *encCodec) codeNum(low, high, v int, pctx *uint32) {
	negative := false
	cutoff := 0
	phase := 1
	rng := uint32(0xffffffff)
	for rng != 1 {
		if *pctx == 0 {
			*pctx = e.curNCell
			e.curNCell++
			e.bitcells[*pctx] = 0
			e.leftcell[*pctx] = 0
			e.rightcell[*pctx] = 0
		}
		decision := false
		if low < cutoff && high >= cutoff {
			b := 0
			if v >= cutoff {
				b = 1
			}
			e.zp.Encode(b, &e.bitcells[*pctx])
			decision = v >= cutoff
		} else {
			decision = v >= cutoff
		}
		if decision {
			pctx = &e.rightcell[*pctx]
		} else {
			pctx = &e.leftcell[*pctx]
		}
		switch phase {
		case 1:
			negative = v < 0
			if negative {
				v = -v - 1
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
	if negative && v != cutoff {
		panic("encoder inconsistency: phase1 sign path")
	}
}

func (e *encCodec) fillShortList(v int) {
	e.shortList[0] = v
	e.shortList[1] = v
	e.shortList[2] = v
	e.shortListPos = 0
}

func (e *encCodec) updateShortList(v int) int {
	e.shortListPos++
	if e.shortListPos == 3 {
		e.shortListPos = 0
	}
	e.shortList[e.shortListPos] = v
	s := &e.shortList
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

// codeRelativeLocation encodes the position of a blit.
func (e *encCodec) codeRelativeLocation(b *Blit, rows, columns int) {
	left := b.Left + 1
	bottom := b.Bottom + 1
	top := bottom + rows - 1
	right := left + columns - 1
	newRow := left < e.lastLeft
	if newRow {
		e.zp.Encode(b2i(newRow), &e.offsetTypeDist)
		e.codeNum(bigNegative, bigPositive, left-e.lastRowLeft, &e.relLocXLast)
		e.codeNum(bigNegative, bigPositive, top-e.lastRowBottom, &e.relLocYLast)
		e.lastLeft = left
		e.lastRowLeft = left
		e.lastRight = right
		e.lastBottom = bottom
		e.lastRowBottom = bottom
		e.fillShortList(bottom)
	} else {
		e.zp.Encode(b2i(newRow), &e.offsetTypeDist)
		e.codeNum(bigNegative, bigPositive, left-e.lastRight, &e.relLocXCurrent)
		e.codeNum(bigNegative, bigPositive, bottom-e.lastBottom, &e.relLocYCurrent)
		e.lastLeft = left
		e.lastRight = right
		e.lastBottom = e.updateShortList(bottom)
	}
}

// codeAbsoluteLocation encodes an absolute blit position.
func (e *encCodec) codeAbsoluteLocation(b *Blit, rows, _ int) {
	top := b.Bottom + rows // bottom + rows = top + 1 in the coded domain
	e.codeNum(1, e.imageColumns, b.Left+1, &e.absLocX)
	e.codeNum(1, e.imageRows, top, &e.absLocY)
}

// codeBitmapDirectly encodes a shape bitmap.
func (e *encCodec) codeBitmapDirectly(bm *grayMap) {
	bm.ensureBorder(3)
	dw := bm.w
	b := bm.border
	for dy := bm.h - 1; dy >= 0; dy-- {
		up2 := bm.row(dy + 2)
		up1 := bm.row(dy + 1)
		up0 := bm.rowWrite(dy)
		context := directContext(up2, up1, up0, b, 0)
		for dx := 0; dx < dw; dx++ {
			n := int(up0[b+dx])
			e.zp.Encode(n, &e.bitdist[context])
			context = directContext(up2, up1, up0, b, dx+1)
		}
	}
}

// codeBitmapByCrossCoding encodes a shape bitmap against a library shape.
func (e *encCodec) codeBitmapByCrossCoding(bm, cbm *grayMap, libno int) {
	dw, dh := bm.w, bm.h
	cw := cbm.w
	l := e.libinfo[libno]
	xd2c := (dw/2 - dw + 1) - (l.width()/2 - l.right)
	yd2c := (dh/2 - dh + 1) - (l.height()/2 - l.top)
	bm.ensureBorder(2)
	cbm.ensureBorder(2 - xd2c)
	cbm.ensureBorder(2 + dw + xd2c - cw)
	bo := bm.border
	co := cbm.border + xd2c
	for dy, cy := dh-1, dh-1+yd2c; dy >= 0; dy, cy = dy-1, cy-1 {
		up1 := bm.row(dy + 1)
		up0 := bm.rowWrite(dy)
		xup1 := cbm.row(cy + 1)
		xup0 := cbm.row(cy)
		xdn1 := cbm.row(cy - 1)
		context := crossContext(up1, up0, xup1, xup0, xdn1, bo, co, 0)
		for dx := 0; dx < dw; dx++ {
			n := int(up0[bo+dx])
			e.zp.Encode(n, &e.cbitdist[context])
			context = crossContext(up1, up0, xup1, xup0, xdn1, bo, co, dx+1)
		}
	}
}

// addLibrary mirrors the decoder's library bookkeeping.
func (e *encCodec) addLibrary(shapeno int, shape *Shape) {
	e.lib2shape = append(e.lib2shape, shapeno)
	e.libinfo = append(e.libinfo, shape.Bits.boundingBox())
}

func (e *encCodec) codeMatchIndex(match int) {
	e.codeNum(0, len(e.lib2shape)-1, match, &e.distMatchIndex)
}

// encodeDict encodes a dictionary: shapes only.
func (e *encCodec) encodeDict(d *Dict) {
	// START_OF_DATA with size (0,0)
	e.codeNum(recStartOfData, recEndOfData, recStartOfData, &e.distRecordType)
	e.codeNum(0, bigPositive, 0, &e.imageSizeDist)
	e.codeNum(0, bigPositive, 0, &e.imageSizeDist)
	e.zp.Encode(0, &e.refinementFlag)
	e.initLibraryEnc(d)
	for i := range d.shapes {
		shape := d.shapes[i]
		if shape.Parent < 0 {
			e.codeNum(recStartOfData, recEndOfData, recNewMarkLibraryOnly, &e.distRecordType)
			e.codeNum(0, bigPositive, shape.Bits.w, &e.absSizeX)
			e.codeNum(0, bigPositive, shape.Bits.h, &e.absSizeY)
			e.codeBitmapDirectly(shape.Bits)
		} else {
			e.codeNum(recStartOfData, recEndOfData, recMatchedRefineLibraryOnly, &e.distRecordType)
			// shape.Parent is a shape number; every dictionary shape
			// enters the library in order, so the library index of an
			// own shape is its number minus the inherited count.
			match := shape.Parent - d.inheritedShapes
			e.codeMatchIndex(match)
			l := e.libinfo[match]
			e.codeNum(bigNegative, bigPositive, shape.Bits.w-l.width(), &e.relSizeX)
			e.codeNum(bigNegative, bigPositive, shape.Bits.h-l.height(), &e.relSizeY)
			e.codeBitmapByCrossCoding(shape.Bits, d.shapes[e.lib2shape[match]].Bits, match)
		}
		e.addLibrary(d.inheritedShapes+i, shape)
	}
	e.codeNum(recStartOfData, recEndOfData, recEndOfData, &e.distRecordType)
}

func (e *encCodec) initLibraryEnc(d *Dict) {
	n := d.inheritedShapes
	e.lib2shape = make([]int, n)
	e.libinfo = make([]libRect, n)
	for i := 0; i < n; i++ {
		e.lib2shape[i] = i
		box, err := d.boundingBox(i)
		if err != nil {
			panic(err)
		}
		e.libinfo[i] = box
	}
}

// encBlit describes a blit for the test encoder.
type encBlit struct {
	blit   Blit
	shape  *Shape
	newRec int // record type for new shapes, or 0 for matched copy
	match  int // library index for matched records
}

// encodeImage encodes an image with an optional required dictionary.
func (e *encCodec) encodeImage(im *Image, inherited *Dict, records []encBlit) {
	if inherited != nil {
		// REQUIRED_DICT record before START_OF_DATA.
		e.codeNum(recStartOfData, recEndOfData, recRequiredDictOrReset, &e.distRecordType)
		e.codeNum(0, bigPositive, inherited.ShapeCount(), &e.inheritedShapeCountDist)
	}
	// START_OF_DATA
	e.codeNum(recStartOfData, recEndOfData, recStartOfData, &e.distRecordType)
	e.codeNum(0, bigPositive, im.Width, &e.imageSizeDist)
	e.codeNum(0, bigPositive, im.Height, &e.imageSizeDist)
	e.imageColumns = im.Width
	e.imageRows = im.Height
	e.lastLeft = 1 + im.Width
	e.lastRowBottom = im.Height
	e.gotStart = true
	e.zp.Encode(0, &e.refinementFlag)
	if inherited != nil {
		if err := im.setInherited(inherited); err != nil {
			panic(err)
		}
	}
	e.initLibraryEnc(&im.Dict)
	for _, r := range records {
		switch r.newRec {
		case recNewMark, recNewMarkImageOnly, recNonMarkData:
			e.codeNum(recStartOfData, recEndOfData, r.newRec, &e.distRecordType)
			e.codeNum(0, bigPositive, r.shape.Bits.w, &e.absSizeX)
			e.codeNum(0, bigPositive, r.shape.Bits.h, &e.absSizeY)
			e.codeBitmapDirectly(r.shape.Bits)
			if r.newRec == recNonMarkData {
				e.codeAbsoluteLocation(&r.blit, r.shape.Bits.h, r.shape.Bits.w)
			} else {
				e.codeRelativeLocation(&r.blit, r.shape.Bits.h, r.shape.Bits.w)
			}
			if r.newRec == recNewMark {
				e.addLibrary(im.ShapeCount(), r.shape)
			}
			im.addShape(r.shape)
			r.blit.ShapeNo = im.ShapeCount() - 1
		case recMatchedRefine, recMatchedRefineImageOnly:
			e.codeNum(recStartOfData, recEndOfData, r.newRec, &e.distRecordType)
			e.codeMatchIndex(r.match)
			l := e.libinfo[r.match]
			e.codeNum(bigNegative, bigPositive, r.shape.Bits.w-l.width(), &e.relSizeX)
			e.codeNum(bigNegative, bigPositive, r.shape.Bits.h-l.height(), &e.relSizeY)
			parent := e.lib2shape[r.match]
			cbm, err := im.Shape(parent)
			if err != nil {
				panic(err)
			}
			e.codeBitmapByCrossCoding(r.shape.Bits, cbm.Bits, r.match)
			e.codeRelativeLocation(&r.blit, r.shape.Bits.h, r.shape.Bits.w)
			if r.newRec == recMatchedRefine {
				e.addLibrary(im.ShapeCount(), r.shape)
			}
			im.addShape(r.shape)
			r.blit.ShapeNo = im.ShapeCount() - 1
		default: // matched copy
			e.codeNum(recStartOfData, recEndOfData, recMatchedCopy, &e.distRecordType)
			e.codeMatchIndex(r.match)
			l := e.libinfo[r.match]
			parent := e.lib2shape[r.match]
			if _, err := im.Shape(parent); err != nil {
				panic(err)
			}
			// The location is coded for the bounding box rectangle.
			b := r.blit
			b.Left += l.left
			b.Bottom += l.bottom
			e.codeRelativeLocation(&b, l.height(), l.width())
			b.Left -= l.left
			b.Bottom -= l.bottom
			r.blit.ShapeNo = parent
		}
		im.Blits = append(im.Blits, r.blit)
	}
	e.codeNum(recStartOfData, recEndOfData, recEndOfData, &e.distRecordType)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// mustEncodeDict encodes a dictionary into a byte stream.
func mustEncodeDict(t *testing.T, d *Dict) []byte {
	t.Helper()
	var buf bytes.Buffer
	e := newEncCodec(&buf)
	e.encodeDict(d)
	e.zp.Flush()
	return buf.Bytes()
}

// mustEncodeImage encodes an image into a byte stream.
func mustEncodeImage(t *testing.T, im *Image, inherited *Dict, records []encBlit) []byte {
	t.Helper()
	var buf bytes.Buffer
	e := newEncCodec(&buf)
	e.encodeImage(im, inherited, records)
	e.zp.Flush()
	return buf.Bytes()
}
