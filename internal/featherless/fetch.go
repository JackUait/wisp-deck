package featherless

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CatalogURL is Featherless's model listing. It answers unauthenticated; a key
// only adds available_on_current_plan.
const CatalogURL = "https://api.featherless.ai/v1/models"

// fetchTimeout covers a ~7MB body over a slow link.
const fetchTimeout = 60 * time.Second

// maxCatalogBytes caps what a wrong URL or a hijacked response can read into
// memory. The real catalog is ~7MB.
const maxCatalogBytes = 64 << 20

// Fetch downloads the catalog. The key is optional and travels as a bearer
// token: Featherless answers x-api-key with a 401.
func Fetch(ctx context.Context, key string) ([]Model, error) {
	return fetchFrom(ctx, CatalogURL, key)
}

func fetchFrom(ctx context.Context, url, key string) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("featherless: fetch catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("featherless: catalog returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))
	if err != nil {
		return nil, fmt.Errorf("featherless: read catalog: %w", err)
	}
	return Parse(data)
}
