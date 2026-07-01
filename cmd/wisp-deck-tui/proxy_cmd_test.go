package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProxyStartupJSON_reportsPortAndKey(t *testing.T) {
	out := proxyStartupJSON(54321, "secret-key")
	var got struct {
		Port int    `json:"port"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not valid JSON: %v (%q)", err, out)
	}
	if got.Port != 54321 || got.Key != "secret-key" {
		t.Errorf("got %+v", got)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("startup line should end with newline for line-buffered readers")
	}
}

func TestGenerateProxyKey_isNonEmptyAndPrefixed(t *testing.T) {
	k1 := generateProxyKey()
	k2 := generateProxyKey()
	if k1 == "" || k2 == "" {
		t.Fatal("key must not be empty")
	}
	if k1 == k2 {
		t.Error("keys should be random per call")
	}
	if !strings.HasPrefix(k1, "wd-") {
		t.Errorf("key should be prefixed wd-, got %q", k1)
	}
}
