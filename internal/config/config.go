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

// ProviderConfig holds settings for a single LLM provider.
type ProviderConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
	Model   string `json:"model,omitempty"`
}

// Config holds all persistent configuration for Genesis-HS.
// Supports multiple provider configs so switching providers preserves API keys.
type Config struct {
	// Active selects which provider is currently in use.
	Active ProviderType `json:"active"`

	// Providers stores per-provider settings keyed by ProviderType.
	Providers map[ProviderType]ProviderConfig `json:"providers"`

	// Legacy flat fields — kept for backward-compatible JSON loading only.
	// New code should use ActiveConfig() instead.
	Provider ProviderType `json:"provider,omitempty"`
	BaseURL  string       `json:"base_url,omitempty"`
	APIKey   string       `json:"api_key,omitempty"`
	Model    string       `json:"model,omitempty"`
}

// ActiveConfig returns the ProviderConfig for the active provider,
// plus the active provider type. This is what the LLM client should use.
func (c Config) ActiveConfig() (ProviderType, ProviderConfig) {
	if c.Providers != nil {
		if pc, ok := c.Providers[c.Active]; ok {
			return c.Active, pc
		}
	}
	// Fallback to legacy flat fields
	return c.Active, ProviderConfig{
		BaseURL: c.BaseURL,
		APIKey:  c.APIKey,
		Model:   c.Model,
	}
}

// SetProvider updates (or creates) the config for a specific provider
// and makes it the active one.
func (c *Config) SetProvider(pt ProviderType, pc ProviderConfig) {
	if c.Providers == nil {
		c.Providers = make(map[ProviderType]ProviderConfig)
	}
	c.Providers[pt] = pc
	c.Active = pt
	// Keep legacy fields in sync for any code that reads them directly
	c.Provider = pt
	c.BaseURL = pc.BaseURL
	c.APIKey = pc.APIKey
	c.Model = pc.Model
}

// GetProvider returns the stored config for a given provider type, if any.
func (c Config) GetProvider(pt ProviderType) (ProviderConfig, bool) {
	if c.Providers == nil {
		return ProviderConfig{}, false
	}
	pc, ok := c.Providers[pt]
	return pc, ok
}

// DefaultConfig returns the default configuration (local Ollama).
func DefaultConfig() Config {
	cfg := Config{
		Active:    ProviderLocal,
		Providers: make(map[ProviderType]ProviderConfig),
	}
	cfg.Providers[ProviderLocal] = ProviderConfig{
		BaseURL: "http://localhost:11434/v1",
		Model:   "qwen2.5-coder:14b",
	}
	// Sync legacy fields
	cfg.Provider = ProviderLocal
	cfg.BaseURL = "http://localhost:11434/v1"
	cfg.Model = "qwen2.5-coder:14b"
	return cfg
}

// KimiCodeDefaults returns default settings for Kimi Code.
func KimiCodeDefaults() ProviderConfig {
	return ProviderConfig{
		BaseURL: "https://api.kimi.com/coding/v1",
	}
}

// ZAIDefaults returns default settings for z.ai.
func ZAIDefaults() ProviderConfig {
	return ProviderConfig{
		BaseURL: "https://api.z.ai/api/paas/v4",
	}
}

// LocalDefaults returns default settings for local.
func LocalDefaults() ProviderConfig {
	return ProviderConfig{
		BaseURL: "http://localhost:11434/v1",
	}
}

// configPath returns the path to config.json for the given project root.
func configPath(projectRoot string) string {
	return filepath.Join(projectRoot, "config.json")
}

// Load reads config from disk. Handles migration from the old flat format.
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

	// Migrate: old config has "provider" but no "active" or "providers" map
	if cfg.Providers == nil || len(cfg.Providers) == 0 {
		cfg.Providers = make(map[ProviderType]ProviderConfig)

		// The old flat fields become an entry in the providers map
		provider := cfg.Provider
		if provider == "" {
			provider = cfg.Active
		}
		if provider == "" {
			provider = ProviderLocal
		}

		cfg.Providers[provider] = ProviderConfig{
			BaseURL: cfg.BaseURL,
			APIKey:  cfg.APIKey,
			Model:   cfg.Model,
		}
		cfg.Active = provider
		cfg.Provider = provider
	}

	// Ensure legacy fields are in sync with the active provider
	if cfg.Active != "" {
		if pc, ok := cfg.Providers[cfg.Active]; ok {
			cfg.Provider = cfg.Active
			cfg.BaseURL = pc.BaseURL
			cfg.APIKey = pc.APIKey
			cfg.Model = pc.Model
		}
	}

	return cfg, nil
}

// Save writes config to disk atomically. Clears legacy flat fields before
// writing so the JSON only contains the new multi-provider format.
func Save(projectRoot string, cfg Config) error {
	// Build a clean struct for serialization — only the new fields
	type cleanConfig struct {
		Active    ProviderType                    `json:"active"`
		Providers map[ProviderType]ProviderConfig `json:"providers"`
	}

	out := cleanConfig{
		Active:    cfg.Active,
		Providers: cfg.Providers,
	}

	data, err := json.MarshalIndent(out, "", "  ")
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
