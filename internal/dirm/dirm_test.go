package dirm

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/din14970/djvu-go/iff"
)

// dirmFromFixture opens a DjVu fixture, walks to its DIRM chunk and parses
// it, returning the directory and the enclosing FORM:DJVM children.
func dirmFromFixture(t *testing.T, name string) (*Directory, []*iff.Chunk) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "djvu", name))
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := iff.ParseChunks(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("iff.ParseChunks: %v", err)
	}
	if len(chunks) != 1 || chunks[0].FullID() != "FORM:DJVM" {
		t.Fatalf("expected a single FORM:DJVM, got %v", chunks)
	}
	children, err := chunks[0].Children()
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(children) == 0 || children[0].ID != "DIRM" {
		t.Fatalf("expected DIRM as first chunk, got %v", children[0].FullID())
	}
	payload, err := children[0].ReadAll()
	if err != nil {
		t.Fatalf("ReadAll DIRM: %v", err)
	}
	dir, err := Parse(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("dirm.Parse: %v", err)
	}
	return dir, children
}

func TestParseBundledFixture(t *testing.T) {
	dir, children := dirmFromFixture(t, "bundled.djvu")
	if !dir.Bundled || dir.Version != 1 {
		t.Fatalf("version/bundled = %d/%v", dir.Version, dir.Bundled)
	}
	if len(dir.Files) != 4 || dir.PageCount() != 3 {
		t.Fatalf("got %d files, %d pages; want 4 files, 3 pages", len(dir.Files), dir.PageCount())
	}
	wantIDs := []string{"p1.djvu", "shared_anno.iff", "p2.djvu", "p3.djvu"}
	wantTypes := []FileType{TypePage, TypeSharedAnno, TypePage, TypePage}
	wantPages := []int{0, -1, 1, 2}
	for i, f := range dir.Files {
		if f.ID != wantIDs[i] || f.Name != wantIDs[i] || f.Title != wantIDs[i] {
			t.Errorf("file %d: id/name/title = %q/%q/%q, want %q", i, f.ID, f.Name, f.Title, wantIDs[i])
		}
		if f.Type != wantTypes[i] || f.PageNum != wantPages[i] {
			t.Errorf("file %d: type=%v pagenum=%d, want %v/%d", i, f.Type, f.PageNum, wantTypes[i], wantPages[i])
		}
		if f.Offset == 0 {
			t.Errorf("file %d has zero offset in a bundled directory", i)
		}
	}

	// Cross-check DIRM offsets and sizes against the chunks actually
	// present in the file. DIRM offsets are absolute file offsets of the
	// component file records; sizes count the whole record (header plus
	// payload, without padding).
	byStart := map[int64]*iff.Chunk{}
	for _, c := range children[1:] {
		byStart[c.Start] = c
	}
	for _, f := range dir.Files {
		c, ok := byStart[f.Offset]
		if !ok {
			t.Errorf("DIRM offset %d (%s) has no matching chunk", f.Offset, f.ID)
			continue
		}
		if want := 8 + c.Length; f.Size != want {
			t.Errorf("DIRM size of %s = %d, walked chunk size = %d", f.ID, f.Size, want)
		}
	}
}

func TestParseIndirectFixture(t *testing.T) {
	dir, _ := dirmFromFixture(t, "indirect.djvu")
	if dir.Bundled {
		t.Fatal("indirect.djvu parsed as bundled")
	}
	if len(dir.Files) != 4 || dir.PageCount() != 3 {
		t.Fatalf("got %d files, %d pages", len(dir.Files), dir.PageCount())
	}
	for _, f := range dir.Files {
		if f.Offset != 0 {
			t.Errorf("file %s has offset %d in an indirect directory", f.ID, f.Offset)
		}
		if f.Size == 0 {
			t.Errorf("file %s has zero size", f.ID)
		}
	}
	if dir.FileByID("shared_anno.iff") == nil {
		t.Error("shared_anno.iff not found by id")
	}
}

// syntheticTail loads a pre-generated BZZ compressed DIRM tail.
func syntheticTail(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "dirm", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSyntheticV1(t *testing.T) {
	// Indirect v1 directory: 2 files, page with a distinct title plus a
	// shared annotation file.
	hdr := []byte{0x01, 0x00, 0x02}
	payload := append(hdr[:], syntheticTail(t, "tail_v1.bz")...)
	dir, err := Parse(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if dir.Bundled || dir.Version != 1 {
		t.Fatalf("version/bundled = %d/%v", dir.Version, dir.Bundled)
	}
	if len(dir.Files) != 2 {
		t.Fatalf("got %d files", len(dir.Files))
	}
	p := dir.Files[0]
	if p.ID != "p001" || p.Name != "p001" || p.Title != "First Page" || p.Type != TypePage || p.PageNum != 0 {
		t.Errorf("file 0 = %+v", p)
	}
	if p.Size != 1000 || p.Offset != 0 {
		t.Errorf("file 0 size/offset = %d/%d", p.Size, p.Offset)
	}
	s := dir.Files[1]
	if s.ID != "shared" || s.Title != "shared" || s.Type != TypeSharedAnno || s.PageNum != -1 {
		t.Errorf("file 1 = %+v", s)
	}
	if s.Size != 2000 {
		t.Errorf("file 1 size = %d", s.Size)
	}
	if dir.PageCount() != 1 {
		t.Errorf("page count = %d", dir.PageCount())
	}
}

func TestParseSyntheticV0(t *testing.T) {
	// Bundled v0 directory: sizes stored inline, v0 flag bits.
	var buf bytes.Buffer
	buf.Write([]byte{0x80, 0x00, 0x02}) // bundled, version 0, 2 files
	// Offsets and sizes are interleaved per file in version 0.
	if err := binary.Write(&buf, binary.BigEndian, uint32(100)); err != nil {
		t.Fatal(err)
	}
	buf.Write([]byte{0x00, 0x03, 0xE8}) // size of file 0: 1000
	if err := binary.Write(&buf, binary.BigEndian, uint32(200)); err != nil {
		t.Fatal(err)
	}
	buf.Write([]byte{0x00, 0x07, 0xD0}) // size of file 1: 2000
	buf.Write(syntheticTail(t, "tail_v0.bz"))
	dir, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if dir.Bundled != true || dir.Version != 0 {
		t.Fatalf("version/bundled = %d/%v", dir.Version, dir.Bundled)
	}
	if len(dir.Files) != 2 {
		t.Fatalf("got %d files", len(dir.Files))
	}
	p := dir.Files[0]
	if p.ID != "p001" || p.Title != "First Page" || p.Type != TypePage || p.PageNum != 0 {
		t.Errorf("file 0 = %+v", p)
	}
	if p.Offset != 100 || p.Size != 1000 {
		t.Errorf("file 0 offset/size = %d/%d", p.Offset, p.Size)
	}
	s := dir.Files[1]
	if s.ID != "shared" || s.Type != TypeInclude || s.PageNum != -1 || s.Size != 2000 {
		t.Errorf("file 1 = %+v", s)
	}
}

func TestParseEmptyDirectory(t *testing.T) {
	dir, err := Parse(bytes.NewReader([]byte{0x81, 0x00, 0x00}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(dir.Files) != 0 || dir.PageCount() != 0 || !dir.Bundled {
		t.Errorf("dir = %+v", dir)
	}
}

func TestParseUnsupportedVersion(t *testing.T) {
	if _, err := Parse(bytes.NewReader([]byte{0x82, 0x00, 0x00})); err == nil {
		t.Error("expected error for version 2")
	}
}

func TestParseZeroOffsetInBundled(t *testing.T) {
	// The offset check runs before the BZZ part is read, so a garbage
	// tail suffices here.
	payload := []byte{0x81, 0x00, 0x01, 0, 0, 0, 0, 'g', 'a', 'r', 'b'}
	if _, err := Parse(bytes.NewReader(payload)); err == nil {
		t.Error("expected error for zero offset in bundled directory")
	}
}

func TestParseTruncated(t *testing.T) {
	payload := []byte{0x81, 0x00, 0x02, 0, 0, 1} // header cut inside offsets
	if _, err := Parse(bytes.NewReader(payload)); err == nil {
		t.Error("expected error for truncated directory")
	}
}

func TestParseShortStringTable(t *testing.T) {
	// A file count larger than the string table provides strings for.
	// (A count only slightly too large can still decode "successfully"
	// by consuming unrelated strings, exactly like the reference
	// implementation; this count exhausts the table.)
	hdr := []byte{0x01, 0x00, 0x06}                           // claims 6 files
	payload := append(hdr, syntheticTail(t, "tail_v1.bz")...) // has 2
	if _, err := Parse(bytes.NewReader(payload)); err == nil {
		t.Error("expected error for missing strings")
	}
}
