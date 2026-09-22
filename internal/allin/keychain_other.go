//go:build !darwin

package allin

import (
	"encoding/json"
	"errors"
)

func keychainToken(string) (string, error) {
	return "", errors.New("allin: Keychain is only readable on darwin")
}

func (KeychainLogins) Login(string) (json.RawMessage, error) {
	return nil, errors.New("allin: Keychain is only readable on darwin")
}

func (KeychainLogins) SetLogin(string, json.RawMessage) error {
	return errors.New("allin: Keychain is only writable on darwin")
}

func (KeychainLogins) Lock(string) (func(), error) {
	return nil, errors.New("allin: Keychain is only lockable on darwin")
}
