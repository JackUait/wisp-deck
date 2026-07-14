package ledger

import (
	"fmt"
	"testing"
)

func testSnapshot(n int) Snapshot {
	rows := []Row{{Kind: RowGroup, Label: "modified"}}
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("src/file-%05d.go", i)
		rows = append(rows, Row{
			Kind:    RowFile,
			ID:      RowID{Group: GroupModified, Path: path},
			Path:    path,
			Added:   12,
			Deleted: 3,
		})
	}
	return NewSnapshot(1, rows, Metadata{})
}

func TestSnapshotIndexesFilesByStableID(t *testing.T) {
	s := testSnapshot(10_000)
	id := RowID{Group: GroupModified, Path: "src/file-09999.go"}

	idx, ok := s.Index(id)

	if !ok || idx != 10_000 {
		t.Fatalf("Index(%v) = %d, %v; want 10000, true", id, idx, ok)
	}
}

func TestSnapshotDoesNotIndexStructuralRows(t *testing.T) {
	s := NewSnapshot(1, []Row{
		{Kind: RowGroup, Label: "modified"},
		{Kind: RowSpacer},
	}, Metadata{})

	if _, ok := s.Index(RowID{}); ok {
		t.Fatal("structural rows must not share an indexed zero identity")
	}
}

func TestSnapshotVisibleRowsSharesBackingStorage(t *testing.T) {
	s := testSnapshot(10_000)

	got := s.VisibleRows(9_990, 20)

	if len(got) != 11 {
		t.Fatalf("visible rows = %d, want 11", len(got))
	}
	if &got[0] != &s.Rows[9_990] {
		t.Fatal("VisibleRows copied rows instead of slicing the snapshot")
	}
}

func TestSnapshotVisibleRowsClampsBounds(t *testing.T) {
	s := testSnapshot(3)

	tests := []struct {
		name         string
		start, count int
		want         int
	}{
		{name: "negative start", start: -5, count: 2, want: 2},
		{name: "past end", start: 20, count: 2, want: 0},
		{name: "negative count", start: 0, count: -1, want: 0},
		{name: "clipped end", start: 2, count: 20, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(s.VisibleRows(tt.start, tt.count)); got != tt.want {
				t.Fatalf("len(VisibleRows(%d, %d)) = %d, want %d", tt.start, tt.count, got, tt.want)
			}
		})
	}
}
