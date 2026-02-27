package configure

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"genesis/internal/config"
	"genesis/internal/llm"
)

// Run launches the interactive configuration menu.
func Run(projectRoot string) {
	scanner := bufio.NewScanner(os.Stdin)

	cfg, err := config.Load(projectRoot)
	if err != nil {
		fmt.Printf("Warning: could not load existing config: %v\n", err)
		cfg = config.DefaultConfig()
	}

	fmt.Println()
	fmt.Println("  ┌───────────────────────────────────┐")
	fmt.Println("  │     Genesis-HS Configuration       │")
	fmt.Println("  └───────────────────────────────────┘")
	fmt.Println()

	showCurrent(cfg)

	fmt.Println("  Select LLM provider:")
	fmt.Println()
	fmt.Println("    1) Local OpenAI-compatible (Ollama, LMStudio, vLLM, etc.)")
	fmt.Println("    2) Kimi Code (api.kimi.com)")
	fmt.Println("    3) z.ai (api.z.ai)")
	fmt.Println()

	choice := prompt(scanner, "  Choice [1/2/3]: ")

	switch choice {
	case "1":
		cfg = configureLocal(scanner, cfg)
	case "2":
		cfg = configureKimiCode(scanner, cfg)
	case "3":
		cfg = configureZAI(scanner, cfg)
	default:
		fmt.Println("  Invalid choice. Aborting.")
		return
	}

	if err := config.Save(projectRoot, cfg); err != nil {
		fmt.Printf("\n  ERROR: Failed to save config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("  Configuration saved!")
	fmt.Println()
	showCurrent(cfg)
}

func showCurrent(cfg config.Config) {
	fmt.Println("  Current configuration:")
	fmt.Printf("    Provider : %s\n", cfg.Provider)
	fmt.Printf("    Endpoint : %s\n", cfg.BaseURL)
	if cfg.APIKey != "" {
		fmt.Printf("    API Key  : %s\n", maskKey(cfg.APIKey))
	}
	fmt.Printf("    Model    : %s\n", cfg.Model)
	fmt.Println()
}

func configureLocal(scanner *bufio.Scanner, current config.Config) config.Config {
	cfg := config.Config{Provider: config.ProviderLocal}

	fmt.Println()
	fmt.Println("  Local OpenAI-compatible endpoint setup")
	fmt.Println()

	// If already configured for local, offer to keep current settings and skip to model
	if current.Provider == config.ProviderLocal && current.BaseURL != "" {
		cfg.BaseURL = current.BaseURL
		cfg.APIKey = current.APIKey

		if current.APIKey != "" {
			fmt.Printf("  Current endpoint: %s\n", current.BaseURL)
			fmt.Printf("  Current API key:  %s\n", maskKey(current.APIKey))
			fmt.Println()
			keep := prompt(scanner, "  Keep current endpoint and key? [Y/n]: ")
			if keep == "" || strings.ToLower(keep) == "y" || strings.ToLower(keep) == "yes" {
				fmt.Println("  Keeping current settings. Jumping to model selection...")
				return selectModelForConfig(scanner, cfg, current.Model)
			}
		}
	}

	defaultURL := "http://localhost:11434/v1"
	if current.Provider == config.ProviderLocal && current.BaseURL != "" {
		defaultURL = current.BaseURL
	}

	fmt.Printf("  Enter base URL (default: %s)\n", defaultURL)
	url := prompt(scanner, "  URL: ")
	if url == "" {
		url = defaultURL
	}
	// Normalize: strip trailing slash
	cfg.BaseURL = strings.TrimRight(url, "/")

	// Optional API key for local endpoints that require one
	fmt.Println()
	fmt.Println("  API key (leave empty if not needed):")
	key := prompt(scanner, "  Key: ")
	cfg.APIKey = key

	return selectModelForConfig(scanner, cfg, current.Model)
}

func configureKimiCode(scanner *bufio.Scanner, current config.Config) config.Config {
	cfg := config.KimiCodeDefaults()

	fmt.Println()
	fmt.Println("  Kimi Code setup")
	fmt.Println()

	// If already configured for Kimi with an API key, offer to keep it
	if current.Provider == config.ProviderKimiCode && current.APIKey != "" {
		fmt.Printf("  Current API key: %s\n", maskKey(current.APIKey))
		fmt.Println()
		keep := prompt(scanner, "  Keep current API key? [Y/n]: ")
		if keep == "" || strings.ToLower(keep) == "y" || strings.ToLower(keep) == "yes" {
			cfg.APIKey = current.APIKey
			fmt.Println("  Keeping current key. Jumping to model selection...")
			return selectModelForConfig(scanner, cfg, current.Model)
		}
	}

	// API key is required
	fmt.Println("  Enter your Kimi Code API key:")
	key := prompt(scanner, "  Key: ")
	if key == "" {
		fmt.Println("  API key is required for Kimi Code. Aborting.")
		os.Exit(1)
	}
	cfg.APIKey = key

	return selectModelForConfig(scanner, cfg, current.Model)
}

func configureZAI(scanner *bufio.Scanner, current config.Config) config.Config {
	cfg := config.ZAIDefaults()

	fmt.Println()
	fmt.Println("  z.ai setup")
	fmt.Println()

	// If already configured for z.ai with an API key, offer to keep it
	if current.Provider == config.ProviderZAI && current.APIKey != "" {
		fmt.Printf("  Current API key: %s\n", maskKey(current.APIKey))
		fmt.Println()
		keep := prompt(scanner, "  Keep current API key? [Y/n]: ")
		if keep == "" || strings.ToLower(keep) == "y" || strings.ToLower(keep) == "yes" {
			cfg.APIKey = current.APIKey
			fmt.Println("  Keeping current key. Jumping to model selection...")
			return selectModelForConfig(scanner, cfg, current.Model)
		}
	}

	// API key is required
	fmt.Println("  Enter your z.ai API key:")
	key := prompt(scanner, "  Key: ")
	if key == "" {
		fmt.Println("  API key is required for z.ai. Aborting.")
		os.Exit(1)
	}
	cfg.APIKey = key

	return selectModelForConfig(scanner, cfg, current.Model)
}

// selectModelForConfig connects to the endpoint, lists models, and lets the
// user pick one. Returns cfg with the Model field set.
func selectModelForConfig(scanner *bufio.Scanner, cfg config.Config, currentModel string) config.Config {
	fmt.Println()
	fmt.Println("  Connecting to endpoint...")

	client := llm.NewClient(cfg)
	models, err := client.ListModels()
	if err != nil {
		fmt.Printf("  Could not list models: %v\n", err)
		fmt.Println("  You can still enter a model name manually.")
		fmt.Println()

		defaultModel := currentModel
		if defaultModel == "" {
			defaultModel = "qwen2.5-coder:14b"
		}
		fmt.Printf("  Enter model name (default: %s)\n", defaultModel)
		model := prompt(scanner, "  Model: ")
		if model == "" {
			model = defaultModel
		}
		cfg.Model = model
		return cfg
	}

	cfg.Model = selectModel(scanner, models, currentModel)
	return cfg
}

func selectModel(scanner *bufio.Scanner, models []llm.ModelInfo, currentModel string) string {
	if len(models) == 0 {
		fmt.Println("  No models found at endpoint.")
		model := prompt(scanner, "  Enter model name: ")
		if model == "" {
			fmt.Println("  Model is required. Aborting.")
			os.Exit(1)
		}
		return model
	}

	fmt.Printf("\n  Available models (%d):\n\n", len(models))
	for i, m := range models {
		marker := "  "
		if m.ID == currentModel {
			marker = "* "
		}
		fmt.Printf("    %s%2d) %s\n", marker, i+1, m.ID)
	}
	fmt.Println()

	if currentModel != "" {
		fmt.Printf("  Current model: %s\n", currentModel)
	}

	input := prompt(scanner, "  Select model number: ")
	if input == "" {
		if currentModel != "" {
			fmt.Printf("  Keeping current model: %s\n", currentModel)
			return currentModel
		}
		// Default to first
		fmt.Printf("  Defaulting to: %s\n", models[0].ID)
		return models[0].ID
	}

	num, err := strconv.Atoi(input)
	if err != nil || num < 1 || num > len(models) {
		fmt.Printf("  Invalid selection. Defaulting to: %s\n", models[0].ID)
		return models[0].ID
	}

	selected := models[num-1].ID
	fmt.Printf("  Selected: %s\n", selected)
	return selected
}

func prompt(scanner *bufio.Scanner, label string) string {
	fmt.Print(label)
	if !scanner.Scan() {
		return ""
	}
	return strings.TrimSpace(scanner.Text())
}

// maskKey returns a masked version of an API key for display.
func maskKey(key string) string {
	if len(key) > 8 {
		return key[:4] + "..." + key[len(key)-4:]
	}
	return "****"
}
