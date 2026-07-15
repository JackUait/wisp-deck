package util

import "strings"

// ParseGitHubRepo recognizes a GitHub repository reference pasted where a
// local path is expected. Accepted forms: https/http URLs (optionally with
// www., .git, trailing slash, or extra segments like /tree/main), bare
// github.com/owner/repo, and git@github.com:owner/repo SSH refs. SSH input
// keeps an SSH clone URL; everything else normalizes to https. Returns the
// clone URL, the repo name, and whether the input was recognized.
func ParseGitHubRepo(input string) (cloneURL, name string, ok bool) {
	s := strings.TrimSpace(input)

	if rest, found := strings.CutPrefix(s, "git@github.com:"); found {
		owner, repo, valid := splitOwnerRepo(rest)
		if !valid {
			return "", "", false
		}
		return "git@github.com:" + owner + "/" + repo + ".git", repo, true
	}

	for _, p := range []string{"https://", "http://"} {
		if rest, found := strings.CutPrefix(s, p); found {
			s = rest
			break
		}
	}
	s = strings.TrimPrefix(s, "www.")
	rest, found := strings.CutPrefix(s, "github.com/")
	if !found {
		return "", "", false
	}
	owner, repo, valid := splitOwnerRepo(rest)
	if !valid {
		return "", "", false
	}
	return "https://github.com/" + owner + "/" + repo + ".git", repo, true
}

// splitOwnerRepo extracts owner and repo from "owner/repo[.git][/...]".
func splitOwnerRepo(s string) (owner, repo string, ok bool) {
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner, repo = parts[0], strings.TrimSuffix(parts[1], ".git")
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}
