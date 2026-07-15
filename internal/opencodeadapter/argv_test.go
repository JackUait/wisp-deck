package opencodeadapter

import (
	"reflect"
	"testing"
)

func TestOpenCodeArgvIsPureAuthenticatedAndExact(t *testing.T) {
	prefixes := [][]string{
		{"/opt/opencode"},
		{"/opt/npx", "--no-install", "opencode-ai"},
		{"/opt/npx", "--prefer-offline", "opencode-ai@latest"},
	}
	for _, prefix := range prefixes {
		server, err := BuildServerArgv(prefix, 41723)
		if err != nil {
			t.Fatalf("server %v: %v", prefix, err)
		}
		wantServer := append(append([]string(nil), prefix...),
			"--pure", "serve", "--hostname", "127.0.0.1", "--port", "41723")
		if !reflect.DeepEqual(server, wantServer) {
			t.Fatalf("server argv = %#v, want %#v", server, wantServer)
		}

		attach, err := BuildAttachArgv(prefix, AttachOptions{
			URL: "http://127.0.0.1:41723", ProjectDir: "/repo with spaces", Password: "secret-value",
			Continue: true,
		})
		if err != nil {
			t.Fatalf("attach %v: %v", prefix, err)
		}
		wantAttach := append(append([]string(nil), prefix...),
			"--pure", "attach", "http://127.0.0.1:41723", "--dir", "/repo with spaces",
			"--password", "secret-value", "--continue")
		if !reflect.DeepEqual(attach, wantAttach) {
			t.Fatalf("attach argv = %#v, want %#v", attach, wantAttach)
		}
	}
}

func TestOpenCodeArgvRejectsUncontrolledInputs(t *testing.T) {
	badPrefixes := [][]string{
		nil,
		{"opencode"},
		{"/opt/opencode", "--plugin", "evil"},
		{"/opt/npx", "opencode-ai"},
		{"/opt/npx", "--no-install", "evil"},
	}
	for _, prefix := range badPrefixes {
		if _, err := BuildServerArgv(prefix, 41000); err == nil {
			t.Fatalf("unsafe prefix %#v accepted", prefix)
		}
	}
	if _, err := BuildServerArgv([]string{"/opt/opencode"}, 0); err == nil {
		t.Fatal("port zero accepted")
	}
	for _, options := range []AttachOptions{
		{URL: "http://localhost:1", ProjectDir: "/repo", Password: "secret"},
		{URL: "http://127.0.0.1:41723", ProjectDir: "relative", Password: "secret"},
		{URL: "http://127.0.0.1:41723", ProjectDir: "/repo", Password: ""},
		{URL: "http://127.0.0.1:41723", ProjectDir: "/repo", Password: "secret", Continue: true, Session: "ses_1"},
	} {
		if _, err := BuildAttachArgv([]string{"/opt/opencode"}, options); err == nil {
			t.Fatalf("unsafe options %#v accepted", options)
		}
	}
}

func TestAttachArgvUsesExactSessionWithoutContinue(t *testing.T) {
	got, err := BuildAttachArgv([]string{"/opt/opencode"}, AttachOptions{
		URL: "http://127.0.0.1:41723", ProjectDir: "/repo", Password: "secret", Session: "ses_exact",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/opencode", "--pure", "attach", "http://127.0.0.1:41723",
		"--dir", "/repo", "--password", "secret", "--session", "ses_exact"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}
