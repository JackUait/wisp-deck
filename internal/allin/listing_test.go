package allin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFetchListing_reads_the_anthropic_shape_across_pages(t *testing.T) {
	var sawAuth, sawVersion, sawAfter string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawVersion = r.Header.Get("anthropic-version")
		if after := r.URL.Query().Get("after_id"); after != "" {
			sawAfter = after
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-haiku-4-5-20251001","display_name":"Claude Haiku 4.5","created_at":"2025-10-15T00:00:00Z"}],"has_more":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5","created_at":"2026-09-21T16:24:00Z"}],"has_more":true,"last_id":"claude-opus-5-5"}`))
	}))
	defer server.Close()

	got, err := FetchListing(server.Client(), server.URL+"/v1/models", http.Header{"Authorization": {"Bearer tok"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(got), []string{"claude-opus-5-5", "claude-haiku-4-5-20251001"}) {
		t.Fatalf("ids = %v", ids(got))
	}
	if got[0].Label != "Claude Opus 5.5" || got[0].Created.IsZero() {
		t.Fatalf("first = %+v", got[0])
	}
	if sawAuth != "Bearer tok" || sawVersion != "2023-06-01" || sawAfter != "claude-opus-5-5" {
		t.Fatalf("auth=%q version=%q after=%q", sawAuth, sawVersion, sawAfter)
	}
}

func TestFetchListing_reads_the_openai_shape_and_its_windows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"deepseek-flash","context_window":1048576},
			{"id":"k3","created":1761264000,"context_length":262144}]}`))
	}))
	defer server.Close()

	got, err := FetchListing(server.Client(), server.URL, http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Context != 1048576 || got[1].Context != 262144 || got[1].Created.Unix() != 1761264000 {
		t.Fatalf("got %+v", got)
	}
}

func TestFetchListing_fails_on_a_non_200_or_bad_json(t *testing.T) {
	for _, handler := range []http.HandlerFunc{
		func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusUnauthorized) },
		func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>`)) },
	} {
		server := httptest.NewServer(handler)
		if _, err := FetchListing(server.Client(), server.URL, http.Header{}); err == nil {
			t.Errorf("want an error")
		}
		server.Close()
	}
}

func TestReadCodexModels_keeps_only_listed_rows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models_cache.json")
	body := `{"fetched_at":"x","models":[
		{"slug":"gpt-6-astra","display_name":"GPT-6-Astra","visibility":"list","context_window":272000},
		{"slug":"gpt-reserve","display_name":"GPT-Reserve","visibility":"hide","context_window":272000}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCodexModels(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "gpt-6-astra" || got[0].Label != "GPT-6-Astra" || got[0].Context != 272000 {
		t.Fatalf("got %+v", got)
	}
	if _, err := ReadCodexModels(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("want an error for a missing file")
	}
}
