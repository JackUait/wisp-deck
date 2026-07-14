package ledger

import (
	"bytes"
	"fmt"
	"strconv"
)

// Change is one path record from Git's NUL-delimited numstat output.
type Change struct {
	Group    Group
	Path     string
	OldPath  string
	Added    int
	Deleted  int
	Binary   bool
	OldBytes int64
	NewBytes int64
}

// parseNumstatZ parses `git diff --numstat -z` output. Ordinary records are
// "added<TAB>deleted<TAB>path<NUL>". Rename/copy records leave that path field
// empty and follow it with old-path<NUL>new-path<NUL>.
func parseNumstatZ(raw []byte, group Group) ([]Change, error) {
	var changes []Change
	for offset := 0; offset < len(raw); {
		header, next, err := readNULField(raw, offset)
		if err != nil {
			return nil, fmt.Errorf("numstat record at byte %d: %w", offset, err)
		}
		offset = next

		firstTab := bytes.IndexByte(header, '\t')
		if firstTab < 0 {
			return nil, fmt.Errorf("numstat record at byte %d: missing added-count separator", offset-len(header)-1)
		}
		secondRel := bytes.IndexByte(header[firstTab+1:], '\t')
		if secondRel < 0 {
			return nil, fmt.Errorf("numstat record at byte %d: missing deleted-count separator", offset-len(header)-1)
		}
		secondTab := firstTab + 1 + secondRel

		added, addedBinary, err := parseNumstatCount(header[:firstTab])
		if err != nil {
			return nil, fmt.Errorf("numstat added count: %w", err)
		}
		deleted, deletedBinary, err := parseNumstatCount(header[firstTab+1 : secondTab])
		if err != nil {
			return nil, fmt.Errorf("numstat deleted count: %w", err)
		}

		path := header[secondTab+1:]
		change := Change{
			Group: group, Added: added, Deleted: deleted,
			Binary: addedBinary || deletedBinary,
		}
		if len(path) > 0 {
			change.Path = string(path)
		} else {
			oldPath, afterOld, err := readNULField(raw, offset)
			if err != nil {
				return nil, fmt.Errorf("numstat rename old path at byte %d: %w", offset, err)
			}
			newPath, afterNew, err := readNULField(raw, afterOld)
			if err != nil {
				return nil, fmt.Errorf("numstat rename new path at byte %d: %w", afterOld, err)
			}
			if len(oldPath) == 0 || len(newPath) == 0 {
				return nil, fmt.Errorf("numstat rename at byte %d has an empty path", offset)
			}
			change.OldPath = string(oldPath)
			change.Path = string(newPath)
			offset = afterNew
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func parseNumstatCount(raw []byte) (value int, binary bool, err error) {
	if bytes.Equal(raw, []byte{'-'}) {
		return 0, true, nil
	}
	if len(raw) == 0 {
		return 0, false, fmt.Errorf("empty count")
	}
	value, err = strconv.Atoi(string(raw))
	if err != nil || value < 0 {
		return 0, false, fmt.Errorf("invalid count %q", raw)
	}
	return value, false, nil
}

// parsePathListZ parses a sequence of NUL-terminated Git paths without treating
// tabs or newlines as delimiters.
func parsePathListZ(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("path list is missing its final NUL terminator")
	}
	fields := bytes.Split(raw[:len(raw)-1], []byte{0})
	paths := make([]string, 0, len(fields))
	for i, field := range fields {
		if len(field) == 0 {
			return nil, fmt.Errorf("path %d is empty", i)
		}
		paths = append(paths, string(field))
	}
	return paths, nil
}

func readNULField(raw []byte, offset int) ([]byte, int, error) {
	if offset < 0 || offset > len(raw) {
		return nil, offset, fmt.Errorf("invalid offset")
	}
	relativeEnd := bytes.IndexByte(raw[offset:], 0)
	if relativeEnd < 0 {
		return nil, offset, fmt.Errorf("missing NUL terminator")
	}
	end := offset + relativeEnd
	return raw[offset:end], end + 1, nil
}
