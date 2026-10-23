package djvu

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openFixture opens a golden fixture produced by the djvulibre tools
// (see testdata/djvu/generate.sh).
func openFixture(t *testing.T, name string) *Document {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "djvu", name))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open(%s): %v", name, err)
	}
	return doc
}

func TestOpenSinglePage(t *testing.T) {
	doc := openFixture(t, "single.djvu")
	if doc.Kind() != KindSinglePage {
		t.Fatalf("kind = %v", doc.Kind())
	}
	n, err := doc.PageCount()
	if err != nil || n != 1 {
		t.Fatalf("page count = %d, %v", n, err)
	}
	pages, err := doc.Pages()
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages", len(pages))
	}
	p := pages[0]
	if !p.HasInfo || p.Width != 200 || p.Height != 280 || p.DPI != 150 || p.Version != 24 || p.Gamma != 2.2 {
		t.Errorf("page info = %+v", p)
	}
	if p.Name != "" || p.Index != 0 {
		t.Errorf("page name/index = %q/%d", p.Name, p.Index)
	}
	if p.Offset != 4 || p.Size != 212 {
		// The chunk starts after the four AT&T magic bytes written by
		// the reference tools.
		t.Errorf("page offset/size = %d/%d", p.Offset, p.Size)
	}

	text, err := doc.PageText(0)
	if err != nil || text != "Hello single page" {
		t.Errorf("text = %q, %v", text, err)
	}

	annos, err := doc.Annotations(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(annos) != 2 {
		t.Fatalf("got %d annotation expressions: %v", len(annos), annos)
	}
	if annos[0].Key() != "zoom" || len(annos[0].List) != 2 || annos[0].List[1].Text != "stretch" {
		t.Errorf("zoom expression = %s", annos[0])
	}
	if annos[1].Key() != "maparea" {
		t.Errorf("second expression = %s", annos[1])
	}
	// The maparea url, comment and rect are preserved.
	ma := annos[1].List
	if ma[1].Text != "https://example.org/" || ma[2].Text != "External link" || ma[3].Key() != "rect" {
		t.Errorf("maparea = %s", annos[1])
	}

	meta, err := doc.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "" || meta.Author != "" {
		t.Errorf("single page without metadata: %+v", meta)
	}
	ol, err := doc.Outline()
	if err != nil || ol != nil {
		t.Errorf("outline = %v, %v", ol, err)
	}
	if comps := doc.Components(); comps != nil {
		t.Errorf("single page components = %v", comps)
	}
}

func TestOpenBundled(t *testing.T) {
	doc := openFixture(t, "bundled.djvu")
	if doc.Kind() != KindBundled {
		t.Fatalf("kind = %v", doc.Kind())
	}
	n, _ := doc.PageCount()
	if n != 3 {
		t.Fatalf("page count = %d, want 3", n)
	}
	pages, err := doc.Pages()
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"p1.djvu", "p2.djvu", "p3.djvu"}
	for i, p := range pages {
		if p.Index != i || p.Name != wantNames[i] || !p.HasInfo {
			t.Errorf("page %d = %+v", i, p)
			continue
		}
		if p.Width != 200 || p.Height != 280 || p.DPI != 150 || p.Version != 24 {
			t.Errorf("page %d info = %+v", i, p)
		}
		if p.Offset == 0 || p.Size == 0 {
			t.Errorf("page %d offset/size = %d/%d", i, p.Offset, p.Size)
		}
	}

	// Hidden text: page 0 has a plain string, page 1 a word structure,
	// page 2 none.
	text, err := doc.PageText(0)
	if err != nil || text != "Hello hidden text page one" {
		t.Errorf("page 0 text = %q, %v", text, err)
	}
	text, err = doc.PageText(1)
	if err != nil || !strings.HasPrefix(text, "Second page") {
		t.Errorf("page 1 text = %q, %v", text, err)
	}
	text, err = doc.PageText(2)
	if err != nil || text != "" {
		t.Errorf("page 2 text = %q, %v", text, err)
	}

	// Annotations: page 0 merges the shared metadata with its own zoom
	// and maparea expressions.
	annos, err := doc.Annotations(0)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, a := range annos {
		keys = append(keys, a.Key())
	}
	if strings.Join(keys, ",") != "metadata,zoom,maparea" {
		t.Errorf("annotation keys = %v", keys)
	}
	// Page 1 only inherits the shared annotations.
	annos, err = doc.Annotations(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(annos) != 1 || annos[0].Key() != "metadata" {
		t.Errorf("page 1 annotations = %v", annos)
	}

	meta, err := doc.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Example Document" || meta.Author != "Jane Doe" ||
		meta.Subject != "DjVu test fixture" || meta.Keywords != "djvu, test, fixture" ||
		meta.Producer != "djvulibre 3.5.29" {
		t.Errorf("metadata = %+v", meta)
	}
	if meta.Get("Title") != "Example Document" || meta.Get("TITLE") != "Example Document" {
		t.Errorf("Get(title) = %q", meta.Get("Title"))
	}
	if meta.Get("nonexistent") != "" {
		t.Errorf("Get(nonexistent) = %q", meta.Get("nonexistent"))
	}

	// Outline.
	ol, err := doc.Outline()
	if err != nil {
		t.Fatal(err)
	}
	if len(ol) != 2 {
		t.Fatalf("outline has %d roots: %+v", len(ol), ol)
	}
	if ol[0].Title != "Chapter 1" || ol[0].URL != "#p1.djvu" || len(ol[0].Children) != 0 {
		t.Errorf("bookmark 0 = %+v", ol[0])
	}
	if ol[1].Title != "Chapter 2" || len(ol[1].Children) != 1 {
		t.Errorf("bookmark 1 = %+v", ol[1])
	}
	if ol[1].Children[0].Title != "Section 2.1" || ol[1].Children[0].URL != "#p3.djvu" {
		t.Errorf("bookmark 1 child = %+v", ol[1].Children[0])
	}

	// Components.
	comps := doc.Components()
	if len(comps) != 4 {
		t.Fatalf("got %d components", len(comps))
	}
	if comps[0].ID != "p1.djvu" || comps[0].Type != ComponentPage {
		t.Errorf("component 0 = %+v", comps[0])
	}
	if comps[1].ID != "shared_anno.iff" || comps[1].Type != ComponentSharedAnnotations {
		t.Errorf("component 1 = %+v", comps[1])
	}
}

func TestOpenIndirect(t *testing.T) {
	doc := openFixture(t, "indirect.djvu")
	if doc.Kind() != KindIndirect {
		t.Fatalf("kind = %v", doc.Kind())
	}
	n, _ := doc.PageCount()
	if n != 3 {
		t.Fatalf("page count = %d", n)
	}
	pages, err := doc.Pages()
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range pages {
		if p.HasInfo {
			t.Errorf("page %d claims INFO data", i)
		}
		if p.Width != 0 || p.DPI != 0 {
			t.Errorf("page %d info fields not zero: %+v", i, p)
		}
	}
	if pages[0].Name != "p1.djvu" {
		t.Errorf("page 0 name = %q", pages[0].Name)
	}
	// Page data accessors report that the data is external.
	if _, err := doc.PageText(0); err == nil {
		t.Error("PageText should fail for indirect documents")
	}
	if _, err := doc.Annotations(0); err == nil {
		t.Error("Annotations should fail for indirect documents")
	}
	// The outline lives in the index file and is available.
	ol, err := doc.Outline()
	if err != nil || len(ol) != 2 {
		t.Errorf("outline = %+v, %v", ol, err)
	}
	// Component names are exposed for external loading.
	var names []string
	for _, c := range doc.Components() {
		names = append(names, c.Name)
	}
	want := "p1.djvu,shared_anno.iff,p2.djvu,p3.djvu"
	if strings.Join(names, ",") != want {
		t.Errorf("component names = %v", names)
	}
}

func TestOpenFromOSFile(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "djvu", "bundled.djvu"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Open(f, fi.Size())
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := doc.PageCount(); n != 3 {
		t.Fatalf("page count = %d", n)
	}
	if _, err := doc.Metadata(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenNonSeekable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "djvu", "bundled.djvu"))
	if err != nil {
		t.Fatal(err)
	}
	r := struct{ io.Reader }{bytes.NewReader(data)}
	doc, err := Open(r, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := doc.PageCount(); n != 3 {
		t.Fatalf("page count = %d", n)
	}
	if meta, _ := doc.Metadata(); meta.Title != "Example Document" {
		t.Errorf("metadata title = %q", meta.Title)
	}
}

func TestOpenErrors(t *testing.T) {
	tests := []struct {
		name string
		r    func() (io.Reader, int64)
	}{
		{"nil reader", func() (io.Reader, int64) { return nil, -1 }},
		{"empty", func() (io.Reader, int64) {
			return bytes.NewReader(nil), 0
		}},
		{"garbage", func() (io.Reader, int64) {
			return bytes.NewReader([]byte("this is not a djvu file at all, really")), int64(36)
		}},
		{"truncated form", func() (io.Reader, int64) {
			return bytes.NewReader([]byte("FORM\x00\x00\x00\x0eDJVUINFO\x00\x00\x00")), -1
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, size := tt.r()
			if r == nil {
				if _, err := Open(nil, size); err == nil {
					t.Error("expected error")
				}
				return
			}
			if _, err := Open(r, size); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestPageOutOfRange(t *testing.T) {
	doc := openFixture(t, "bundled.djvu")
	if _, err := doc.Annotations(-1); err == nil {
		t.Error("Annotations(-1) should fail")
	}
	if _, err := doc.Annotations(3); err == nil {
		t.Error("Annotations(3) should fail")
	}
	if _, err := doc.PageText(-1); err == nil {
		t.Error("PageText(-1) should fail")
	}
	if _, err := doc.Pages(); err != nil {
		t.Errorf("Pages: %v", err)
	}
}

func TestParseInfoVariants(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want Info
	}{
		{
			name: "full 10 byte info",
			// width 320, height 240, version 24, dpi 300, gamma 2.2,
			// orientation flags 2 (180 degrees).
			data: []byte{0x01, 0x40, 0x00, 0xF0, 24, 0, 0x2C, 0x01, 22, 2},
			want: Info{Width: 320, Height: 240, Version: 24, DPI: 300, Gamma: 2.2, Orientation: 2},
		},
		{
			name: "single byte version with 0xff high byte",
			data: []byte{0x01, 0x40, 0x00, 0xF0, 24, 0xff, 0x2C, 0x01, 22, 1},
			want: Info{Width: 320, Height: 240, Version: 24, DPI: 300, Gamma: 2.2, Orientation: 0},
		},
		{
			name: "dpi 0xff high byte falls back to 300",
			data: []byte{0x01, 0x40, 0x00, 0xF0, 24, 0, 0x2C, 0xff, 22, 1},
			want: Info{Width: 320, Height: 240, Version: 24, DPI: 300, Gamma: 2.2, Orientation: 0},
		},
		{
			name: "dpi out of range falls back to 300",
			data: []byte{0x01, 0x40, 0x00, 0xF0, 24, 0, 0x10, 0x00, 22, 1},
			want: Info{Width: 320, Height: 240, Version: 24, DPI: 300, Gamma: 2.2, Orientation: 0},
		},
		{
			name: "orientation flags 6 is 90 degrees ccw",
			data: []byte{0x01, 0x40, 0x00, 0xF0, 24, 0, 0x2C, 0x01, 22, 6},
			want: Info{Width: 320, Height: 240, Version: 24, DPI: 300, Gamma: 2.2, Orientation: 1},
		},
		{
			name: "orientation flags 5 is 270 degrees ccw",
			data: []byte{0x01, 0x40, 0x00, 0xF0, 24, 0, 0x2C, 0x01, 22, 5},
			want: Info{Width: 320, Height: 240, Version: 24, DPI: 300, Gamma: 2.2, Orientation: 3},
		},
		{
			name: "5 byte info",
			data: []byte{0x01, 0x40, 0x00, 0xF0, 24},
			want: Info{Width: 320, Height: 240, Version: 24, DPI: 300, Gamma: 2.2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseInfo(tt.data)
			if err != nil {
				t.Fatalf("parseInfo: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseInfo = %+v, want %+v", got, tt.want)
			}
		})
	}
	if _, err := parseInfo([]byte{1, 2, 3, 4}); err == nil {
		t.Error("expected error for 4-byte INFO")
	}
	if _, err := parseInfo(nil); err == nil {
		t.Error("expected error for empty INFO")
	}
}

func TestParseAnnotations(t *testing.T) {
	src := `(metadata (title "A \"quoted\" title") (year "1999"))
(maparea "#p" "ct" (rect 1 2 3 4) (border #FF0000))
(garbage`
	annos := parseAnnotations([]byte(src))
	if len(annos) != 3 {
		t.Fatalf("got %d expressions: %v", len(annos), annos)
	}
	if annos[0].Key() != "metadata" {
		t.Errorf("first expression = %s", annos[0])
	}
	title := annos[0].List[1].List[1].Text
	if title != `A "quoted" title` {
		t.Errorf("escaped title = %q", title)
	}
	meta := annos.Metadata()
	if meta["title"] != `A "quoted" title` || meta["year"] != "1999" {
		t.Errorf("metadata = %v", meta)
	}
	if annos[2].Key() != "garbage" || len(annos[2].List) != 1 {
		// The unterminated last list is tolerated and kept.
		t.Errorf("third expression = %s", annos[2])
	}
}

func TestMetadataFromOthers(t *testing.T) {
	m := metadataFrom(map[string]string{
		"title":     "T",
		"booktitle": "BT",
		"year":      "2001",
	})
	if m.Title != "T" || m.Others["booktitle"] != "BT" || m.Others["year"] != "2001" {
		t.Errorf("metadata = %+v", m)
	}
	if m.Get("BookTitle") != "BT" || m.Get("Year") != "2001" {
		t.Errorf("Get = %q %q", m.Get("BookTitle"), m.Get("Year"))
	}
}
