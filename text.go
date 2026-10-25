// Go port of djvulibre's libdjvu/DjVuText.cpp.
// Upstream copyright (c) 2002 Leon Bottou and Yann Le Cun; derived
// from the DjVu Reference Library (c) 1999-2001 LizardTech, Inc. This
// derivative work is licensed GPL-2.0-or-later; see the LICENSE file.

package djvu

import (
	"fmt"

	"github.com/din14970/djvu-go/internal/bzz"
)

// PageText returns the hidden text layer of the given page: the UTF-8
// text stored in its TXTa or TXTz chunk. It returns the empty string
// when the page has no hidden text. The text may contain the structural
// separator characters used by the format (vertical tab for columns,
// group separator for regions, unit separator for paragraphs, line feed
// for lines). The positional zone structure that may follow the text is
// not decoded.
//
// For indirect documents it returns an error: the text data is stored
// in external files.
func (d *Document) PageText(page int) (string, error) {
	p, err := d.page(page)
	if err != nil {
		return "", err
	}
	if p.children == nil {
		if d.kind == KindSinglePage {
			return "", nil
		}
		return "", errIndirect
	}
	for _, c := range p.children {
		var data []byte
		switch c.ID {
		case "TXTa":
			data, err = c.ReadAll()
			if err != nil {
				return "", fmt.Errorf("djvu: reading TXTa: %w", err)
			}
		case "TXTz":
			data, err = bzz.ReadAll(c.Payload())
			if err != nil {
				return "", fmt.Errorf("djvu: decoding TXTz: %w", err)
			}
		default:
			continue
		}
		return parseHiddenText(data)
	}
	return "", nil
}

// parseHiddenText decodes the content of a TXTa or TXTz chunk: a 24 bit
// big endian text length, followed by the text and optional zone data.
func parseHiddenText(data []byte) (string, error) {
	if len(data) < 3 {
		return "", fmt.Errorf("djvu: hidden text chunk of %d bytes is too short", len(data))
	}
	textLen := int(data[0])<<16 | int(data[1])<<8 | int(data[2])
	if textLen+3 > len(data) {
		return "", fmt.Errorf("djvu: hidden text declares %d bytes but only %d remain", textLen, len(data)-3)
	}
	return string(data[3 : 3+textLen]), nil
}
