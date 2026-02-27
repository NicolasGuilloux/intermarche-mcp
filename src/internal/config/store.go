package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type StoreConfig struct {
	PdvRef string `json:"pdv_ref"`
}

func LoadStore() (*StoreConfig, error) {
	path, err := StoreFilePath()
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

	var sc StoreConfig
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, err
	}
	return &sc, nil
}

func SaveStore(sc *StoreConfig) error {
	path, err := StoreFilePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// RequireStore loads the store config and returns an error if no store is set.
func RequireStore() (*StoreConfig, error) {
	sc, err := LoadStore()
	if err != nil {
		return nil, fmt.Errorf("loading store config: %w", err)
	}
	if sc == nil || sc.PdvRef == "" {
		return nil, fmt.Errorf("no store selected, run: intermarche-mcp store set <pdvRef>")
	}
	return sc, nil
}
