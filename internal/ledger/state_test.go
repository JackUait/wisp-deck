package ledger

import "testing"

func TestStateHoverMapsViewportRowDirectly(t *testing.T) {
	s := testSnapshot(10_000)
	st := NewState(s)
	st.Resize(80, 24, 2, 1)
	st.ScrollTo(9_000)
	want := s.Rows[9_009].ID

	changed := st.HoverScreenRow(12)

	if !changed || st.Hovered != want {
		t.Fatalf("hover = %v, changed=%v; want %v, true", st.Hovered, changed, want)
	}
	if st.HoverScreenRow(12) {
		t.Fatal("same-row hover must be a no-op")
	}
}

func TestStateHoverClearsOutsideFileRows(t *testing.T) {
	s := testSnapshot(10)
	st := NewState(s)
	st.Resize(80, 10, 2, 1)
	st.HoverScreenRow(4)
	if st.Hovered == (RowID{}) {
		t.Fatal("precondition: a file row should be hovered")
	}

	if !st.HoverScreenRow(3) {
		t.Fatal("moving to the group row should clear hover")
	}
	if st.Hovered != (RowID{}) {
		t.Fatalf("hover = %v, want zero identity", st.Hovered)
	}
	if st.HoverScreenRow(1) {
		t.Fatal("remaining outside the viewport must be a no-op")
	}
}

func TestStateScrollOperationsClamp(t *testing.T) {
	st := NewState(testSnapshot(100))
	st.Resize(80, 14, 2, 1) // 11 visible rows

	st.ScrollTo(10_000)
	if got, want := st.Scroll, len(st.Snapshot.Rows)-11; got != want {
		t.Fatalf("bottom scroll = %d, want %d", got, want)
	}
	st.ScrollBy(-10_000)
	if st.Scroll != 0 {
		t.Fatalf("top scroll = %d, want 0", st.Scroll)
	}
	st.PageBy(1)
	if st.Scroll != 11 {
		t.Fatalf("page scroll = %d, want 11", st.Scroll)
	}
}

func TestStateReconcilePreservesVisibleAnchor(t *testing.T) {
	old := testSnapshot(10_000)
	st := NewState(old)
	st.Resize(80, 24, 2, 1)
	st.ScrollTo(8_000)
	anchor := old.Rows[8_000].ID

	rows := append([]Row(nil), old.Rows[:20]...)
	insertedPath := "src/inserted.go"
	rows = append(rows, Row{
		Kind: RowFile,
		ID:   RowID{Group: GroupModified, Path: insertedPath},
		Path: insertedPath,
	})
	rows = append(rows, old.Rows[20:]...)
	next := NewSnapshot(2, rows, old.Metadata)

	st.ReplaceSnapshot(next)

	if got := st.Snapshot.Rows[st.Scroll].ID; got != anchor {
		t.Fatalf("visible anchor = %v, want %v", got, anchor)
	}
}

func TestStateReconcilePreservesPathSelectionAcrossGroups(t *testing.T) {
	old := NewSnapshot(1, []Row{{
		Kind: RowFile,
		ID:   RowID{Group: GroupModified, Path: "a.go"},
		Path: "a.go",
	}}, Metadata{})
	st := NewState(old)
	st.ToggleSelected("a.go")
	st.Hovered = old.Rows[0].ID
	next := NewSnapshot(2, []Row{{
		Kind: RowFile,
		ID:   RowID{Group: GroupStaged, Path: "a.go"},
		Path: "a.go",
	}}, Metadata{})

	st.ReplaceSnapshot(next)

	if !st.IsSelected("a.go") {
		t.Fatal("selection should follow a path into another status group")
	}
	if st.Hovered != (RowID{}) {
		t.Fatal("hover identity must clear when its exact row ID disappears")
	}
}

func TestStateReconcileDropsMissingSelectionAndHover(t *testing.T) {
	old := testSnapshot(3)
	st := NewState(old)
	st.ToggleSelected(old.Rows[1].Path)
	st.Hovered = old.Rows[1].ID

	st.ReplaceSnapshot(NewSnapshot(2, nil, Metadata{}))

	if len(st.Selected) != 0 {
		t.Fatalf("selected paths = %v, want empty", st.Selected)
	}
	if st.Hovered != (RowID{}) {
		t.Fatalf("hover = %v, want empty", st.Hovered)
	}
	if st.Scroll != 0 {
		t.Fatalf("scroll = %d, want 0", st.Scroll)
	}
}

func TestStateVisibleRowsUsesSnapshotSlice(t *testing.T) {
	st := NewState(testSnapshot(100))
	st.Resize(80, 14, 2, 1)
	st.ScrollTo(50)

	got := st.VisibleRows()

	if len(got) != 11 {
		t.Fatalf("visible rows = %d, want 11", len(got))
	}
	if &got[0] != &st.Snapshot.Rows[50] {
		t.Fatal("state copied rows instead of exposing the snapshot viewport")
	}
}
