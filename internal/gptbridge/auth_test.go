package gptbridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFakeAuthCodex(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadChatGPTAccountReturnsDiscoveredState(t *testing.T) {
	codex := writeFakeAuthCodex(t, `
read init
printf '{"id":1,"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"macos"}}\n'
read initialized
read account
printf '{"id":2,"result":{"account":null,"requiresOpenaiAuth":true}}\n'
read models
printf '{"id":3,"result":{"data":[]}}\n'
while read line; do :; done
`)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	account, err := ReadChatGPTAccount(ctx, ChatGPTAuthOptions{
		CodexPath: codex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.Account != nil || !account.RequiresOpenAIAuth {
		t.Fatalf("account = %+v, want signed out", account)
	}
}

func TestAuthenticateChatGPTPresentsURLAndRefreshesAccount(t *testing.T) {
	codex := writeFakeAuthCodex(t, `
read init
printf '{"id":1,"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"macos"}}\n'
read initialized
read account_before_login
printf '{"id":2,"result":{"account":{"type":"chatgpt","email":"old@example.com","planType":"plus"},"requiresOpenaiAuth":true}}\n'
read models_before_login
printf '{"id":3,"result":{"data":[]}}\n'
read login_start
printf '{"id":4,"result":{"type":"chatgpt","loginId":"login-42","authUrl":"https://chatgpt.com/auth/wisp"}}\n'
printf '{"method":"account/login/completed","params":{"loginId":"login-42","success":true,"error":null}}\n'
read account_after_login
printf '{"id":5,"result":{"account":{"type":"chatgpt","email":"new@example.com","planType":"pro"},"requiresOpenaiAuth":true}}\n'
read models_after_login
printf '{"id":6,"result":{"data":[]}}\n'
while read line; do :; done
`)

	openErr := errors.New("browser unavailable")
	var opened string
	var events []ChatGPTAuthEvent
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	account, err := AuthenticateChatGPT(ctx, ChatGPTAuthOptions{
		CodexPath: codex,
		OpenURL: func(url string) error {
			opened = url
			return openErr
		},
	}, func(event ChatGPTAuthEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened != "https://chatgpt.com/auth/wisp" {
		t.Fatalf("opened URL = %q", opened)
	}
	if len(events) != 1 || events[0].URL != opened ||
		!errors.Is(events[0].OpenErr, openErr) {
		t.Fatalf("events = %+v, want URL and opener error", events)
	}
	if account.Account == nil || account.Account.Type != "chatgpt" ||
		account.Account.Email == nil || *account.Account.Email != "new@example.com" {
		t.Fatalf("account = %+v, want refreshed ChatGPT account", account)
	}
}
