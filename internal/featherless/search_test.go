package featherless

import "testing"

func searchCorpus() []Model {
	return []Model{
		{ID: "moonshotai/Kimi-K3", Class: "kimi3-2780b", Context: 262144},
		{ID: "zai-org/GLM-5.2", Class: "glm52-753b", Context: 262144},
		{ID: "deepseek-ai/DeepSeek-V4-Flash", Class: "deepseek4-284b", Context: 262144},
		{ID: "unsloth/Llama-3.3-70B-Instruct", Class: "llama33-70b", Context: 32768},
		{ID: "Sao10K/L3.3-70B-Euryale-v2.3", Class: "llama33-70b", Context: 32768},
	}
}

func TestSearch_empty_query_keeps_the_catalog_order(t *testing.T) {
	got := Search(searchCorpus(), "  ")
	if len(got) != 5 || got[0].ID != "moonshotai/Kimi-K3" {
		t.Errorf("empty query changed the list: %v", ids(got))
	}
}

func TestSearch_matches_the_id_case_insensitively(t *testing.T) {
	got := Search(searchCorpus(), "KIMI")
	if len(got) != 1 || got[0].ID != "moonshotai/Kimi-K3" {
		t.Errorf("got %v, want just Kimi-K3", ids(got))
	}
}

// Model ids are namespaced, so someone typing a bare model name is typing the
// part after the slash. That must rank above an incidental match elsewhere.
func TestSearch_ranks_a_name_match_above_a_namespace_match(t *testing.T) {
	corpus := append(searchCorpus(), Model{ID: "llama-org/Some-Other-Model", Class: "x-1b", Context: 8192})
	got := Search(corpus, "llama")
	if len(got) != 3 {
		t.Fatalf("got %v, want 3 matches", ids(got))
	}
	if got[0].ID != "unsloth/Llama-3.3-70B-Instruct" {
		t.Errorf("first = %q, want the model whose name starts with llama", got[0].ID)
	}
}

// A model family is how someone thinks about these ("give me a llama33"), and
// the class is the only place that family name appears for a fine-tune whose id
// does not carry it.
func TestSearch_matches_the_model_class(t *testing.T) {
	got := Search(searchCorpus(), "llama33")
	if len(got) != 2 {
		t.Fatalf("got %v, want both llama33 models", ids(got))
	}
}

func TestSearch_reports_no_match_as_an_empty_list(t *testing.T) {
	if got := Search(searchCorpus(), "nothing-matches-this"); len(got) != 0 {
		t.Errorf("got %v, want none", ids(got))
	}
}
