// Package dirm parses the DIRM chunk of DjVu multipage documents
// (FORM:DJVM), following DjVmDir.cpp of the DjVu reference library.
//
// A DIRM chunk consists of unencoded data followed by a BZZ compressed
// part:
//
//	BYTE              flags: bit 7 = bundled (1) or indirect (0),
//	                  bits 0-6 = format version (0 or 1)
//	INT16             number of component files
//	INT32[n]          (bundled only) file offsets within the document
//	INT24[n]          (version 0, bundled only) file sizes
//	--- BZZ compressed from here ---
//	INT24[n]          (version 1 only) file sizes
//	BYTE[n]           flag bytes
//	ZSTR...           zero terminated id/name/title strings
package dirm

import (
	"bufio"
	"bytes"
	"fmt"
	"io"

	"github.com/din14970/djvu-go/internal/bzz"
)

// Version is the DIRM format version produced and understood here.
const Version = 1

// FileType classifies a component file listed in the directory
// (DjVmDir::File::FILE_TYPE).
type FileType int

// Component file types. The numeric values are the low bits of the DIRM
// flag byte.
const (
	// TypeInclude marks a shared component file included by other files
	// (FORM:DJVI).
	TypeInclude FileType = 0
	// TypePage marks a file representing a page of the document.
	TypePage FileType = 1
	// TypeThumbnails marks a file containing page thumbnails (FORM:THUM).
	TypeThumbnails FileType = 2
	// TypeSharedAnno marks the single file holding annotations shared by
	// all pages.
	TypeSharedAnno FileType = 3
)

// String returns the reference library's name for the file type.
func (t FileType) String() string {
	switch t {
	case TypeInclude:
		return "INCLUDE"
	case TypePage:
		return "PAGE"
	case TypeThumbnails:
		return "THUMBNAILS"
	case TypeSharedAnno:
		return "SHARED_ANNO"
	}
	return fmt.Sprintf("FileType(%d)", int(t))
}

// Flag bits of the version 1 flag byte.
const (
	flagHasName  = 0x80
	flagHasTitle = 0x40
	flagTypeMask = 0x3f
)

// Flag bits of the obsolete version 0 flag byte.
const (
	flag0IsPage   = 1
	flag0HasName  = 2
	flag0HasTitle = 4
)

// File describes one component file of a multipage document.
type File struct {
	// ID is the identifier of the component file, referenced by INCL
	// chunks of other component files.
	ID string
	// Name is the file name used when the document is stored as an
	// indirect document. It equals ID unless the directory records a
	// separate name.
	Name string
	// Title is the cosmetic display title. It equals ID unless the
	// directory records a separate title.
	Title string
	// Offset is the byte offset of the component file within the
	// bundled document. It is zero for indirect documents.
	Offset int64
	// Size is the byte size of the component file.
	Size int64
	// Type classifies the file (page, include, thumbnails, shared
	// annotations).
	Type FileType
	// Flags is the raw flag byte from the directory.
	Flags byte
	// PageNum is the zero based page number for files of type TypePage,
	// or -1 for other files.
	PageNum int
}

// IsPage reports whether the file represents a page of the document.
func (f *File) IsPage() bool { return f.Type == TypePage }

// Directory is the parsed document directory.
type Directory struct {
	// Version is the DIRM format version (0 or 1).
	Version int
	// Bundled reports whether the document is bundled (all component
	// files stored in the same file) or indirect (component files stored
	// externally, addressed by Name).
	Bundled bool
	// Files lists the component files in directory order.
	Files []*File
}

// Pages returns the files representing pages, ordered by page number.
func (d *Directory) Pages() []*File {
	var pages []*File
	for _, f := range d.Files {
		if f.IsPage() {
			pages = append(pages, f)
		}
	}
	return pages
}

// PageCount returns the number of pages in the document.
func (d *Directory) PageCount() int { return len(d.Pages()) }

// FileByID returns the component file with the given ID, or nil.
func (d *Directory) FileByID(id string) *File {
	for _, f := range d.Files {
		if f.ID == id {
			return f
		}
	}
	return nil
}

// Parse decodes a DIRM chunk payload.
func Parse(r io.Reader) (*Directory, error) {
	// Unencoded part.
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("dirm: reading header: %w", err)
	}
	dir := &Directory{Version: int(hdr[0] & 0x7f), Bundled: hdr[0]&0x80 != 0}
	if dir.Version > Version {
		return nil, fmt.Errorf("dirm: unsupported directory version %d", dir.Version)
	}
	count := int(hdr[1])<<8 | int(hdr[2])

	for i := 0; i < count; i++ {
		f := &File{PageNum: -1}
		if dir.Bundled {
			var buf [4]byte
			if _, err := io.ReadFull(r, buf[:]); err != nil {
				return nil, fmt.Errorf("dirm: reading offset of file %d: %w", i, err)
			}
			f.Offset = int64(buf[0])<<24 | int64(buf[1])<<16 | int64(buf[2])<<8 | int64(buf[3])
			if f.Offset == 0 {
				return nil, fmt.Errorf("dirm: file %d has zero offset in a bundled directory", i)
			}
			if dir.Version == 0 {
				var s [3]byte
				if _, err := io.ReadFull(r, s[:]); err != nil {
					return nil, fmt.Errorf("dirm: reading size of file %d: %w", i, err)
				}
				f.Size = int64(s[0])<<16 | int64(s[1])<<8 | int64(s[2])
			}
		}
		dir.Files = append(dir.Files, f)
	}

	if count == 0 {
		return dir, nil
	}

	// BZZ compressed part: sizes (version 1), flags, then the string table.
	zr := bzz.NewReader(r)
	br := bufio.NewReader(zr)
	readN := func(n int) (uint32, error) {
		var v uint32
		for i := 0; i < n; i++ {
			b, err := br.ReadByte()
			if err != nil {
				return 0, err
			}
			v = v<<8 | uint32(b)
		}
		return v, nil
	}
	if dir.Version > 0 {
		for i, f := range dir.Files {
			v, err := readN(3)
			if err != nil {
				return nil, fmt.Errorf("dirm: reading size of file %d: %w", i, err)
			}
			f.Size = int64(v)
		}
	}
	for i, f := range dir.Files {
		v, err := readN(1)
		if err != nil {
			return nil, fmt.Errorf("dirm: reading flag of file %d: %w", i, err)
		}
		f.Flags = byte(v)
		if dir.Version == 0 {
			// Convert version 0 flags to version 1 flags.
			var f1 byte
			if f.Flags&flag0IsPage != 0 {
				f1 |= byte(TypePage)
			} else {
				f1 |= byte(TypeInclude)
			}
			if f.Flags&flag0HasName != 0 {
				f1 |= flagHasName
			}
			if f.Flags&flag0HasTitle != 0 {
				f1 |= flagHasTitle
			}
			f.Flags = f1
		}
		f.Type = FileType(f.Flags & flagTypeMask)
	}

	strtab, err := io.ReadAll(br)
	if err != nil {
		return nil, fmt.Errorf("dirm: decoding string table: %w", err)
	}
	pos := 0
	next := func(i int, what string) (string, error) {
		if pos > len(strtab) {
			return "", fmt.Errorf("dirm: string table truncated at file %d", i)
		}
		end := bytes.IndexByte(strtab[pos:], 0)
		if end < 0 {
			return "", fmt.Errorf("dirm: unterminated %s string for file %d", what, i)
		}
		s := string(strtab[pos : pos+end])
		pos += end + 1
		return s, nil
	}
	for i, f := range dir.Files {
		if f.ID, err = next(i, "id"); err != nil {
			return nil, err
		}
		f.Name = f.ID
		f.Title = f.ID
		if f.Flags&flagHasName != 0 {
			if f.Name, err = next(i, "name"); err != nil {
				return nil, err
			}
		}
		if f.Flags&flagHasTitle != 0 {
			if f.Title, err = next(i, "title"); err != nil {
				return nil, err
			}
		}
	}

	if err := dir.validate(); err != nil {
		return nil, err
	}

	// Assign page numbers in directory order.
	page := 0
	for _, f := range dir.Files {
		if f.IsPage() {
			f.PageNum = page
			page++
		}
	}
	return dir, nil
}

// validate enforces the directory consistency rules of the reference
// implementation: at most one shared annotation file, and unique ids,
// names and titles.
func (d *Directory) validate() error {
	shared := 0
	ids := make(map[string]bool, len(d.Files))
	names := make(map[string]bool, len(d.Files))
	titles := make(map[string]bool, len(d.Files))
	for _, f := range d.Files {
		if f.Type == TypeSharedAnno {
			shared++
			if shared > 1 {
				return fmt.Errorf("dirm: more than one file with shared annotations")
			}
		}
		if ids[f.ID] {
			return fmt.Errorf("dirm: duplicate file id %q", f.ID)
		}
		ids[f.ID] = true
		if names[f.Name] {
			return fmt.Errorf("dirm: duplicate file name %q", f.Name)
		}
		names[f.Name] = true
		if f.Title != "" {
			if titles[f.Title] {
				return fmt.Errorf("dirm: duplicate file title %q", f.Title)
			}
			titles[f.Title] = true
		}
	}
	return nil
}
