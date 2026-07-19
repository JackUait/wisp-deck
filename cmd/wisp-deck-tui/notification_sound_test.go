package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNotificationSoundConfiguredPreferenceUsesValidatedNameUnderCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-features.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"sound":true,"sound_name":"Glass"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var names []string
	err := withConfiguredNotificationSound(path, func(name string) error {
		names = append(names, name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "Glass" {
		t.Fatalf("validated callback names = %#v, want [Glass]", names)
	}
}

func TestNotificationSoundCommandFactoryReadsCanonicalPreference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "features.json")
	tests := []struct {
		name    string
		content string
		write   bool
		want    []string
	}{
		{name: "missing"},
		{name: "invalid", content: `{"sound":true`, write: true},
		{name: "off", content: `{"sound":false,"sound_name":"Glass"}`, write: true},
		{name: "missing name defaults", content: `{"sound":true}`, write: true, want: []string{"Bottle"}},
		{name: "empty name defaults", content: `{"sound":true,"sound_name":""}`, write: true, want: []string{"Bottle"}},
		{name: "on", content: `{"sound":true,"sound_name":"Ping"}`, write: true, want: []string{"Ping"}},
		{name: "unsafe falls back", content: `{"sound":true,"sound_name":"../../private"}`, write: true, want: []string{"Bottle"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_ = os.Remove(path)
			if test.write {
				if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			command := newNotificationSoundCommand(func(name string) error {
				got = append(got, name)
				return nil
			})
			command.SetArgs([]string{"--features-file", path})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("callback names = %#v, want %#v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("callback names = %#v, want %#v", got, test.want)
				}
			}
		})
	}
}

func TestNotificationSoundCommandIsHiddenAndRequiresFeaturesFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var calls int
	command := newNotificationSoundCommand(func(string) error {
		calls++
		return nil
	})
	if !command.Hidden {
		t.Fatal("notification-sound command must remain hidden")
	}
	if command.Use != "notification-sound --features-file PATH" {
		t.Fatalf("Use = %q", command.Use)
	}
	command.SetArgs(nil)
	if err := command.Execute(); err == nil {
		t.Fatal("missing --features-file unexpectedly succeeded")
	}
	if calls != 0 {
		t.Fatalf("missing required flag reached callback %d times", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); !os.IsNotExist(err) {
		t.Fatalf("missing required flag reached lock creation: %v", err)
	}
}
