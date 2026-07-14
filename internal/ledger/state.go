package ledger

// State contains the small mutable interaction state layered over a snapshot.
type State struct {
	Snapshot     Snapshot
	Width        int
	Height       int
	HeaderHeight int
	FooterHeight int
	Scroll       int
	Hovered      RowID
	Selected     map[string]struct{}
}

// NewState creates interaction state for a snapshot.
func NewState(snapshot Snapshot) *State {
	return &State{
		Snapshot: snapshot,
		Selected: make(map[string]struct{}),
	}
}

// Resize updates terminal geometry and clamps the current viewport.
func (s *State) Resize(width, height, headerHeight, footerHeight int) {
	s.Width = width
	s.Height = height
	s.HeaderHeight = headerHeight
	s.FooterHeight = footerHeight
	s.ScrollTo(s.Scroll)
}

// ViewportHeight is the number of rows available to scrolling content.
func (s *State) ViewportHeight() int {
	height := s.Height - s.HeaderHeight - s.FooterHeight
	if height < 1 {
		return 1
	}
	return height
}

// MaxScroll returns the largest valid viewport offset.
func (s *State) MaxScroll() int {
	maxScroll := len(s.Snapshot.Rows) - s.ViewportHeight()
	if maxScroll < 0 {
		return 0
	}
	return maxScroll
}

// ScrollTo moves to a clamped absolute viewport offset.
func (s *State) ScrollTo(offset int) {
	if offset < 0 {
		offset = 0
	}
	if maxScroll := s.MaxScroll(); offset > maxScroll {
		offset = maxScroll
	}
	s.Scroll = offset
}

// ScrollBy moves by a number of rows.
func (s *State) ScrollBy(delta int) {
	s.ScrollTo(s.Scroll + delta)
}

// PageBy moves by a number of whole viewports.
func (s *State) PageBy(pages int) {
	s.ScrollBy(pages * s.ViewportHeight())
}

// HoverScreenRow maps a one-based terminal row directly into the viewport.
// It reports whether the stable hovered identity changed.
func (s *State) HoverScreenRow(screenRow int) bool {
	viewportOffset := screenRow - s.HeaderHeight - 1
	next := RowID{}
	if viewportOffset >= 0 && viewportOffset < s.ViewportHeight() {
		index := s.Scroll + viewportOffset
		if index >= 0 && index < len(s.Snapshot.Rows) {
			row := s.Snapshot.Rows[index]
			if row.Kind == RowFile {
				next = row.ID
			}
		}
	}
	if next == s.Hovered {
		return false
	}
	s.Hovered = next
	return true
}

// ToggleSelected toggles a path in the selection set.
func (s *State) ToggleSelected(path string) {
	if path == "" {
		return
	}
	if _, ok := s.Selected[path]; ok {
		delete(s.Selected, path)
		return
	}
	s.Selected[path] = struct{}{}
}

// IsSelected reports whether a path is selected.
func (s *State) IsSelected(path string) bool {
	_, ok := s.Selected[path]
	return ok
}

// ReplaceSnapshot installs a new immutable generation and reconciles identity-
// based interaction state. This refresh-time operation may inspect all new rows;
// hover and scroll operations never do.
func (s *State) ReplaceSnapshot(snapshot Snapshot) {
	anchor := RowID{}
	if s.Scroll >= 0 && s.Scroll < len(s.Snapshot.Rows) {
		row := s.Snapshot.Rows[s.Scroll]
		if row.Kind == RowFile {
			anchor = row.ID
		}
	}

	s.Snapshot = snapshot
	if anchor != (RowID{}) {
		if index, ok := snapshot.Index(anchor); ok {
			s.Scroll = index
		}
	}
	s.ScrollTo(s.Scroll)

	if s.Hovered != (RowID{}) {
		if _, ok := snapshot.Index(s.Hovered); !ok {
			s.Hovered = RowID{}
		}
	}

	validPaths := make(map[string]struct{}, snapshot.Metadata.TotalFiles)
	for _, row := range snapshot.Rows {
		if row.Kind == RowFile {
			validPaths[row.Path] = struct{}{}
		}
	}
	for path := range s.Selected {
		if _, ok := validPaths[path]; !ok {
			delete(s.Selected, path)
		}
	}
}

// VisibleRows returns the current viewport without copying snapshot rows.
func (s *State) VisibleRows() []Row {
	return s.Snapshot.VisibleRows(s.Scroll, s.ViewportHeight())
}
