package session

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// config is what the app remembers between runs. It is deliberately tiny:
// the receiver's own state lives on the receiver.
type config struct {
	// WdiSimple is the path of a wdi-simple.exe the user chose, so the app
	// can use it again without asking.
	WdiSimple string `json:"wdiSimple,omitempty"`
}

func loadConfig(path string) (config, error) {
	var cfg config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func saveConfig(path string, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
