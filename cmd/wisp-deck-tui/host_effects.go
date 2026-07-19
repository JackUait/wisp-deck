package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jackuait/wisp-deck/internal/attention"
	"github.com/jackuait/wisp-deck/internal/soundpref"
)

type hostEffectKind uint8

const (
	hostEffectSystemSound hostEffectKind = iota + 1
	hostEffectClaudeBackgroundNotification
)

type claudeBackgroundNotificationKind uint8

const (
	claudeBackgroundNotificationNeedsAttention claudeBackgroundNotificationKind = iota + 1
	claudeBackgroundNotificationNeedsInput
	claudeBackgroundNotificationCompleted
	claudeBackgroundNotificationFailed
	claudeBackgroundNotificationStopped
)

type hostEffect struct {
	kind             hostEffectKind
	soundName        string
	notificationKind claudeBackgroundNotificationKind
}

type hostEffectPlan struct {
	executable  string
	arguments   []string
	environment []string
}

func newSystemSoundHostEffect(name string) (hostEffect, bool) {
	if !soundpref.IsAllowedName(name) {
		return hostEffect{}, false
	}
	return hostEffect{
		kind:      hostEffectSystemSound,
		soundName: name,
	}, true
}

func newClaudeBackgroundNotificationHostEffect(
	status attention.ClaudeBackgroundStatus,
) hostEffect {
	kind := claudeBackgroundNotificationNeedsAttention
	switch status {
	case attention.ClaudeBackgroundBlocked:
		kind = claudeBackgroundNotificationNeedsInput
	case attention.ClaudeBackgroundCompleted:
		kind = claudeBackgroundNotificationCompleted
	case attention.ClaudeBackgroundFailed:
		kind = claudeBackgroundNotificationFailed
	case attention.ClaudeBackgroundStopped:
		kind = claudeBackgroundNotificationStopped
	}
	return hostEffect{
		kind:             hostEffectClaudeBackgroundNotification,
		notificationKind: kind,
	}
}

func planHostEffect(effect hostEffect, inherited []string) (hostEffectPlan, bool) {
	switch effect.kind {
	case hostEffectSystemSound:
		if !soundpref.IsAllowedName(effect.soundName) {
			return hostEffectPlan{}, false
		}
		return hostEffectPlan{
			executable: "/usr/bin/afplay",
			arguments: []string{
				"/System/Library/Sounds/" + effect.soundName + ".aiff",
			},
		}, true
	case hostEffectClaudeBackgroundNotification:
		body, ok := claudeBackgroundNotificationBody(effect.notificationKind)
		if !ok {
			return hostEffectPlan{}, false
		}
		return hostEffectPlan{
			executable: "/usr/bin/osascript",
			arguments: []string{
				"-e",
				`display notification (system attribute "WISP_DECK_NOTIFICATION_BODY") with title (system attribute "WISP_DECK_NOTIFICATION_TITLE")`,
			},
			environment: hostEffectEnvironment(
				inherited,
				"Claude background",
				body,
			),
		}, true
	default:
		return hostEffectPlan{}, false
	}
}

func claudeBackgroundNotificationBody(
	kind claudeBackgroundNotificationKind,
) (string, bool) {
	switch kind {
	case claudeBackgroundNotificationNeedsAttention:
		return "Background agent needs attention", true
	case claudeBackgroundNotificationNeedsInput:
		return "Background agent needs input", true
	case claudeBackgroundNotificationCompleted:
		return "Background agent completed", true
	case claudeBackgroundNotificationFailed:
		return "Background agent failed", true
	case claudeBackgroundNotificationStopped:
		return "Background agent stopped", true
	default:
		return "", false
	}
}

func hostEffectEnvironment(inherited []string, title, body string) []string {
	environment := make([]string, 0, len(inherited)+2)
	for _, entry := range inherited {
		key, _, found := strings.Cut(entry, "=")
		if found &&
			(key == "WISP_DECK_NOTIFICATION_TITLE" ||
				key == "WISP_DECK_NOTIFICATION_BODY") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(
		environment,
		"WISP_DECK_NOTIFICATION_TITLE="+title,
		"WISP_DECK_NOTIFICATION_BODY="+body,
	)
}

func runHostEffect(ctx context.Context, effect hostEffect) error {
	if !currentHostEffectsDecision().Allowed {
		return nil
	}
	plan, ok := planHostEffect(effect, os.Environ())
	if !ok {
		return nil
	}
	cmd := exec.CommandContext(ctx, plan.executable, plan.arguments...)
	cmd.Env = plan.environment
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	configureHostEffectProcessGroup(cmd)
	return cmd.Run()
}

func configureHostEffectProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
