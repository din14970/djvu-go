package djvu

import (
	"encoding/binary"
	"fmt"

	"github.com/din14970/djvu-go/internal/bzz"
)

// Bookmark is one entry of the document outline (the navigation
// bookmarks stored in the NAVM chunk of multipage documents).
type Bookmark struct {
	// Title is the display name of the bookmark.
	Title string
	// URL is the link target, usually a fragment reference such as
	// "#p3.djvu".
	URL string
	// Children are the nested bookmarks.
	Children []Bookmark
}

// Outline returns the outline (bookmarks) of a multipage document, or
// nil when the document has no outline. The outline is stored in the
// NAVM chunk of the FORM:DJVM, which is part of the index file of
// indirect documents, so it is available for bundled and indirect
// documents alike. Single-page documents never have an outline.
func (d *Document) Outline() ([]Bookmark, error) {
	if d.navm == nil {
		return nil, nil
	}
	data, err := bzz.ReadAll(d.navm.Payload())
	if err != nil {
		return nil, fmt.Errorf("djvu: decoding NAVM: %w", err)
	}
	if len(data) < 2 {
		return nil, fmt.Errorf("djvu: NAVM chunk of %d bytes is too short", len(data))
	}
	count := int(binary.BigEndian.Uint16(data[0:2]))
	pos := 2
	items := make([]navBookmark, 0, count)
	for i := 0; i < count; i++ {
		if pos+4 > len(data) {
			return nil, fmt.Errorf("djvu: NAVM entry %d is truncated", i)
		}
		var it navBookmark
		// The child count is stored little endian (low byte first),
		// unlike every other NAVM field. See DjVmNav.cpp.
		it.Children = int(data[pos]) | int(data[pos+1])<<8
		nameLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		pos += 4
		if pos+nameLen > len(data) {
			return nil, fmt.Errorf("djvu: NAVM entry %d has a truncated title", i)
		}
		it.Title = string(data[pos : pos+nameLen])
		pos += nameLen
		if pos+3 > len(data) {
			return nil, fmt.Errorf("djvu: NAVM entry %d is truncated", i)
		}
		urlLen := int(data[pos])<<16 | int(data[pos+1])<<8 | int(data[pos+2])
		pos += 3
		if pos+urlLen > len(data) {
			return nil, fmt.Errorf("djvu: NAVM entry %d has a truncated URL", i)
		}
		it.URL = string(data[pos : pos+urlLen])
		pos += urlLen
		items = append(items, it)
	}
	// The flat entry list is a pre-order walk of the bookmark forest;
	// the child counts restore the nesting.
	var roots []Bookmark
	i := 0
	for i < len(items) {
		b, err := buildBookmark(items, &i)
		if err != nil {
			return nil, err
		}
		roots = append(roots, b)
	}
	return roots, nil
}

// navBookmark is one flat entry of the NAVM chunk.
type navBookmark struct {
	Title    string
	URL      string
	Children int
}

// buildBookmark consumes one bookmark and its subtree from items.
func buildBookmark(items []navBookmark, pos *int) (Bookmark, error) {
	it := items[*pos]
	*pos++
	b := Bookmark{Title: it.Title, URL: it.URL}
	for i := 0; i < it.Children; i++ {
		if *pos >= len(items) {
			return b, fmt.Errorf("djvu: NAVM bookmark %q declares %d children but the entry list is exhausted", it.Title, it.Children)
		}
		child, err := buildBookmark(items, pos)
		if err != nil {
			return b, err
		}
		b.Children = append(b.Children, child)
	}
	return b, nil
}
