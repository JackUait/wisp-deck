// Package secrets_test guards the repository against committing a live vendor
// credential.
//
// This shipped once: the sk-kimi-… subscription key from a developer's own
// ~/.config/wisp-deck/claude-configs/moonshot-kimi.json was pasted into two
// tests as a "realistic" fixture and pushed to a public repo, where it sat for
// eleven days and rode into two release source archives. Nothing caught it —
// the repo has secret scanning disabled, and a credential in a _test.go file
// looks exactly like test data to a reviewer.
//
// So the shape of a real key is the thing that fails the build. A fixture only
// ever needs the vendor prefix to be recognizable; the long high-entropy tail
// after it carries no test value and is the entire secret.
package secrets_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// livePatterns match the credential shapes issued by the API-key providers in
// internal/claudeconfig's catalog. Each requires the long uninterrupted
// alphanumeric run that only a real key has, so a readable fixture like
// "sk-kimi-looks-like-coding-but-is-a-glm-profile" stays legal.
var livePatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	// Kimi For Coding: "sk-kimi-" + 64 alphanumerics.
	{"Kimi For Coding key", regexp.MustCompile(`sk-kimi-[A-Za-z0-9]{40,}`)},
	// Moonshot open platform / OpenAI-style: "sk-" + a long alphanumeric run.
	{"sk- style API key", regexp.MustCompile(`sk-[A-Za-z0-9]{40,}`)},
	// Xiaomi MiMo token-plan: "tp-" + 48 alphanumerics.
	{"MiMo token-plan key", regexp.MustCompile(`tp-[A-Za-z0-9]{40,}`)},
	// Zhipu / z.ai: 32 hex, a dot, then 16 alphanumerics.
	{"Zhipu / z.ai key", regexp.MustCompile(`\b[0-9a-f]{32}\.[A-Za-z0-9]{16}\b`)},
}

// skipDirs are trees with no hand-written source to protect.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"testdata":     true,
	"dist":         true,
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("secrets: could not locate repo root (no go.mod above cwd)")
		}
		dir = parent
	}
}

func TestNoLiveCredentialInTheWorkingTree(t *testing.T) {
	root := repoRoot(t)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// This file states the patterns it forbids; matching itself is not a leak.
		if path == thisFile(t, root) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || isBinary(data) {
			return nil
		}
		for _, p := range livePatterns {
			if loc := p.re.FindIndex(data); loc != nil {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s: looks like a live %s at byte %d (%s…).\n"+
					"A real credential must never be committed. Use a fixture whose tail "+
					"is words-and-dashes, not a high-entropy run.",
					rel, p.name, loc[0], redact(data[loc[0]:loc[1]]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// thisFile returns the absolute path of this test source, which legitimately
// contains the vendor prefixes it screens for.
func thisFile(t *testing.T, root string) string {
	t.Helper()
	return filepath.Join(root, "test", "internal", "secrets", "no_committed_credentials_test.go")
}

// redact keeps only enough of a match to locate it, never the secret itself —
// a failing CI log is a public artifact too.
func redact(match []byte) string {
	s := string(match)
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func isBinary(data []byte) bool {
	if len(data) > 512 {
		data = data[:512]
	}
	return strings.IndexByte(string(data), 0) >= 0
}
