package models

import (
	"math/rand/v2"
	"strings"
)

// Word pools for RandomWorktreeName. The nato-noun-verb-adjective shape gives
// memorable, git-branch-safe names like "foxtrot-people-stop-sexy".
var (
	natoWords = []string{
		"alfa", "bravo", "charlie", "delta", "echo", "foxtrot", "golf",
		"hotel", "india", "juliett", "kilo", "lima", "mike", "november",
		"oscar", "papa", "quebec", "romeo", "sierra", "tango", "uniform",
		"victor", "whiskey", "xray", "yankee", "zulu",
	}
	nounWords = []string{
		"people", "river", "mountain", "engine", "garden", "harbor", "island",
		"jungle", "kitten", "lantern", "meadow", "needle", "ocean", "pillow",
		"quartz", "rocket", "shadow", "temple", "umbrella", "valley", "window",
		"yogurt", "zebra", "anchor", "bottle", "candle", "dragon", "feather",
	}
	verbWords = []string{
		"stop", "jump", "swim", "climb", "dance", "float", "glide", "hover",
		"launch", "march", "orbit", "paint", "race", "sing", "travel", "vault",
		"wander", "zoom", "bounce", "carry", "drift", "explore", "gather",
	}
	adjectiveWords = []string{
		"sexy", "brave", "calm", "daring", "eager", "fancy", "gentle", "happy",
		"icy", "jolly", "keen", "lucky", "mellow", "noble", "proud", "quiet",
		"rapid", "shiny", "tidy", "vivid", "witty", "zesty", "bold", "cozy",
	}
)

// RandomWorktreeName returns a random four-word dash-joined name suitable for
// a git branch and worktree directory, e.g. "foxtrot-people-stop-sexy".
func RandomWorktreeName() string {
	return strings.Join([]string{
		natoWords[rand.IntN(len(natoWords))],
		nounWords[rand.IntN(len(nounWords))],
		verbWords[rand.IntN(len(verbWords))],
		adjectiveWords[rand.IntN(len(adjectiveWords))],
	}, "-")
}
