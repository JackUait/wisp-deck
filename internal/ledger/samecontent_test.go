package ledger

import "testing"

func snapshotFor(generation uint64, added int, branch string, ahead int) Snapshot {
	return NewSnapshot(generation, []Row{
		{Kind: RowGroup, Label: "modified", Count: 1},
		{Kind: RowFile, ID: RowID{Group: GroupModified, Path: "a.go"}, Path: "a.go", Added: added, Deleted: 1},
	}, Metadata{Branch: branch, Ahead: ahead, TotalFiles: 1, Added: added, Deleted: 1})
}

func TestSameContent_ignores_the_generation_counter(t *testing.T) {
	if !SameContent(snapshotFor(1, 5, "main", 0), snapshotFor(99, 5, "main", 0)) {
		t.Error("two identical changesets compared unequal because their generations differ")
	}
}

func TestSameContent_sees_an_edit_that_only_moves_line_counts(t *testing.T) {
	// A file edited twice stays "modified"; only its line counts move. Missing
	// this would freeze the ledger on a repository that is actively changing.
	if SameContent(snapshotFor(1, 5, "main", 0), snapshotFor(2, 6, "main", 0)) {
		t.Error("a changed line count compared equal")
	}
}

func TestSameContent_sees_metadata_changes(t *testing.T) {
	if SameContent(snapshotFor(1, 5, "main", 0), snapshotFor(2, 5, "feature", 0)) {
		t.Error("a branch switch compared equal")
	}
	if SameContent(snapshotFor(1, 5, "main", 0), snapshotFor(2, 5, "main", 3)) {
		t.Error("a new commit ahead of upstream compared equal")
	}
}

func TestSameContent_sees_a_row_appearing(t *testing.T) {
	base := snapshotFor(1, 5, "main", 0)
	grown := NewSnapshot(2, append(append([]Row{}, base.Rows...),
		Row{Kind: RowFile, ID: RowID{Group: GroupNew, Path: "b.go"}, Path: "b.go"}),
		base.Metadata)
	if SameContent(base, grown) {
		t.Error("a new untracked file compared equal")
	}
}
