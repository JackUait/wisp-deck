package models_test

import (
	"regexp"
	"testing"

	"github.com/jackuait/wisp-deck/internal/models"
)

// wordsPattern matches four lowercase words joined by dashes,
// e.g. "foxtrot-people-stop-sexy".
var wordsPattern = regexp.MustCompile(`^[a-z]+(-[a-z]+){3}$`)

func TestRandomWorktreeName_FourDashJoinedLowercaseWords(t *testing.T) {
	for i := 0; i < 50; i++ {
		name := models.RandomWorktreeName()
		if !wordsPattern.MatchString(name) {
			t.Fatalf("RandomWorktreeName() = %q, want four dash-joined lowercase words", name)
		}
	}
}

func TestRandomWorktreeName_VariesAcrossCalls(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		seen[models.RandomWorktreeName()] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected varied names across 20 calls, got %d unique", len(seen))
	}
}
