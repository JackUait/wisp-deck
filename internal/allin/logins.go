package allin

import (
	"encoding/json"
	"errors"
	"fmt"
)

// KeychainLogins is claudeaccount.LoginStore over the real Keychain. Only the
// claudeAiOauth object moves between slots: mcpOAuth sits in the same entry
// and belongs to the slot, not to the login.
type KeychainLogins struct{}

func loginOf(blob []byte) (json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(blob, &root); err != nil {
		return nil, fmt.Errorf("allin: parse keychain blob: %w", err)
	}
	login, ok := root["claudeAiOauth"]
	if !ok || string(login) == "null" {
		return nil, errors.New("allin: the keychain entry holds no Claude login")
	}
	return login, nil
}

func withLogin(blob []byte, login json.RawMessage) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(blob, &root); err != nil {
		return nil, fmt.Errorf("allin: parse keychain blob: %w", err)
	}
	root["claudeAiOauth"] = login
	return json.Marshal(root)
}
