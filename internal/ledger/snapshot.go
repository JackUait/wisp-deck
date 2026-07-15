// Package ledger contains the data and interaction core for the changes ledger.
package ledger

// Group identifies the Git state section that owns a file row.
type Group uint8

const (
	GroupNone Group = iota
	GroupStaged
	GroupModified
	GroupNew
)

// RowKind distinguishes files from structural rows in a snapshot.
type RowKind uint8

const (
	RowGroup RowKind = iota
	RowFile
	RowSpacer
)

// RowID is a stable identity for a file row across snapshot refreshes.
type RowID struct {
	Group Group
	Path  string
}

// Row is one structural or file row in the ledger.
type Row struct {
	Kind     RowKind
	Group    Group
	ID       RowID
	Path     string
	Label    string
	Count    int
	Added    int
	Deleted  int
	Binary   bool
	OldBytes int64
	NewBytes int64
}

// RowVisualState contains transient presentation state for one visible row.
type RowVisualState struct {
	Hovered  bool
	Selected bool
}

// Metadata holds snapshot-wide values rendered outside the scrolling viewport.
type Metadata struct {
	Branch     string
	Ahead      int
	Behind     int
	Plan       string
	TotalFiles int
	Added      int
	Deleted    int
}

// Snapshot is an immutable ledger generation with a precomputed row index.
type Snapshot struct {
	Generation uint64
	Rows       []Row
	Metadata   Metadata
	index      map[RowID]int
}

// NewSnapshot creates a generation and indexes its file rows once.
func NewSnapshot(generation uint64, rows []Row, metadata Metadata) Snapshot {
	index := make(map[RowID]int, metadata.TotalFiles)
	for i, row := range rows {
		if row.Kind == RowFile {
			index[row.ID] = i
		}
	}
	return Snapshot{
		Generation: generation,
		Rows:       rows,
		Metadata:   metadata,
		index:      index,
	}
}

// Index returns the row index for a stable file identity.
func (s Snapshot) Index(id RowID) (int, bool) {
	i, ok := s.index[id]
	return i, ok
}

// VisibleRows returns a clamped view into the snapshot's backing row slice.
func (s Snapshot) VisibleRows(start, count int) []Row {
	if count <= 0 || start >= len(s.Rows) {
		return nil
	}
	if start < 0 {
		start = 0
	}
	end := start + count
	if end > len(s.Rows) {
		end = len(s.Rows)
	}
	return s.Rows[start:end]
}
