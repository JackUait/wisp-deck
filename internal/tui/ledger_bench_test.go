package tui

import (
	"fmt"
	"testing"
)

func BenchmarkLedgerView(b *testing.B) {
	for _, rows := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			m := NewLedgerModel(fakeLedgerSource{}, ledgerTestSnapshot(rows), LedgerOptions{})
			sizeLedger(m, 120, 40)
			m.state.ScrollTo(rows / 2)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.View()
			}
		})
	}
}
