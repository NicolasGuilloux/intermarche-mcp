package config

import (
	"os"
	"path/filepath"
)

const (
	Issuer  = "https://itmconnect.intermarche.com/auth/realms/customers"
	AuthURL = Issuer + "/protocol/openid-connect/auth"

	TokenURL  = Issuer + "/protocol/openid-connect/token"
	RevokeURL = Issuer + "/protocol/openid-connect/revoke"

	ClientID = "desktop"
	Scopes   = "openid email profile offline_access"
)

func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "intermarche-mcp")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func TokenFilePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tokens.json"), nil
}

func StoreFilePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "store.json"), nil
}
