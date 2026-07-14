package soundpref

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadRequiresExplicitValidOptIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "features.json")
	tests := []struct {
		name, content, want string
	}{
		{name: "missing", want: ""},
		{name: "invalid", content: `{"sound":true`, want: ""},
		{name: "NaN", content: `{"sound":true,"other":NaN}`, want: ""},
		{name: "trailing garbage", content: `{"sound":true}x`, want: ""},
		{name: "duplicate flag", content: `{"sound":false,"sound":true,"sound_name":"Glass"}`, want: ""},
		{name: "case variant", content: `{"Sound":true,"sound_name":"Glass"}`, want: ""},
		{name: "mixed exact and case variant", content: `{"sound":true,"Sound":false,"sound_name":"Glass"}`, want: ""},
		{name: "missing flag", content: `{"sound_name":"Glass"}`, want: ""},
		{name: "off", content: `{"sound":false,"sound_name":"Glass"}`, want: ""},
		{name: "default", content: `{"sound":true}`, want: "Bottle"},
		{name: "allowed", content: `{"sound":true,"sound_name":"Glass"}`, want: "Glass"},
		{name: "non-string name", content: `{"sound":true,"sound_name":123}`, want: ""},
		{name: "unsafe", content: `{"sound":true,"sound_name":"../../private"}`, want: "Bottle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Remove(path)
			if tt.content != "" {
				if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := Read(path); got != tt.want {
				t.Fatalf("Read() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadRejectsMalformedUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "features.json")
	content := append([]byte(`{"sound":true,"padding":"`), 0xff)
	content = append(content, []byte(`"}`)...)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if got := Read(path); got != "" {
		t.Fatalf("malformed UTF-8 preference played %q, want silence", got)
	}
}

func TestReadRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "features.json")
	content := `{"sound":true,"padding":"` + strings.Repeat("x", 64*1024) + `"}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if got := Read(path); got != "" {
		t.Fatalf("oversized preference played %q, want silence", got)
	}
}

func TestReadDoesNotBlockOnFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "features.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() { done <- Read(path) }()
	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("FIFO preference played %q, want silence", got)
		}
	case <-time.After(200 * time.Millisecond):
		// Unblock a legacy blocking open before failing, so the test leaks no
		// goroutine or descriptor during its RED phase.
		writer, err := os.OpenFile(path, os.O_WRONLY, 0600)
		if err == nil {
			_ = writer.Close()
		}
		<-done
		t.Fatal("preference reader blocked on a FIFO")
	}
}
