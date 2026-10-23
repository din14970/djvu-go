// Package djvu is a pure Go reader for the DjVu file format. It parses
// single-page and multipage (bundled and indirect) documents and gives
// access to their structure: pages, dimensions and resolution (INFO),
// annotations, document metadata, hidden text and outlines.
//
// Image decoding (JB2, IW44) is not implemented; this package reads
// structure and metadata only.
//
// The entry point is [Open], which accepts any io.Reader (files, byte
// slices, embedded filesystem files). Inputs implementing io.ReaderAt
// are accessed in place, others are buffered in memory.
package djvu

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/din14970/djvu-go/iff"
	"github.com/din14970/djvu-go/internal/bzz"
	"github.com/din14970/djvu-go/internal/dirm"
)

// DocumentKind classifies the storage model of a DjVu document.
type DocumentKind int

const (
	// KindSinglePage is a single-page document: a top-level FORM:DJVU.
	KindSinglePage DocumentKind = iota
	// KindBundled is a multipage document whose component files are all
	// stored inside one file (FORM:DJVM with a bundled DIRM).
	KindBundled
	// KindIndirect is a multipage document whose component files are
	// stored as external files (FORM:DJVM with an indirect DIRM). The
	// directory can be inspected, but loading page data from the external
	// files is not supported yet.
	KindIndirect
)

// String returns a human readable name for the kind.
func (k DocumentKind) String() string {
	switch k {
	case KindSinglePage:
		return "single-page"
	case KindBundled:
		return "bundled"
	case KindIndirect:
		return "indirect"
	}
	return fmt.Sprintf("DocumentKind(%d)", int(k))
}

// ComponentType classifies a component file of a multipage document.
type ComponentType int

const (
	// ComponentInclude is a shared component file included by other files.
	ComponentInclude ComponentType = iota
	// ComponentPage is a file representing a page of the document.
	ComponentPage
	// ComponentThumbnails is a file holding page thumbnails.
	ComponentThumbnails
	// ComponentSharedAnnotations is the file holding annotations shared
	// by all pages.
	ComponentSharedAnnotations
)

// String returns the reference library's name for the component type.
func (t ComponentType) String() string {
	switch t {
	case ComponentInclude:
		return "INCLUDE"
	case ComponentPage:
		return "PAGE"
	case ComponentThumbnails:
		return "THUMBNAILS"
	case ComponentSharedAnnotations:
		return "SHARED_ANNO"
	}
	return fmt.Sprintf("ComponentType(%d)", int(t))
}

// ComponentFile describes one component file listed in the directory of
// a multipage document.
type ComponentFile struct {
	// ID is the identifier referenced by INCL chunks of other files.
	ID string
	// Name is the file name under which the component is stored in
	// indirect documents.
	Name string
	// Title is the cosmetic display title.
	Title string
	// Type classifies the component.
	Type ComponentType
	// Offset is the byte offset of the component record within the
	// bundled document (0 for indirect documents).
	Offset int64
	// Size is the byte size of the component record.
	Size int64
}

// PageInfo describes one page of a document.
type PageInfo struct {
	// Index is the zero-based page number.
	Index int
	// Name is the identifier of the page's component file in the DIRM
	// directory, or the empty string for single-page documents.
	Name string
	// Offset is the byte offset of the page's component record within
	// the file (0 for single-page and indirect documents).
	Offset int64
	// Size is the byte size of the page's component record (0 for
	// indirect documents).
	Size int64

	// Width and Height are the page dimensions in pixels.
	Width  int
	Height int
	// DPI is the resolution in dots per inch.
	DPI int
	// Gamma is the display gamma the page was designed for.
	Gamma float64
	// Version is the DjVu format version recorded in the INFO chunk.
	Version int
	// Orientation is the page rotation (0-3, see [Info]).
	Orientation int

	// HasInfo reports whether the INFO fields above could be read. It is
	// false for indirect documents (whose page data lives in external
	// files) and for pages without an INFO chunk.
	HasInfo bool
}

// page is the internal per-page state.
type page struct {
	file     *dirm.File // directory entry; nil for single-page documents
	form     *iff.Chunk // the page's component chunk; nil for indirect docs
	children []*iff.Chunk
	info     Info
	hasInfo  bool
}

// component is the internal per-component state of a bundled document.
type component struct {
	file     *dirm.File
	form     *iff.Chunk
	children []*iff.Chunk
}

// Document is a parsed DjVu document. Its methods are safe for
// concurrent use by multiple goroutines; all parsing happens in [Open].
type Document struct {
	kind   DocumentKind
	single *page
	dir    *dirm.Directory
	pages  []*page
	comps  map[string]*component
	shared *component
	navm   *iff.Chunk

	// Decoded page layers, filled on demand by Render.
	renderMu    sync.Mutex
	renderCache map[int]*pageLayers
}

// Open parses a DjVu document from r, which may be a single-page image
// (FORM:DJVU), a bundled multipage document (FORM:DJVM with bundled
// DIRM) or an indirect multipage document (FORM:DJVM with indirect
// DIRM).
//
// size is the total size of r in bytes, or -1 when unknown. It is used
// only to reject empty inputs early; the parser determines everything
// else from the data itself.
//
// If r implements io.ReaderAt (as *os.File, *bytes.Reader and files
// opened from an embed.FS do), the document is parsed in place and chunk
// payloads are read lazily from r. Otherwise r is read fully into
// memory.
func Open(r io.Reader, size int64) (*Document, error) {
	if r == nil {
		return nil, fmt.Errorf("djvu: nil reader")
	}
	if size == 0 {
		return nil, fmt.Errorf("djvu: empty document")
	}
	chunks, err := iff.ParseChunks(r)
	if err != nil {
		return nil, fmt.Errorf("djvu: parsing chunks: %w", err)
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("djvu: no top-level chunk: not a DjVu document")
	}
	if len(chunks) > 1 {
		return nil, fmt.Errorf("djvu: %d top-level chunks: trailing data after document", len(chunks))
	}
	top := chunks[0]
	switch {
	case top.FullID() == "FORM:DJVU":
		return openSinglePage(top)
	case top.FullID() == "FORM:PM44" || top.FullID() == "FORM:BM44":
		// Obsolete IW44 wavelet image files have no INFO chunk.
		return openSinglePage(top)
	case top.FullID() == "FORM:DJVM":
		return openMultipage(top)
	}
	return nil, fmt.Errorf("djvu: unsupported top-level chunk %s", top.FullID())
}

// openSinglePage builds a document from a top-level page chunk.
func openSinglePage(top *iff.Chunk) (*Document, error) {
	d := &Document{kind: KindSinglePage, comps: map[string]*component{}}
	p := &page{form: top}
	if top.Composite() {
		children, err := top.Children()
		if err != nil {
			return nil, fmt.Errorf("djvu: parsing page chunks: %w", err)
		}
		p.children = children
	}
	if err := d.loadPageInfo(p); err != nil {
		return nil, err
	}
	d.single = p
	d.pages = []*page{p}
	return d, nil
}

// openMultipage builds a document from a top-level FORM:DJVM chunk.
func openMultipage(top *iff.Chunk) (*Document, error) {
	children, err := top.Children()
	if err != nil {
		return nil, fmt.Errorf("djvu: parsing document chunks: %w", err)
	}
	if len(children) == 0 || children[0].ID != "DIRM" {
		return nil, fmt.Errorf("djvu: FORM:DJVM without a DIRM chunk")
	}
	dirmData, err := children[0].ReadAll()
	if err != nil {
		return nil, fmt.Errorf("djvu: reading DIRM: %w", err)
	}
	dir, err := dirm.Parse(bytes.NewReader(dirmData))
	if err != nil {
		return nil, fmt.Errorf("djvu: parsing DIRM: %w", err)
	}

	d := &Document{kind: KindBundled, dir: dir, comps: map[string]*component{}}
	if !dir.Bundled {
		d.kind = KindIndirect
	}

	// Collect the remaining chunks of the FORM:DJVM.
	var navm *iff.Chunk
	byStart := make(map[int64]*iff.Chunk, len(dir.Files))
	for _, c := range children[1:] {
		if c.ID == "NAVM" && navm == nil {
			navm = c
		}
		byStart[c.Start] = c
	}
	d.navm = navm

	if d.kind == KindIndirect {
		// Component data lives in external files; only the directory is
		// available.
		for _, f := range dir.Pages() {
			d.pages = append(d.pages, &page{file: f})
		}
		return d, nil
	}

	// Resolve every component file to its chunk.
	for _, f := range dir.Files {
		form, ok := byStart[f.Offset]
		if !ok {
			return nil, fmt.Errorf("djvu: directory entry %q points at offset %d, where no component record begins", f.ID, f.Offset)
		}
		comp := &component{file: f, form: form}
		if form.Composite() {
			compChildren, err := form.Children()
			if err != nil {
				return nil, fmt.Errorf("djvu: parsing component %q: %w", f.ID, err)
			}
			comp.children = compChildren
		}
		d.comps[f.ID] = comp
		switch f.Type {
		case dirm.TypePage:
			p := &page{file: f, form: form, children: comp.children}
			if err := d.loadPageInfo(p); err != nil {
				return nil, fmt.Errorf("djvu: page %q: %w", f.ID, err)
			}
			d.pages = append(d.pages, p)
		case dirm.TypeSharedAnno:
			d.shared = comp
		}
	}
	return d, nil
}

// loadPageInfo locates and parses the INFO chunk of a page.
func (d *Document) loadPageInfo(p *page) error {
	for _, c := range p.children {
		if c.ID != "INFO" {
			continue
		}
		data, err := c.ReadAll()
		if err != nil {
			return fmt.Errorf("reading INFO: %w", err)
		}
		info, err := parseInfo(data)
		if err != nil {
			return err
		}
		p.info = info
		p.hasInfo = true
		break
	}
	return nil
}

// Kind returns the storage model of the document.
func (d *Document) Kind() DocumentKind { return d.kind }

// PageCount returns the number of pages of the document. For indirect
// documents this is the number of page entries in the directory.
func (d *Document) PageCount() (int, error) {
	return len(d.pages), nil
}

// Pages returns information about every page of the document, ordered by
// page number. For indirect documents the INFO-derived fields are left
// at their zero values and HasInfo is false, because the page data is
// stored in external files.
func (d *Document) Pages() ([]PageInfo, error) {
	out := make([]PageInfo, len(d.pages))
	for i, p := range d.pages {
		pi := PageInfo{
			Index:       i,
			Width:       p.info.Width,
			Height:      p.info.Height,
			DPI:         p.info.DPI,
			Gamma:       p.info.Gamma,
			Version:     p.info.Version,
			Orientation: p.info.Orientation,
			HasInfo:     p.hasInfo,
		}
		if p.file != nil {
			pi.Name = p.file.ID
			pi.Offset = p.file.Offset
			pi.Size = p.file.Size
		} else if p.form != nil {
			pi.Offset = p.form.Start
			pi.Size = 8 + p.form.Length
		}
		out[i] = pi
	}
	return out, nil
}

// Components returns the component files listed in the directory of a
// multipage document. It returns nil for single-page documents.
func (d *Document) Components() []ComponentFile {
	if d.dir == nil {
		return nil
	}
	out := make([]ComponentFile, 0, len(d.dir.Files))
	for _, f := range d.dir.Files {
		cf := ComponentFile{ID: f.ID, Name: f.Name, Title: f.Title, Offset: f.Offset, Size: f.Size}
		switch f.Type {
		case dirm.TypeInclude:
			cf.Type = ComponentInclude
		case dirm.TypePage:
			cf.Type = ComponentPage
		case dirm.TypeThumbnails:
			cf.Type = ComponentThumbnails
		case dirm.TypeSharedAnno:
			cf.Type = ComponentSharedAnnotations
		}
		out = append(out, cf)
	}
	return out
}

// page returns the internal state of page i.
func (d *Document) page(i int) (*page, error) {
	if i < 0 || i >= len(d.pages) {
		return nil, fmt.Errorf("djvu: page %d out of range (document has %d pages)", i, len(d.pages))
	}
	return d.pages[i], nil
}

// Annotations returns the annotation expressions of the given page: the
// contents of its ANTa/ANTz chunks plus the annotations merged from the
// component files it includes and from the document's shared annotation
// file. Annotations of included files come first, the page's own
// annotations next, so that page annotations take precedence, matching
// the merge order of the reference implementation.
//
// For indirect documents it returns an error: the annotation data is
// stored in external files.
func (d *Document) Annotations(page int) (Annotations, error) {
	p, err := d.page(page)
	if err != nil {
		return nil, err
	}
	if p.children == nil {
		if d.kind == KindSinglePage {
			// A non-composite top-level chunk cannot hold annotations.
			return nil, nil
		}
		return nil, errIndirect
	}
	var buf bytes.Buffer
	visited := make(map[string]bool)
	// Included files first (lower precedence), like DjVuFile::get_merged_anno.
	for _, id := range inclIDs(p.children) {
		if err := d.gatherAnno(id, &buf, visited, 0); err != nil {
			return nil, err
		}
	}
	gatherAnnoChunks(p.children, &buf)
	// Shared annotations apply to every page, even when the pages do not
	// reference the shared file through an INCL chunk.
	if d.shared != nil && !visited[d.shared.file.ID] {
		gatherAnnoChunks(d.shared.children, &buf)
	}
	return parseAnnotations(buf.Bytes()), nil
}

// gatherAnno appends the annotation chunks of the component with the
// given id, first recursing into the component's own includes.
func (d *Document) gatherAnno(id string, buf *bytes.Buffer, visited map[string]bool, depth int) error {
	if depth > 32 {
		return fmt.Errorf("djvu: INCL chain nested deeper than 32 levels")
	}
	comp := d.comps[id]
	if comp == nil || visited[id] {
		return nil
	}
	visited[id] = true
	for _, sub := range inclIDs(comp.children) {
		if err := d.gatherAnno(sub, buf, visited, depth+1); err != nil {
			return err
		}
	}
	gatherAnnoChunks(comp.children, buf)
	return nil
}

// gatherAnnoChunks appends the decoded contents of the annotation chunks
// (ANTa, ANTz, and the obsolete FORM:ANNO composite) found in children.
func gatherAnnoChunks(children []*iff.Chunk, buf *bytes.Buffer) {
	for _, c := range children {
		switch {
		case c.ID == "ANTa":
			if data, err := c.ReadAll(); err == nil {
				buf.Write(data)
			}
		case c.ID == "ANTz":
			if data, err := bzz.ReadAll(c.Payload()); err == nil {
				buf.Write(data)
			}
		case c.FullID() == "FORM:ANNO":
			sub, err := c.Children()
			if err == nil {
				gatherAnnoChunks(sub, buf)
			}
		}
	}
}

// inclIDs returns the component ids referenced by INCL chunks, in order.
func inclIDs(children []*iff.Chunk) []string {
	var ids []string
	for _, c := range children {
		if c.ID != "INCL" {
			continue
		}
		data, err := c.ReadAll()
		if err != nil {
			continue
		}
		id := strings.Trim(string(data), "\x00\r\n\t ")
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// Metadata returns the document-level metadata strings (title, author,
// subject, keywords, producer and any other keys) collected from the
// (metadata ...) annotation expressions of the shared annotation file
// and the pages. Fields are empty when absent. For indirect documents
// the result is empty, since the annotation data lives in external
// files.
func (d *Document) Metadata() (Metadata, error) {
	entries := make(map[string]string)
	switch d.kind {
	case KindSinglePage:
		annos, err := d.Annotations(0)
		if err != nil {
			return Metadata{}, err
		}
		for k, v := range annos.Metadata() {
			entries[k] = v
		}
	case KindBundled:
		// The shared annotation file has the lowest precedence, page
		// metadata overwrites it.
		if d.shared != nil {
			var buf bytes.Buffer
			gatherAnnoChunks(d.shared.children, &buf)
			for k, v := range parseAnnotations(buf.Bytes()).Metadata() {
				entries[k] = v
			}
		}
		for i := range d.pages {
			annos, err := d.Annotations(i)
			if err != nil {
				return Metadata{}, err
			}
			for k, v := range annos.Metadata() {
				entries[k] = v
			}
		}
	}
	return metadataFrom(entries), nil
}
