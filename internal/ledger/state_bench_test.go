package ledger

import (
	"fmt"
	"testing"
)

func benchmarkStateHover(b *testing.B, rows int) {
	st := NewState(testSnapshot(rows))
	st.Resize(100, 40, 2, 1)
	st.ScrollTo(rows / 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.HoverScreenRow(3 + i%37)
	}
}

func BenchmarkStateHover(b *testing.B) {
	for _, rows := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			benchmarkStateHover(b, rows)
		})
	}
}
