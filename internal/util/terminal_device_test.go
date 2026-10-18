package util

import (
	"errors"
	"testing"
)

// Bun's kqueue rejects /dev/tty on macOS, so a child that must own the
// terminal needs the real device the controlling terminal points at.
func TestDeviceWithRdev_picksTheMatchingTerminal(t *testing.T) {
	rdevs := map[string]uint64{"ttys030": 7, "ttys031": 9, "null": 9}
	rdevOf := func(name string) (uint64, error) {
		if r, ok := rdevs[name]; ok {
			return r, nil
		}
		return 0, errors.New("gone")
	}
	got, err := deviceWithRdev("/dev", []string{"null", "ttys030", "ttys031", "ttysgone"}, 9, rdevOf)
	if err != nil || got != "/dev/ttys031" {
		t.Fatalf("got %q, %v; want /dev/ttys031", got, err)
	}
	if _, err := deviceWithRdev("/dev", []string{"ttys030"}, 9, rdevOf); err == nil {
		t.Fatal("expected an error when no terminal matches")
	}
}
