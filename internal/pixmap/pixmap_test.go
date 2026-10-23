package pixmap

import "testing"

// TestBlitAllocs checks that the alpha blit allocates nothing per
// pixel.
func TestBlitAllocs(t *testing.T) {
	a := NewAlpha(64, 64, 4)
	rows := make([]byte, 32)
	row := func(_ int) []byte { return rows }
	allocs := testing.AllocsPerRun(100, func() {
		a.Blit(row, 32, 32, 0, 0)
	})
	if allocs != 0 {
		t.Errorf("Blit allocated %v times per run; want 0", allocs)
	}
}

// BenchmarkBlit measures the alpha blit throughput.
func BenchmarkBlit(b *testing.B) {
	a := NewAlpha(512, 512, 4)
	rows := make([]byte, 256)
	row := func(_ int) []byte { return rows }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Blit(row, 256, 256, 0, 0)
	}
}
