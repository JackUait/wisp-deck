package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jackuait/wisp-deck/internal/attention"
)

func TestHostEffectSystemSoundPlannerUsesAuditedPath(t *testing.T) {
	effect, ok := newSystemSoundHostEffect("Glass")
	if !ok {
		t.Fatal("allowlisted sound did not produce a typed effect")
	}
	plan, ok := planHostEffect(effect, []string{"HOME=/tmp/home"})
	if !ok {
		t.Fatal("allowlisted sound did not produce a process plan")
	}
	if plan.executable != "/usr/bin/afplay" {
		t.Fatalf("executable = %q, want /usr/bin/afplay", plan.executable)
	}
	wantArguments := []string{"/System/Library/Sounds/Glass.aiff"}
	if !reflect.DeepEqual(plan.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", plan.arguments, wantArguments)
	}
	if plan.environment != nil {
		t.Fatalf("sound environment = %#v, want inherited environment", plan.environment)
	}
}

func TestHostEffectSystemSoundPlannerRejectsEveryUnsafeName(t *testing.T) {
	for _, name := range []string{
		"",
		"glass",
		"NotASystemSound",
		"../../tmp/private",
		"Glass.aiff",
		"Glass\nPing",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := newSystemSoundHostEffect(name); ok {
				t.Fatalf("newSystemSoundHostEffect(%q) succeeded", name)
			}
			forged := hostEffect{
				kind:      hostEffectSystemSound,
				soundName: name,
			}
			if plan, ok := planHostEffect(forged, nil); ok {
				t.Fatalf("forged sound %q planned %#v", name, plan)
			}
		})
	}
}

func TestHostEffectClaudeBackgroundPlannerIsFixedAndPrivacyPreserving(t *testing.T) {
	private := []string{
		"private-job-id",
		"private question text",
		"private project",
	}
	inherited := []string{
		"HOME=/tmp/home",
		"WISP_DECK_NOTIFICATION_TITLE=" + private[0],
		"WISP_DECK_NOTIFICATION_BODY=" + private[1],
	}
	tests := map[attention.ClaudeBackgroundStatus]string{
		attention.ClaudeBackgroundBlocked:           "Background agent needs input",
		attention.ClaudeBackgroundCompleted:         "Background agent completed",
		attention.ClaudeBackgroundFailed:            "Background agent failed",
		attention.ClaudeBackgroundStopped:           "Background agent stopped",
		attention.ClaudeBackgroundStatus("unknown"): "Background agent needs attention",
	}
	for status, wantBody := range tests {
		t.Run(string(status), func(t *testing.T) {
			effect := newClaudeBackgroundNotificationHostEffect(status)
			plan, ok := planHostEffect(effect, inherited)
			if !ok {
				t.Fatal("fixed Claude background notification did not plan")
			}
			if plan.executable != "/usr/bin/osascript" {
				t.Fatalf("executable = %q, want /usr/bin/osascript", plan.executable)
			}
			wantArguments := []string{
				"-e",
				`display notification (system attribute "WISP_DECK_NOTIFICATION_BODY") with title (system attribute "WISP_DECK_NOTIFICATION_TITLE")`,
			}
			if !reflect.DeepEqual(plan.arguments, wantArguments) {
				t.Fatalf("arguments = %#v, want %#v", plan.arguments, wantArguments)
			}
			environment := strings.Join(plan.environment, "\n")
			for _, required := range []string{
				"HOME=/tmp/home",
				"WISP_DECK_NOTIFICATION_TITLE=Claude background",
				"WISP_DECK_NOTIFICATION_BODY=" + wantBody,
			} {
				if strings.Count(environment, required) != 1 {
					t.Fatalf("environment %q contains %q %d times, want once", environment, required, strings.Count(environment, required))
				}
			}
			for _, forbidden := range private {
				if strings.Contains(environment, forbidden) {
					t.Fatalf("private detail %q leaked into %q", forbidden, environment)
				}
			}
		})
	}
}

func TestHostEffectPlannerSupportsOnlyTypedSoundAndFixedNotification(t *testing.T) {
	for _, effect := range []hostEffect{
		{},
		{kind: hostEffectKind(255)},
		{kind: hostEffectSystemSound},
	} {
		if plan, ok := planHostEffect(effect, nil); ok {
			t.Fatalf("invalid effect %#v planned %#v", effect, plan)
		}
	}
}

func TestHostEffectRunnerCannotPlanInvalidEffect(t *testing.T) {
	if err := runHostEffect(context.Background(), hostEffect{}); err != nil {
		t.Fatalf("invalid host effect returned %v, want a silent no-op", err)
	}
}
