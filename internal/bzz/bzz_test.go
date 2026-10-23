package bzz

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// goldenPairs maps each golden fixture to its expected plain content.
var goldenPairs = []struct{ compressed, plain string }{
	{"empty.bz", "empty.txt"},
	{"hello.bz", "hello.txt"},
	{"big.bz", "big.txt"},
	{"big64.bz", "big.txt"},
	{"rand.bz", "rand.bin"},
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "bzz", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDecodeGolden checks the decoder against streams compressed by the
// reference implementation's bzz tool (DjVuLibre 3.5.29).
func TestDecodeGolden(t *testing.T) {
	for _, g := range goldenPairs {
		t.Run(g.compressed, func(t *testing.T) {
			compressed := fixture(t, g.compressed)
			plain := fixture(t, g.plain)
			got, err := ReadAll(bytes.NewReader(compressed))
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if !bytes.Equal(got, plain) {
				t.Fatalf("decoded %d bytes, want %d", len(got), len(plain))
			}
		})
	}
}

func TestReadSmallChunks(t *testing.T) {
	compressed := fixture(t, "big64.bz")
	plain := fixture(t, "big.txt")
	r := NewReader(bytes.NewReader(compressed))
	var out bytes.Buffer
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if !bytes.Equal(out.Bytes(), plain) {
		t.Fatalf("small-chunk read decoded %d bytes, want %d", out.Len(), len(plain))
	}
}

func TestReadAllBufferedVsDirect(t *testing.T) {
	compressed := fixture(t, "big.bz")
	plain := fixture(t, "big.txt")
	a, err := ReadAll(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	// Read with a large buffer through the io.Reader interface.
	b, err := io.ReadAll(NewReader(bytes.NewReader(compressed)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, plain) || !bytes.Equal(b, plain) {
		t.Fatal("results differ between ReadAll and io.ReadAll")
	}
}

func TestTruncatedStreams(t *testing.T) {
	compressed := fixture(t, "hello.bz")
	// The BZZ format carries no checksum, so a truncated stream can
	// decode to garbage without an error, exactly like the reference
	// decoder. The invariants are: no panic, no hang, no over-sized
	// output, and the full stream still decodes exactly (TestDecodeGolden).
	for n := 0; n < len(compressed); n++ {
		got, err := ReadAll(bytes.NewReader(compressed[:n]))
		if err == nil && len(got) > maxBlock {
			t.Fatalf("truncation at %d: decoded %d bytes", n, len(got))
		}
	}
}

func TestCorruptStreams(t *testing.T) {
	compressed := fixture(t, "big64.bz")
	// Corrupt bytes must never panic; the result is either data or an
	// error. A single flipped byte frequently decodes without error
	// detection: the format has no checksum, like the reference decoder.
	for i := 0; i < len(compressed); i += 97 {
		mutated := append([]byte(nil), compressed...)
		mutated[i] ^= 0xff
		_, _ = ReadAll(bytes.NewReader(mutated))
	}
}

func FuzzBZZ(f *testing.F) {
	for _, g := range goldenPairs {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "bzz", g.compressed))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte("garbage"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 512<<10 {
			return
		}
		_, _ = ReadAll(bytes.NewReader(data))
	})
}

func BenchmarkDecode(b *testing.B) {
	compressed, err := os.ReadFile(filepath.Join("..", "..", "testdata", "bzz", "big.bz"))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(fixtureOf(b, "big.txt"))))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ReadAll(bytes.NewReader(compressed)); err != nil {
			b.Fatal(err)
		}
	}
}

// fixtureOf is fixture for *testing.B.
func fixtureOf(b *testing.B, name string) []byte {
	b.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "bzz", name))
	if err != nil {
		b.Fatal(err)
	}
	return data
}
