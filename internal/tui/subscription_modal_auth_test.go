package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jackuait/wisp-deck/internal/gptbridge"
)

func applySubscriptionAuthMsg(
	t *testing.T,
	m *MainMenuModel,
	msg tea.Msg,
) (*MainMenuModel, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	got, ok := updated.(*MainMenuModel)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	return got, cmd
}

func TestSubscriptionModalChatGPTAuthChecksAccountAsynchronously(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.SetCodexPath("/opt/codex")
	m.chatGPTAuthCheck = func(
		context.Context,
		string,
	) (gptbridge.AccountReadResult, error) {
		return gptbridge.AccountReadResult{
			Account: &gptbridge.Account{
				Type:     "chatgpt",
				PlanType: "plus",
			},
			RequiresOpenAIAuth: true,
		}, nil
	}

	cmd := m.openSubscriptionModal()
	if cmd == nil {
		t.Fatal("opening ChatGPT profile did not schedule an account check")
	}
	if m.subscriptionModal.auth.status != subscriptionAuthChecking {
		t.Fatalf("initial status = %v, want checking", m.subscriptionModal.auth.status)
	}

	m, next := applySubscriptionAuthMsg(t, m, cmd())
	if next != nil {
		t.Fatal("account check unexpectedly scheduled more work")
	}
	if m.subscriptionModal.auth.status != subscriptionAuthSignedIn {
		t.Fatalf("checked status = %v, want signed in", m.subscriptionModal.auth.status)
	}
}

func TestSubscriptionModalChatGPTAuthPresentsURLBeforeLoginCompletes(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.SetCodexPath("/opt/codex")
	m.openSubscriptionModal()

	release := make(chan struct{})
	m.chatGPTAuthLogin = func(
		ctx context.Context,
		_ string,
		present func(gptbridge.ChatGPTAuthEvent),
	) (gptbridge.AccountReadResult, error) {
		present(gptbridge.ChatGPTAuthEvent{
			URL: "https://chatgpt.com/auth/wisp",
		})
		select {
		case <-release:
			return gptbridge.AccountReadResult{
				Account: &gptbridge.Account{Type: "chatgpt", PlanType: "pro"},
			}, nil
		case <-ctx.Done():
			return gptbridge.AccountReadResult{}, ctx.Err()
		}
	}

	cmd := m.startSubscriptionChatGPTLogin()
	if cmd == nil {
		t.Fatal("login action returned no command")
	}
	if !m.subscriptionModal.auth.pending {
		t.Fatal("login action did not enter pending state")
	}
	first := make(chan tea.Msg, 1)
	go func() { first <- cmd() }()

	var msg tea.Msg
	select {
	case msg = <-first:
	case <-time.After(time.Second):
		t.Fatal("login URL was not emitted before completion")
	}
	m, next := applySubscriptionAuthMsg(t, m, msg)
	if m.subscriptionModal.auth.url != "https://chatgpt.com/auth/wisp" {
		t.Fatalf("auth URL = %q", m.subscriptionModal.auth.url)
	}
	if next == nil {
		t.Fatal("URL event did not schedule completion wait")
	}
	if duplicate := m.startSubscriptionChatGPTLogin(); duplicate != nil {
		t.Fatal("pending login allowed a duplicate command")
	}

	close(release)
	m, next = applySubscriptionAuthMsg(t, m, next())
	if next != nil {
		t.Fatal("login completion unexpectedly scheduled more work")
	}
	if m.subscriptionModal.auth.pending {
		t.Fatal("successful login remained pending")
	}
	if m.subscriptionModal.auth.status != subscriptionAuthSignedIn {
		t.Fatalf("completed status = %v, want signed in", m.subscriptionModal.auth.status)
	}
}

func TestSubscriptionModalChatGPTAuthDiscardCloseCancelsLogin(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.SetCodexPath("/opt/codex")
	m.openSubscriptionModal()

	started := make(chan struct{})
	canceled := make(chan struct{})
	m.chatGPTAuthLogin = func(
		ctx context.Context,
		_ string,
		_ func(gptbridge.ChatGPTAuthEvent),
	) (gptbridge.AccountReadResult, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return gptbridge.AccountReadResult{}, ctx.Err()
	}
	cmd := m.startSubscriptionChatGPTLogin()
	go func() { _ = cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("login did not start")
	}

	m.subscriptionModal.draft.dirty = true
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.subscriptionModal.mode != subscriptionDiscardConfirm {
		t.Fatalf("close mode = %v, want discard confirmation", m.subscriptionModal.mode)
	}
	m = subscriptionModalKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.subscriptionModal.open {
		t.Fatal("discard confirmation did not close modal")
	}

	select {
	case <-canceled:
	case <-time.After(100 * time.Millisecond):
		m.cancelSubscriptionAuth()
		t.Fatal("discard-confirmed close did not cancel login")
	}
	if m.subscriptionModal.auth.pending {
		t.Fatal("closed modal kept pending login state")
	}
}

func TestSubscriptionModalChatGPTAuthIgnoresStaleCheckAfterReopen(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.SetCodexPath("/opt/codex")
	m.chatGPTAuthCheck = func(
		context.Context,
		string,
	) (gptbridge.AccountReadResult, error) {
		return gptbridge.AccountReadResult{
			Account: &gptbridge.Account{Type: "chatgpt"},
		}, nil
	}

	oldCheck := m.openSubscriptionModal()
	m.openSubscriptionModal()
	m, _ = applySubscriptionAuthMsg(t, m, oldCheck())

	if m.subscriptionModal.auth.status != subscriptionAuthChecking {
		t.Fatalf("stale check changed status to %v", m.subscriptionModal.auth.status)
	}
}
