package main

import "testing"

// diff-view gains an image mode: --image switches the stdin bytes from a diff
// body to raw image data, and --status supplies the header badge (a binary
// body can't be classified from its content).
func TestDiffViewCmd_has_image_flag(t *testing.T) {
	if diffViewCmd.Flags().Lookup("image") == nil {
		t.Fatal("expected --image flag on diff-view")
	}
}

func TestDiffViewCmd_has_status_flag(t *testing.T) {
	if diffViewCmd.Flags().Lookup("status") == nil {
		t.Fatal("expected --status flag on diff-view")
	}
}
