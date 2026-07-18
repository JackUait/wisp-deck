package main

import "testing"

func TestHostEffectsAllowedRequiresEveryBoundarySignal(t *testing.T) {
	tests := map[string]struct {
		capability      string
		testBinary      bool
		testEnvironment string
		testAncestor    bool
		ancestryKnown   bool
		want            bool
	}{
		"production": {
			capability:    "enabled",
			ancestryKnown: true,
			want:          true,
		},
		"compiled disabled": {
			capability:    "disabled",
			ancestryKnown: true,
		},
		"unknown capability": {
			capability:    "anything-else",
			ancestryKnown: true,
		},
		"Go test binary": {
			capability:    "enabled",
			testBinary:    true,
			ancestryKnown: true,
		},
		"exact current marker": {
			capability:      "enabled",
			testEnvironment: "1",
			ancestryKnown:   true,
		},
		"marker prefix is not exact": {
			capability:      "enabled",
			testEnvironment: "10",
			ancestryKnown:   true,
			want:            true,
		},
		"test ancestor": {
			capability:    "enabled",
			testAncestor:  true,
			ancestryKnown: true,
		},
		"unknown ancestry": {
			capability: "enabled",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := hostEffectsAllowed(
				test.capability,
				test.testBinary,
				test.testEnvironment,
				test.testAncestor,
				test.ancestryKnown,
			)
			if got != test.want {
				t.Fatalf("hostEffectsAllowed() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestHostEffectsDecisionUsesStableDenialReasons(t *testing.T) {
	tests := map[string]struct {
		capability      string
		testBinary      bool
		testEnvironment string
		ancestry        hostProcessAncestry
		wantAllowed     bool
		wantReason      string
	}{
		"compiled disabled": {
			capability: "disabled",
			ancestry:   hostProcessAncestry{Known: true},
			wantReason: hostEffectsDeniedCompiled,
		},
		"unknown capability fails compiled closed": {
			capability: "surprise",
			ancestry:   hostProcessAncestry{Known: true},
			wantReason: hostEffectsDeniedCompiled,
		},
		"Go test binary": {
			capability: "enabled",
			testBinary: true,
			ancestry:   hostProcessAncestry{Known: true},
			wantReason: hostEffectsDeniedGoTest,
		},
		"current marker": {
			capability:      "enabled",
			testEnvironment: "1",
			ancestry:        hostProcessAncestry{Known: true},
			wantReason:      hostEffectsDeniedCurrentMarker,
		},
		"ancestor marker beats test path": {
			capability: "enabled",
			ancestry: hostProcessAncestry{
				Known:          true,
				TestMarker:     true,
				TestExecutable: true,
			},
			wantReason: hostEffectsDeniedAncestorMarker,
		},
		"ancestor sentinel beats every fallback": {
			capability: "enabled",
			ancestry: hostProcessAncestry{
				TestSentinel:   true,
				TestMarker:     true,
				TestExecutable: true,
			},
			wantReason: hostEffectsDeniedAncestorSentinel,
		},
		"test path fallback": {
			capability: "enabled",
			ancestry: hostProcessAncestry{
				Known:          true,
				TestExecutable: true,
			},
			wantReason: hostEffectsDeniedAncestorTestBinary,
		},
		"unknown ancestry": {
			capability: "enabled",
			wantReason: hostEffectsDeniedUnknownAncestry,
		},
		"allowed": {
			capability:  "enabled",
			ancestry:    hostProcessAncestry{Known: true},
			wantAllowed: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := hostEffectsDecisionFor(
				test.capability,
				test.testBinary,
				test.testEnvironment,
				test.ancestry,
			)
			if got.Allowed != test.wantAllowed || got.DenialReason != test.wantReason {
				t.Fatalf(
					"hostEffectsDecisionFor() = %#v, want allowed=%t reason=%q",
					got,
					test.wantAllowed,
					test.wantReason,
				)
			}
		})
	}
}

func TestHostEffectsBoundaryDefaultsFailClosed(t *testing.T) {
	if HostEffectsCapability != "disabled" {
		t.Fatalf("HostEffectsCapability = %q, want disabled", HostEffectsCapability)
	}
	if HostEffectsBoundaryVersion != 1 {
		t.Fatalf("HostEffectsBoundaryVersion = %d, want 1", HostEffectsBoundaryVersion)
	}
	if wispDeckRepositoryTestSentinel != "__WISP_DECK_REPOSITORY_TEST_V1__.test" {
		t.Fatalf(
			"wispDeckRepositoryTestSentinel = %q, want exact versioned sentinel",
			wispDeckRepositoryTestSentinel,
		)
	}
}
