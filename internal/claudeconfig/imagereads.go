package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// imageReadExtensions names every file extension whose Read returns a block a
// text-only endpoint cannot accept. Images become `image` blocks; a PDF becomes
// a `document` block, and when Claude Code falls back to per-page extraction,
// image blocks as well. Over-denying a binary format costs a text-only model
// nothing, so the list is deliberately a superset of what Claude Code decodes
// today.
//
// SVG is absent on purpose: it is text, git tracks it as text and Claude Code
// reads it as text, so denying it would remove a working capability instead of
// preventing a failure.
var imageReadExtensions = []string{
	"png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff",
	"ico", "icns", "heic", "heif", "avif", "pdf",
}

// ImageReadDenyRules returns the permission rules that stop Claude Code turning
// a file into an image or document block.
//
// The `//` prefix is load-bearing: a Read rule's path is gitignore-style and
// relative to the project unless it is rooted at the filesystem, and the images
// that reach a model live outside the project — a screenshot directory, the
// Desktop, a temp path. Verified against a live pane on 2.1.247: a rooted rule
// denies /tmp, an absolute scratch path and an uppercase extension alike, in
// bypassPermissions mode, and a Task subagent inherits the denial.
func ImageReadDenyRules() []string {
	rules := make([]string, 0, len(imageReadExtensions))
	for _, ext := range imageReadExtensions {
		rules = append(rules, "Read(//**/*."+ext+")")
	}
	return rules
}

// ReadImagesBlocked reports whether a profile stops images reaching its model.
//
// Any owned rule counts as engaged, not all of them: a profile stamped by an
// older version carries a shorter list, and reading that as off would show the
// toggle unset on a session that is already blocking images. Turning it on
// again fills the list in.
func ReadImagesBlocked(configsDir, file string) bool {
	deny := readDenyList(filepath.Join(configsDir, file))
	owned := make(map[string]bool, len(imageReadExtensions))
	for _, rule := range ImageReadDenyRules() {
		owned[rule] = true
	}
	for _, rule := range deny {
		if owned[rule] {
			return true
		}
	}
	return false
}

// WriteImagesBlocked declares, or withdraws, the image deny rules on a profile.
//
// Claude Code has no "this model is text-only" switch of its own — no env var,
// no setting, and no per-model vision capability it consults — so blocking the
// reads that produce image blocks is the only lever a settings profile has.
// This is never stamped automatically the way the byte watchdog is: a
// self-hosted endpoint may serve a model that sees images perfectly well, so
// only the user turns it on.
//
// Withdrawing removes exactly the rules this owns. A deny rule the user wrote
// by hand is theirs, and dropping it would hand the model a file they meant to
// keep away from it.
func WriteImagesBlocked(configsDir, file string, blocked bool) error {
	path := filepath.Join(configsDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return err
	}

	owned := make(map[string]bool, len(imageReadExtensions))
	for _, rule := range ImageReadDenyRules() {
		owned[rule] = true
	}
	kept := make([]any, 0, len(settings))
	for _, rule := range readDenyList(path) {
		if !owned[rule] {
			kept = append(kept, rule)
		}
	}
	if blocked {
		for _, rule := range ImageReadDenyRules() {
			kept = append(kept, rule)
		}
	}

	permissions, _ := settings["permissions"].(map[string]any)
	if len(kept) == 0 {
		// An empty permissions object is not the same as none: leave a profile
		// that never declared any exactly as it was.
		if permissions == nil {
			return nil
		}
		delete(permissions, "deny")
		if len(permissions) == 0 {
			delete(settings, "permissions")
		}
	} else {
		if permissions == nil {
			permissions = make(map[string]any)
		}
		permissions["deny"] = kept
		settings["permissions"] = permissions
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return writeSecure(path, append(out, '\n'))
}

// readDenyList returns a profile's permission deny rules. A missing file,
// invalid JSON, or an absent permissions section all read as none.
func readDenyList(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var settings struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return nil
	}
	return settings.Permissions.Deny
}
