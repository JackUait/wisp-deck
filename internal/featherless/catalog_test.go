package featherless

import (
	"os"
	"testing"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func ids(models []Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}

// Claude Code cannot run without tool calling: a model lacking it produces a
// pane that cannot read or edit a single file, so those models are never
// offered. A model with no declared context length cannot be sized, and an
// undeclared window strands the session on the flat 200000 default.
func TestParse_keeps_only_sizable_tool_calling_models(t *testing.T) {
	models, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.ID == "recursal/EagleX_1-7T" {
			t.Error("a model without features.tool_use must be dropped")
		}
		if m.ID == "broken/no-context" {
			t.Error("a model without context_length must be dropped")
		}
		if m.Context <= 0 {
			t.Errorf("%s kept with context %d", m.ID, m.Context)
		}
	}
	if len(models) != 4 {
		t.Fatalf("kept %d models (%v), want 4", len(models), ids(models))
	}
}

// Someone opening the picker is nearly always after the frontier tier, so the
// widest window comes first and ties break to the newest model.
func TestParse_orders_by_context_then_newest(t *testing.T) {
	models, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"moonshotai/Kimi-K3",
		"zai-org/GLM-5.2",
		"unsloth/Llama-3.3-70B-Instruct",
		"meta-llama/Llama-3.3-70B-Instruct",
	}
	got := ids(models)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestParse_carries_the_fields_the_picker_and_the_pick_need(t *testing.T) {
	models, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	kimi := models[0]
	if kimi.Context != 262144 {
		t.Errorf("context = %d, want 262144", kimi.Context)
	}
	if kimi.InPerM != 3 || kimi.OutPerM != 15 {
		t.Errorf("price = %v/%v, want 3/15", kimi.InPerM, kimi.OutPerM)
	}
	if !kimi.ImageInput {
		t.Error("Kimi-K3 reports image_input and must carry it: the pick defaults the images toggle from it")
	}
	if models[1].ImageInput {
		t.Error("GLM-5.2 declares no image_input and must be text-only")
	}
}

// available_on_current_plan is absent on an unauthenticated listing, and absent
// must not read as "unavailable" — that would empty the picker for a user who
// has not typed a key yet.
func TestParse_treats_an_absent_plan_flag_as_available(t *testing.T) {
	models, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.ID == "meta-llama/Llama-3.3-70B-Instruct" {
			if m.OnPlan {
				t.Error("an explicit available_on_current_plan:false must be preserved")
			}
			continue
		}
		if !m.OnPlan {
			t.Errorf("%s has no plan flag and must default to available", m.ID)
		}
	}
}

func TestParse_rejects_a_body_that_is_not_the_catalog(t *testing.T) {
	for name, body := range map[string]string{
		"truncated": `{"data":[{"id":"a"`,
		"html":      `<!doctype html><title>502</title>`,
		"error":     `{"error":{"message":"nope"}}`,
	} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s body accepted, want an error", name)
		}
	}
}
