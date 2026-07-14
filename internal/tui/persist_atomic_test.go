package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Sound playback reads this JSON from separate watcher/notifier processes. The
// menu must replace it atomically so readers always observe a complete choice.
func TestPersistSound_concurrentReadAlwaysValid(t *testing.T) {
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "claude-features.json")
	initial, err := json.Marshal(map[string]interface{}{
		"sound":   false,
		"padding": strings.Repeat("x", 4096),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(soundFile, append(initial, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSoundFile(soundFile)

	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				m.soundName = "Glass"
			} else {
				m.soundName = ""
			}
			m.persistSound()
		}
	}()

	var bad int64
	var mu sync.Mutex
	var readers sync.WaitGroup
	for r := 0; r < 6; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 5000; i++ {
				data, err := os.ReadFile(soundFile)
				if err != nil {
					mu.Lock()
					bad++
					mu.Unlock()
					continue
				}
				var document map[string]interface{}
				if err := json.Unmarshal(data, &document); err != nil {
					mu.Lock()
					bad++
					mu.Unlock()
					continue
				}
				if _, ok := document["sound"].(bool); !ok {
					mu.Lock()
					bad++
					mu.Unlock()
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	writer.Wait()

	if bad > 0 {
		t.Fatalf("sound reader observed invalid/partial JSON %d times", bad)
	}
}

func TestPersistSound_waits_for_inflight_notification_lock(t *testing.T) {
	if _, err := os.Stat("/usr/bin/lockf"); err != nil {
		t.Skip("macOS lockf is required for the cross-process sound lock")
	}
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "claude-features.json")
	if err := os.WriteFile(soundFile, []byte(`{"sound":true,"sound_name":"Glass"}`), 0644); err != nil {
		t.Fatal(err)
	}
	lockFile := filepath.Join(dir, ".claude-features.json.lock")
	held := filepath.Join(dir, "held")
	release := filepath.Join(dir, "release")
	command := fmt.Sprintf(`touch %q; while [ ! -e %q ]; do sleep 0.01; done`, held, release)
	locker := exec.Command("/usr/bin/lockf", "-k", lockFile, "/bin/sh", "-c", command)
	if err := locker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(release, nil, 0644)
		_ = locker.Wait()
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(held); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lock holder did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSoundFile(soundFile)
	m.soundName = ""
	done := make(chan struct{})
	go func() {
		m.persistSound()
		close(done)
	}()

	completedEarly := false
	select {
	case <-done:
		completedEarly = true
	case <-time.After(200 * time.Millisecond):
	}
	if err := os.WriteFile(release, nil, 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sound preference write did not continue after notification released the lock")
	}
	if completedEarly {
		t.Fatal("Off persisted while an in-flight notification still held the sound lock")
	}
}

func TestPersistSound_does_not_block_reading_fifo(t *testing.T) {
	dir := t.TempDir()
	soundFile := filepath.Join(dir, "claude-features.json")
	if err := syscall.Mkfifo(soundFile, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.soundFile = soundFile
	m.soundConfigDir = dir
	m.soundName = ""
	done := make(chan bool, 1)
	go func() { done <- m.persistSound() }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("Off write failed after safely replacing FIFO preference")
		}
	case <-time.After(300 * time.Millisecond):
		writer, err := os.OpenFile(soundFile, os.O_WRONLY, 0600)
		if err == nil {
			_ = writer.Close()
		}
		<-done
		t.Fatal("sound writer blocked reading a FIFO while holding the preference lock")
	}
}

// TestPersistSetting_concurrentReadNeverEmpty reproduces the loader/menu race:
// the menu writes the settings file (persistSetting) while the loader, a
// SEPARATE process, reads `theme=` from it. A non-atomic truncate-then-write
// leaves a window where a reader sees an empty/partial file, so the loader
// falls back to the tool default color (which looks like the "old" color). The
// write must be atomic so every read observes a complete theme= line.
func TestPersistSetting_concurrentReadNeverEmpty(t *testing.T) {
	dir := t.TempDir()
	sf := filepath.Join(dir, "settings")
	if err := os.WriteFile(sf, []byte("ghost_display=animated\ntab_title=full\ntheme=auto\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewMainMenu(nil, []string{"claude"}, "claude", "none")
	m.SetSettingsFile(sf)

	colors := []string{"green", "blue", "rose", "cyan", "purple", "orange"}

	stop := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			m.persistSetting("theme", colors[i%len(colors)])
		}
	}()

	var bad int64
	var mu sync.Mutex
	var readers sync.WaitGroup
	for r := 0; r < 6; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 5000; i++ {
				data, err := os.ReadFile(sf)
				if err != nil {
					continue
				}
				hasTheme := false
				for _, line := range strings.Split(string(data), "\n") {
					if v, ok := strings.CutPrefix(line, "theme="); ok && strings.TrimSpace(v) != "" {
						hasTheme = true
						break
					}
				}
				if !hasTheme {
					mu.Lock()
					bad++
					mu.Unlock()
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	writers.Wait()

	if bad > 0 {
		t.Fatalf("loader saw an empty/partial theme line %d times during concurrent menu writes (non-atomic write race)", bad)
	}
}
