package auth

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/nover/intermarche-mcp/internal/config"
)

func loadTokens() (*TokenSet, error) {
	path, err := config.TokenFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var ts TokenSet
	if err := json.Unmarshal(data, &ts); err != nil {
		return nil, err
	}
	return &ts, nil
}

func saveTokens(ts *TokenSet) error {
	path, err := config.TokenFilePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func clearTokens() error {
	path, err := config.TokenFilePath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
