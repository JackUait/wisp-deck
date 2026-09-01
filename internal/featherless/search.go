package featherless

import (
	"sort"
	"strings"
)

// Search narrows the catalog to models matching the query, preserving the
// catalog's own order within each rank. Ids are namespaced (owner/name), so a
// bare query is nearly always the name half — a match there outranks one in the
// owner or the class.
func Search(models []Model, query string) []Model {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return models
	}
	type ranked struct {
		model Model
		rank  int
		order int
	}
	var hits []ranked
	for i, model := range models {
		id := strings.ToLower(model.ID)
		name := id
		if slash := strings.LastIndex(id, "/"); slash >= 0 {
			name = id[slash+1:]
		}
		rank := -1
		switch {
		case strings.HasPrefix(name, query):
			rank = 0
		case strings.Contains(id, query):
			rank = 1
		case strings.Contains(strings.ToLower(model.Class), query):
			rank = 2
		}
		if rank >= 0 {
			hits = append(hits, ranked{model: model, rank: rank, order: i})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		return hits[i].order < hits[j].order
	})
	out := make([]Model, len(hits))
	for i, hit := range hits {
		out[i] = hit.model
	}
	return out
}
