package ledger

// SameContent reports whether two snapshots show the user the same thing.
//
// The ledger polls Git on a timer, and on a real deck 99.1% of those polls find
// nothing changed. Comparing the loaded snapshot against the displayed one is
// what lets an idle pane stop paying for five git processes every two seconds.
//
// Generation is deliberately excluded: it counts loads, so including it would
// make every comparison unequal and the backoff dead code. Row and Metadata are
// comparable structs, so every displayed field -- including line counts, which
// move while a file stays "modified" -- takes part.
func SameContent(a, b Snapshot) bool {
	if a.Metadata != b.Metadata || len(a.Rows) != len(b.Rows) {
		return false
	}
	for i := range a.Rows {
		if a.Rows[i] != b.Rows[i] {
			return false
		}
	}
	return true
}
