package allin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// maxListingPages bounds pagination so a server that always says has_more
// cannot keep a refresh running.
const maxListingPages = 10

type listingPage struct {
	Data []struct {
		ID            string `json:"id"`
		DisplayName   string `json:"display_name"`
		CreatedAt     string `json:"created_at"`
		Created       int64  `json:"created"`
		ContextLength int    `json:"context_length"`
		ContextWindow int    `json:"context_window"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// FetchListing reads a /models list in either the Anthropic shape
// (display_name, created_at, has_more) or the OpenAI one (created in epoch
// seconds). auth is copied onto every request as-is.
func FetchListing(client *http.Client, listURL string, auth http.Header) ([]Listed, error) {
	var out []Listed
	after := ""
	for page := 0; page < maxListingPages; page++ {
		target := listURL
		if after != "" {
			parsed, err := url.Parse(listURL)
			if err != nil {
				return nil, err
			}
			query := parsed.Query()
			query.Set("after_id", after)
			parsed.RawQuery = query.Encode()
			target = parsed.String()
		}
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		for key, values := range auth {
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
		req.Header.Set("anthropic-version", "2023-06-01")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("allin: model list %s answered %d", listURL, resp.StatusCode)
		}
		var parsed listingPage
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("allin: model list %s: %w", listURL, err)
		}
		for _, m := range parsed.Data {
			item := Listed{ID: m.ID, Label: m.DisplayName, Context: m.ContextWindow}
			if item.Context == 0 {
				item.Context = m.ContextLength
			}
			if created, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
				item.Created = created
			} else if m.Created > 0 {
				item.Created = time.Unix(m.Created, 0)
			}
			out = append(out, item)
		}
		if !parsed.HasMore || parsed.LastID == "" {
			break
		}
		after = parsed.LastID
	}
	return out, nil
}

// ReadCodexModels reads the model list Codex caches for itself. Only rows it
// shows in its own picker are kept; hidden ones are internal.
func ReadCodexModels(path string) ([]Listed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cache struct {
		Models []struct {
			Slug          string `json:"slug"`
			DisplayName   string `json:"display_name"`
			Visibility    string `json:"visibility"`
			ContextWindow int    `json:"context_window"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	var out []Listed
	for _, m := range cache.Models {
		if m.Visibility != "list" || m.Slug == "" {
			continue
		}
		out = append(out, Listed{ID: m.Slug, Label: m.DisplayName, Context: m.ContextWindow})
	}
	return out, nil
}
