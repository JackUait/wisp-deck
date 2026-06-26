package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestPersistSetting_concurrentReadNeverEmpty reproduces the loader/menu race:
// the menu writes the settings file (persistSetting) while the loader, a
// SEPARATE process, reads `theme=` from it. A non-atomic truncate-then-write
// leaves a window where a reader sees an empty/partial file, so the loader
// falls back to the tool default color (which looks like the "old" color). The
// write must be atomic so every read observes a complete theme= line.
func TestPersistSetting_concurrentReadNeverEmpty(t *testing.T) {
	dir := t.TempDir()
	sf := filepath.Join(dir, "settings")
	if err := os.WriteFile(sf, []byte("ghost_display=animated\ntab_title=full\npanel_mode=compact\ntheme=auto\n"), 0644); err != nil {
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
