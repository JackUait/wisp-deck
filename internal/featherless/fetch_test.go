package featherless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Featherless rejects x-api-key with a 401; the credential travels as a bearer
// token, which is exactly what the profile's ANTHROPIC_AUTH_TOKEN produces.
func TestFetch_sends_the_key_as_a_bearer_token(t *testing.T) {
	var gotAuth, gotAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		_, _ = w.Write(fixture(t))
	}))
	defer server.Close()

	models, err := fetchFrom(context.Background(), server.URL, "rc_secret")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer rc_secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer rc_secret")
	}
	if gotAPIKey != "" {
		t.Errorf("x-api-key sent (%q); Featherless 401s on it", gotAPIKey)
	}
	if len(models) != 4 {
		t.Errorf("got %d models, want the 4 usable ones", len(models))
	}
}

// The catalog is public. A user who has not entered a key yet must still be able
// to browse and pick, or the flow deadlocks on itself.
func TestFetch_works_without_a_key(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("no key means no Authorization header at all")
		}
		_, _ = w.Write(fixture(t))
	}))
	defer server.Close()

	if _, err := fetchFrom(context.Background(), server.URL, ""); err != nil {
		t.Fatal(err)
	}
}

func TestFetch_reports_a_failing_status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream is down"))
	}))
	defer server.Close()

	if _, err := fetchFrom(context.Background(), server.URL, ""); err == nil {
		t.Error("a 502 must be an error, not an empty catalog")
	}
}

func TestFetch_honours_a_cancelled_context(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture(t))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchFrom(ctx, server.URL, ""); err == nil {
		t.Error("a cancelled context must fail the fetch")
	}
}

func TestCatalogURL_points_at_the_documented_listing(t *testing.T) {
	if CatalogURL != "https://api.featherless.ai/v1/models" {
		t.Errorf("CatalogURL = %q", CatalogURL)
	}
}
