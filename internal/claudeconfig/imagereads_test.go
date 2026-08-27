package claudeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDenyRules(t *testing.T, dir, file string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var settings struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return settings.Permissions.Deny
}

func hasRule(rules []string, want string) bool {
	for _, rule := range rules {
		if rule == want {
			return true
		}
	}
	return false
}

// A relative Read rule scopes to the project. The images that reach a text-only
// model come from everywhere else — a screenshot directory, the Desktop, a temp
// path — so every rule has to be rooted at the filesystem.
func TestImageReadDenyRules_are_rooted_at_the_filesystem(t *testing.T) {
	rules := ImageReadDenyRules()
	if len(rules) == 0 {
		t.Fatal("no deny rules declared")
	}
	for _, rule := range rules {
		if !strings.HasPrefix(rule, "Read(//") {
			t.Errorf("rule %q is not rooted at the filesystem", rule)
		}
		if !strings.HasSuffix(rule, ")") {
			t.Errorf("rule %q is not a well-formed permission rule", rule)
		}
	}
}

// Over-denying a binary format costs a text-only model nothing; missing one
// kills its next turn. A PDF earns its place because Claude Code returns the
// file as a document block, which a text-only endpoint refuses just as hard.
func TestImageReadDenyRules_cover_every_format_read_returns_as_a_block(t *testing.T) {
	rules := ImageReadDenyRules()
	for _, ext := range []string{
		"png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff",
		"ico", "icns", "heic", "heif", "avif", "pdf",
	} {
		if !hasRule(rules, "Read(//**/*."+ext+")") {
			t.Errorf("no deny rule for .%s", ext)
		}
	}
	// git tracks an SVG as text and Claude Code reads it as text, so denying it
	// would remove a working capability rather than prevent a failure.
	if hasRule(rules, "Read(//**/*.svg)") {
		t.Error("SVG is read as text and must stay readable")
	}
}

func TestWriteImagesBlocked_declares_every_rule(t *testing.T) {
	dir, _, file := customConfig(t)

	if err := WriteImagesBlocked(dir, file, true); err != nil {
		t.Fatalf("WriteImagesBlocked: %v", err)
	}

	rules := readDenyRules(t, dir, file)
	for _, want := range ImageReadDenyRules() {
		if !hasRule(rules, want) {
			t.Errorf("profile is missing %q; deny = %v", want, rules)
		}
	}
	if !ReadImagesBlocked(dir, file) {
		t.Error("ReadImagesBlocked reports off after writing the rules")
	}
}

// The profile carries the endpoint, the model and the window. Rewriting the
// permissions must not disturb any of them.
func TestWriteImagesBlocked_keeps_the_rest_of_the_profile(t *testing.T) {
	dir, _, file := customConfig(t)
	if err := WriteCustomEndpoint(dir, file, "http://127.0.0.1:8000"); err != nil {
		t.Fatalf("WriteCustomEndpoint: %v", err)
	}
	if err := WriteCustomModel(dir, file, "Qwen-3.8"); err != nil {
		t.Fatalf("WriteCustomModel: %v", err)
	}

	if err := WriteImagesBlocked(dir, file, true); err != nil {
		t.Fatalf("WriteImagesBlocked: %v", err)
	}

	env := readEnv(t, dir, file)
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8000" {
		t.Errorf("endpoint = %v, want it untouched", env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "Qwen-3.8" {
		t.Errorf("opus model = %v, want it untouched", env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
}

// Every launch path may write the profile again, so a second write must leave
// the file exactly as the first one did.
func TestWriteImagesBlocked_writes_each_rule_once(t *testing.T) {
	dir, _, file := customConfig(t)
	if err := WriteImagesBlocked(dir, file, true); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteImagesBlocked(dir, file, true); err != nil {
		t.Fatalf("second write: %v", err)
	}

	rules := readDenyRules(t, dir, file)
	if len(rules) != len(ImageReadDenyRules()) {
		t.Fatalf("deny = %v, want %d rules", rules, len(ImageReadDenyRules()))
	}
}

// Turning the toggle off removes only what the toggle put there. A deny rule
// the user wrote by hand is theirs, and silently dropping it hands the model a
// file the user meant to keep away from it.
func TestWriteImagesBlocked_off_keeps_rules_it_does_not_own(t *testing.T) {
	dir, _, file := customConfig(t)
	path := filepath.Join(dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	settings["permissions"] = map[string]any{"deny": []any{"Read(//**/secrets/**)"}}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	if err := WriteImagesBlocked(dir, file, true); err != nil {
		t.Fatalf("on: %v", err)
	}
	if err := WriteImagesBlocked(dir, file, false); err != nil {
		t.Fatalf("off: %v", err)
	}

	rules := readDenyRules(t, dir, file)
	if !hasRule(rules, "Read(//**/secrets/**)") {
		t.Errorf("deny = %v, want the user's own rule kept", rules)
	}
	for _, owned := range ImageReadDenyRules() {
		if hasRule(rules, owned) {
			t.Errorf("deny = %v, still carries %q", rules, owned)
		}
	}
	if ReadImagesBlocked(dir, file) {
		t.Error("ReadImagesBlocked reports on after clearing the rules")
	}
}

// A profile written by an older version carries a shorter list. Reporting it as
// off would make the toggle read as unset on a session that is already blocking
// images, so any owned rule counts as engaged — and turning it on again fills
// the list in.
func TestReadImagesBlocked_reports_on_for_a_partially_stamped_profile(t *testing.T) {
	dir, _, file := customConfig(t)
	path := filepath.Join(dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	settings["permissions"] = map[string]any{"deny": []any{"Read(//**/*.png)"}}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	if !ReadImagesBlocked(dir, file) {
		t.Fatal("a profile carrying an owned rule must read as on")
	}
	if err := WriteImagesBlocked(dir, file, true); err != nil {
		t.Fatalf("WriteImagesBlocked: %v", err)
	}
	rules := readDenyRules(t, dir, file)
	for _, want := range ImageReadDenyRules() {
		if !hasRule(rules, want) {
			t.Errorf("profile is missing %q after a re-write; deny = %v", want, rules)
		}
	}
}

// The default is off. Some self-hosted models see images perfectly well, so
// this is never stamped for the user the way the byte watchdog is.
func TestReadImagesBlocked_is_off_on_a_fresh_profile(t *testing.T) {
	dir, _, file := customConfig(t)
	if ReadImagesBlocked(dir, file) {
		t.Error("a fresh profile must allow images")
	}
	if rules := readDenyRules(t, dir, file); len(rules) != 0 {
		t.Errorf("a fresh profile declares deny rules: %v", rules)
	}
}

// Turning it off on a profile that never had it on must not leave an empty
// permissions object behind.
func TestWriteImagesBlocked_off_leaves_an_untouched_profile_alone(t *testing.T) {
	dir, _, file := customConfig(t)
	before, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteImagesBlocked(dir, file, false); err != nil {
		t.Fatalf("WriteImagesBlocked: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("profile changed:\nbefore %s\nafter  %s", before, after)
	}
}
