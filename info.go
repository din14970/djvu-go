package djvu

import (
	"encoding/binary"
	"fmt"
)

// Info holds the fields of a DjVu INFO chunk: the geometry and display
// parameters of a page. The zero value has Width, Height and DPI set to
// zero; use Open and Pages to obtain populated values.
//
// The INFO chunk layout:
//
//	INT16  width       (big endian)
//	INT16  height      (big endian)
//	INT16  version     (little endian: low byte first)
//	INT16  dpi         (little endian: low byte first)
//	BYTE   gamma encoded as gamma*10
//	BYTE   orientation flags
//
// Following DjVuInfo::decode of the reference implementation, missing
// fields fall back to defaults (dpi 300, gamma 2.2), the gamma value is
// clamped to [0.3, 5.0], and out-of-range dpi values fall back to 300.
type Info struct {
	// Width and Height are the page dimensions in pixels.
	Width  int
	Height int
	// Version is the DjVu format version of the encoder.
	Version int
	// DPI is the resolution in dots per inch.
	DPI int
	// Gamma is the gamma coefficient of the display the image was
	// designed for.
	Gamma float64
	// Orientation is the page rotation: 0 none, 1 ninety degrees
	// counter-clockwise, 2 one hundred eighty degrees, 3 two hundred
	// seventy degrees counter-clockwise.
	Orientation int
}

// parseInfo decodes the payload of an INFO chunk.
func parseInfo(b []byte) (Info, error) {
	info := Info{DPI: 300, Gamma: 2.2}
	size := len(b)
	if size == 0 {
		return info, fmt.Errorf("djvu: empty INFO chunk")
	}
	if size < 5 {
		return info, fmt.Errorf("djvu: INFO chunk of %d bytes is too short", size)
	}
	info.Width = int(binary.BigEndian.Uint16(b[0:2]))
	info.Height = int(binary.BigEndian.Uint16(b[2:4]))
	info.Version = int(b[4])
	if size >= 6 && b[5] != 0xff {
		info.Version = int(b[4]) | int(b[5])<<8
	}
	if size >= 8 && b[7] != 0xff {
		info.DPI = int(b[6]) | int(b[7])<<8
	}
	if size >= 9 {
		info.Gamma = float64(b[8]) / 10
	}
	// Fixups from the reference implementation.
	if info.Gamma < 0.3 {
		info.Gamma = 0.3
	}
	if info.Gamma > 5.0 {
		info.Gamma = 5.0
	}
	if info.DPI < 25 || info.DPI > 6000 {
		info.DPI = 300
	}
	if size >= 10 {
		switch b[9] & 0x7 {
		case 6:
			info.Orientation = 1
		case 2:
			info.Orientation = 2
		case 5:
			info.Orientation = 3
		default:
			info.Orientation = 0
		}
	}
	return info, nil
}
