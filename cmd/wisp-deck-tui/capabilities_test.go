package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestCapabilitiesEmitsExactStableJSON(t *testing.T) {
	want := binaryCapabilities{
		HostEffectsCompiled:     true,
		SoundPreviewCompiled:    true,
		HostEffectsBoundary:     1,
		HostEffectsAllowed:      false,
		HostEffectsDenialReason: hostEffectsDeniedAncestorMarker,
	}
	command := newCapabilitiesCommand(func() binaryCapabilities {
		return want
	})
	var output bytes.Buffer
	command.SetOut(&output)

	if err := command.Execute(); err != nil {
		t.Fatalf("execute capabilities: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode capabilities: %v\noutput: %s", err, output.String())
	}
	wantFields := map[string]any{
		"host_effects_compiled":      true,
		"sound_preview_compiled":     true,
		"host_effects_boundary":      float64(1),
		"host_effects_allowed":       false,
		"host_effects_denial_reason": hostEffectsDeniedAncestorMarker,
	}
	if !reflect.DeepEqual(got, wantFields) {
		t.Fatalf("capabilities JSON = %#v, want exactly %#v", got, wantFields)
	}
}

func TestCapabilitiesOmitsDenialReasonWhenAllowed(t *testing.T) {
	command := newCapabilitiesCommand(func() binaryCapabilities {
		return binaryCapabilities{
			HostEffectsCompiled:  true,
			SoundPreviewCompiled: true,
			HostEffectsBoundary:  1,
			HostEffectsAllowed:   true,
		}
	})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatalf("execute capabilities: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, exists := got["host_effects_denial_reason"]; exists {
		t.Fatalf("allowed capabilities included denial reason: %s", output.String())
	}
}

func TestCapabilitiesRequireProductionIgnoresRuntimeDenial(t *testing.T) {
	command := newCapabilitiesCommand(func() binaryCapabilities {
		return binaryCapabilities{
			HostEffectsCompiled:     true,
			SoundPreviewCompiled:    true,
			HostEffectsBoundary:     1,
			HostEffectsAllowed:      false,
			HostEffectsDenialReason: hostEffectsDeniedAncestorMarker,
		}
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--require-production"})

	if err := command.Execute(); err != nil {
		t.Fatalf("valid production artifact rejected for runtime denial: %v", err)
	}
	if output.Len() == 0 {
		t.Fatal("require-production emitted no JSON")
	}
}

func TestCapabilitiesRequireProductionEmitsJSONBeforeFailure(t *testing.T) {
	tests := map[string]binaryCapabilities{
		"host effects disabled": {
			SoundPreviewCompiled: true,
			HostEffectsBoundary:  1,
		},
		"sound preview disabled": {
			HostEffectsCompiled: true,
			HostEffectsBoundary: 1,
		},
		"boundary missing": {
			HostEffectsCompiled:  true,
			SoundPreviewCompiled: true,
		},
		"boundary unknown": {
			HostEffectsCompiled:  true,
			SoundPreviewCompiled: true,
			HostEffectsBoundary:  2,
		},
	}
	for name, capabilities := range tests {
		t.Run(name, func(t *testing.T) {
			command := newCapabilitiesCommand(func() binaryCapabilities {
				return capabilities
			})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetArgs([]string{"--require-production"})
			if err := command.Execute(); err == nil {
				t.Fatal("invalid artifact passed --require-production")
			}
			var decoded map[string]any
			if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
				t.Fatalf("JSON was not emitted before failure: %v\n%s", err, output.String())
			}
		})
	}
}

func TestCapabilitiesCommandFactoryDoesNotLeakFlagState(t *testing.T) {
	valid := func() binaryCapabilities {
		return binaryCapabilities{
			HostEffectsCompiled:  true,
			SoundPreviewCompiled: true,
			HostEffectsBoundary:  1,
		}
	}
	first := newCapabilitiesCommand(valid)
	first.SetOut(&bytes.Buffer{})
	first.SetArgs([]string{"--require-production"})
	if err := first.Execute(); err != nil {
		t.Fatal(err)
	}

	second := newCapabilitiesCommand(valid)
	var output bytes.Buffer
	second.SetOut(&output)
	if err := second.Execute(); err != nil {
		t.Fatalf("fresh command inherited prior flag state: %v", err)
	}
}
