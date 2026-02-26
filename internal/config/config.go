package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ProviderType identifies the LLM backend.
type ProviderType string

const (
	ProviderLocal    ProviderType = "local"     // Local OpenAI-compatible (Ollama, LMStudio, etc.)
	ProviderKimiCode ProviderType = "kimi-code" // Kimi Code API
	ProviderZAI      ProviderType = "z-ai"      // z.ai API
)

// Config holds all persistent configuration for Genesis-HS.
type Config struct {
	Provider ProviderType `json:"provider"`
	BaseURL  string       `json:"base_url"`
	APIKey   string       `json:"api_key,omitempty"`
	Model    string       `json:"model"`
}

// DefaultConfig returns the default configuration (local Ollama).
func DefaultConfig() Config {
	return Config{
		Provider: ProviderLocal,
		BaseURL:  "http://localhost:11434/v1",
		Model:    "qwen2.5-coder:14b",
	}
}

// KimiCodeDefaults returns default settings for Kimi Code.
func KimiCodeDefaults() Config {
	return Config{
		Provider: ProviderKimiCode,
		BaseURL:  "https://api.kimi.com/coding/v1",
	}
}

// ZAIDefaults returns default settings for z.ai.
func ZAIDefaults() Config {
	return Config{
		Provider: ProviderZAI,
		BaseURL:  "https://api.z.ai/api/paas/v4",
	}
}

// configPath returns the path to config.json for the given project root.
func configPath(projectRoot string) string {
	return filepath.Join(projectRoot, "config.json")
}

// Load reads config from disk. Returns default config if file doesn't exist.
func Load(projectRoot string) (Config, error) {
	data, err := os.ReadFile(configPath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// Save writes config to disk atomically.
func Save(projectRoot string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	path := configPath(projectRoot)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return os.Rename(tmp, path)
}
