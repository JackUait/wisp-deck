package util

import "testing"

func TestParseGitHubRepo_accepted_forms(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantURL  string
		wantName string
	}{
		{"https", "https://github.com/owner/repo", "https://github.com/owner/repo.git", "repo"},
		{"https with .git", "https://github.com/owner/repo.git", "https://github.com/owner/repo.git", "repo"},
		{"https trailing slash", "https://github.com/owner/repo/", "https://github.com/owner/repo.git", "repo"},
		{"https extra segments", "https://github.com/owner/repo/tree/main/docs", "https://github.com/owner/repo.git", "repo"},
		{"http", "http://github.com/owner/repo", "https://github.com/owner/repo.git", "repo"},
		{"www", "www.github.com/owner/repo", "https://github.com/owner/repo.git", "repo"},
		{"https www", "https://www.github.com/owner/repo", "https://github.com/owner/repo.git", "repo"},
		{"bare", "github.com/owner/repo", "https://github.com/owner/repo.git", "repo"},
		{"ssh", "git@github.com:owner/repo.git", "git@github.com:owner/repo.git", "repo"},
		{"ssh without .git", "git@github.com:owner/repo", "git@github.com:owner/repo.git", "repo"},
		{"hyphenated names", "https://github.com/my-org/my-repo.name", "https://github.com/my-org/my-repo.name.git", "my-repo.name"},
		{"surrounding whitespace", "  https://github.com/owner/repo  ", "https://github.com/owner/repo.git", "repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, name, ok := ParseGitHubRepo(tt.input)
			if !ok {
				t.Fatalf("ParseGitHubRepo(%q) not recognized, want ok", tt.input)
			}
			if url != tt.wantURL {
				t.Errorf("clone URL = %q, want %q", url, tt.wantURL)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

func TestParseGitHubRepo_rejected_forms(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"local path", "~/code/project"},
		{"absolute path", "/Users/me/github.com-notes"},
		{"relative path", "code/project"},
		{"other host", "https://gitlab.com/owner/repo"},
		{"github without repo", "https://github.com/owner"},
		{"github root", "https://github.com/"},
		{"ssh other host", "git@gitlab.com:owner/repo.git"},
		{"path containing github.com dir", "~/mirrors/github.com/owner/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, ok := ParseGitHubRepo(tt.input); ok {
				t.Errorf("ParseGitHubRepo(%q) recognized, want rejected", tt.input)
			}
		})
	}
}
