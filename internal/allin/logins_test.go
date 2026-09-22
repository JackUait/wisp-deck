package allin

import (
	"encoding/json"
	"testing"
)

func TestLoginOf_reads_only_the_claude_login(t *testing.T) {
	blob := []byte(`{"mcpOAuth":{"srv":{"t":"m"}},"claudeAiOauth":{"accessToken":"a","scopes":["x"]}}`)
	got, err := loginOf(blob)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"accessToken":"a","scopes":["x"]}` {
		t.Fatalf("login = %s", got)
	}
}

func TestLoginOf_refuses_a_blob_with_no_login(t *testing.T) {
	if _, err := loginOf([]byte(`{"mcpOAuth":{}}`)); err == nil {
		t.Fatal("a blob with no claudeAiOauth must be refused, or a move would copy null into another slot")
	}
}

func TestWithLogin_replaces_the_login_and_keeps_the_mcp_tokens(t *testing.T) {
	blob := []byte(`{"mcpOAuth":{"srv":{"t":"m"}},"claudeAiOauth":{"accessToken":"old"}}`)
	out, err := withLogin(blob, json.RawMessage(`{"accessToken":"new"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["claudeAiOauth"]) != `{"accessToken":"new"}` {
		t.Fatalf("login = %s", got["claudeAiOauth"])
	}
	if string(got["mcpOAuth"]) != `{"srv":{"t":"m"}}` {
		t.Fatalf("mcp tokens = %s", got["mcpOAuth"])
	}
}
