package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// remoteControlKey is Claude Code's own setting. Claude reads a true from user
// settings only, so the row writes the shared ~/.claude/settings.json.
const remoteControlKey = "remoteControlAtStartup"

// readRemoteControl reports whether the settings file turns auto-connect on.
// A missing or unreadable file reads as off.
func readRemoteControl(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var s struct {
		On *bool `json:"remoteControlAtStartup"`
	}
	if json.Unmarshal(data, &s) != nil || s.On == nil {
		return false
	}
	return *s.On
}

// writeRemoteControl sets the key and leaves every other byte of the file as
// it was. It edits the text instead of re-encoding the whole object: Go's
// encoder would escape the & and > in hook and status line commands.
func writeRemoteControl(path string, on bool) error {
	value := "false"
	if on {
		value = "true"
	}
	target := path
	mode := os.FileMode(0o600)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if _, lerr := os.Lstat(path); lerr == nil {
			return fmt.Errorf("%s is a dangling link", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return writeFileAtomic(path, []byte("{\n  \""+remoteControlKey+"\": "+value+"\n}\n"), mode)
	case err != nil:
		return err
	}
	// Accounts link settings.json here; renaming onto the link would cut it.
	if target, err = filepath.EvalSymlinks(path); err != nil {
		return err
	}
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}

	var before map[string]json.RawMessage
	if err := json.Unmarshal(data, &before); err != nil {
		return fmt.Errorf("%s is not a JSON object: %w", path, err)
	}
	out, err := setTopLevelValue(data, len(before), value)
	if err != nil {
		return err
	}

	var after map[string]json.RawMessage
	if err := json.Unmarshal(out, &after); err != nil {
		return err
	}
	if string(after[remoteControlKey]) != value {
		return errors.New("edit did not set " + remoteControlKey)
	}
	for k, v := range before {
		if k != remoteControlKey && !bytes.Equal(after[k], v) {
			return errors.New("edit changed " + k)
		}
	}
	if len(after) != len(before) && len(after) != len(before)+1 {
		return errors.New("edit changed the set of keys")
	}
	return writeFileAtomic(target, out, mode)
}

// setTopLevelValue replaces the top-level key's value in place, or adds the
// key right after the opening brace when it is absent.
func setTopLevelValue(data []byte, keys int, value string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	open := int(dec.InputOffset())
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if tok != remoteControlKey {
			continue
		}
		end := int(dec.InputOffset())
		start := end - len(raw)
		if start < 0 || !bytes.Equal(data[start:end], raw) {
			return nil, errors.New("could not locate " + remoteControlKey)
		}
		return concat(data[:start], []byte(value), data[end:]), nil
	}
	if keys == 0 {
		return []byte("{\n  \"" + remoteControlKey + "\": " + value + "\n}\n"), nil
	}
	return concat(data[:open], []byte("\n  \""+remoteControlKey+"\": "+value+","), data[open:]), nil
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
