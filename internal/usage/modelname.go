package usage

import (
	"strings"
	"unicode"
)

// acronymFamilies are family tokens the vendor writes in full caps. They cannot
// be detected by shape — "gpt" and "sol" are both three lowercase letters, but
// only the first is an acronym — so they are listed. A family named here is also
// joined to its version by a hyphen ("GPT-5.6") rather than a space, matching how
// the vendor writes it; word families take a space ("Opus 4.8").
var acronymFamilies = map[string]bool{"gpt": true, "glm": true}

// DisplayModelName turns a raw model id into the name its vendor writes, for
// display in the Stats tab: "claude-opus-4-8" → "Opus 4.8", "gpt-5.6-sol" →
// "GPT-5.6 Sol", "claude-haiku-4-5-20251001" → "Haiku 4.5".
//
// The id is split on "-" and each part classified as a version number or a word.
// All the version parts join with "." — which folds the dashed ("opus-4-8") and
// dotted ("opus-4.5") forms Anthropic uses interchangeably into one shape — and
// the words supply the family plus any variant suffix. Because the family is the
// first *word* rather than the first part, the legacy id order
// ("claude-3-5-sonnet") resolves to "Sonnet 3.5" too.
//
// An id that yields no words is returned unchanged, so a non-model row such as
// Claude Code's "<synthetic>" passes through rather than being mangled into a
// plausible-looking name.
func DisplayModelName(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return id
	}
	// The vendor prefix is noise once the family name is shown.
	s := strings.TrimPrefix(trimmed, "claude-")

	var versions, words []string
	for _, part := range strings.Split(s, "-") {
		if part == "" {
			continue
		}
		switch {
		case isReleaseDate(part):
			// A build stamp, not part of the name.
		case isVersion(part):
			versions = append(versions, part)
		default:
			words = append(words, part)
		}
	}
	if len(words) == 0 {
		return id
	}

	family := words[0]
	acronym := acronymFamilies[family]
	name := displayToken(family)
	if version := strings.Join(versions, "."); version != "" {
		if acronym {
			name += "-" + version
		} else {
			name += " " + version
		}
	}
	for _, w := range words[1:] {
		name += " " + displayToken(w)
	}
	return name
}

// displayToken renders one word of a model name: a listed acronym or a token
// mixing letters and digits ("k3", "256k") goes to full caps, anything else is
// capitalised.
func displayToken(w string) string {
	if acronymFamilies[w] || hasLetterAndDigit(w) {
		return strings.ToUpper(w)
	}
	r := []rune(w)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// isVersion reports whether a part is a version number — digits, optionally with
// dots between them ("5", "4.5"). Used to separate "4"/"5" from "opus"/"codex".
func isVersion(s string) bool {
	digits := false
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits = true
		case r == '.':
		default:
			return false
		}
	}
	return digits
}

// isReleaseDate reports whether a part is an 8-digit release stamp such as
// "20251001", which identifies a build rather than naming the model.
func isReleaseDate(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// hasLetterAndDigit reports whether a token mixes letters and digits, the shape
// of a short designator ("k3", "256k") that reads as an acronym in full caps.
func hasLetterAndDigit(s string) bool {
	var letter, digit bool
	for _, r := range s {
		if unicode.IsLetter(r) {
			letter = true
		}
		if unicode.IsDigit(r) {
			digit = true
		}
	}
	return letter && digit
}
