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
	fmt.Println()

	choice := prompt(scanner, "  Choice [1/2]: ")

	switch choice {
	case "1":
		cfg = configureLocal(scanner, cfg)
	case "2":
		cfg = configureKimiCode(scanner, cfg)
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
		masked := cfg.APIKey
		if len(masked) > 8 {
			masked = masked[:4] + "..." + masked[len(masked)-4:]
		}
		fmt.Printf("    API Key  : %s\n", masked)
	}
	fmt.Printf("    Model    : %s\n", cfg.Model)
	fmt.Println()
}

func configureLocal(scanner *bufio.Scanner, current config.Config) config.Config {
	cfg := config.Config{Provider: config.ProviderLocal}

	fmt.Println()
	fmt.Println("  Local OpenAI-compatible endpoint setup")
	fmt.Println()

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

	// Try to connect and list models
	fmt.Println()
	fmt.Println("  Connecting to endpoint...")

	client := llm.NewClient(cfg)
	models, err := client.ListModels()
	if err != nil {
		fmt.Printf("  Could not connect: %v\n", err)
		fmt.Println("  You can still enter a model name manually.")
		fmt.Println()

		defaultModel := "qwen2.5-coder:14b"
		if current.Model != "" {
			defaultModel = current.Model
		}
		fmt.Printf("  Enter model name (default: %s)\n", defaultModel)
		model := prompt(scanner, "  Model: ")
		if model == "" {
			model = defaultModel
		}
		cfg.Model = model
		return cfg
	}

	cfg.Model = selectModel(scanner, models, current.Model)
	return cfg
}

func configureKimiCode(scanner *bufio.Scanner, current config.Config) config.Config {
	cfg := config.KimiCodeDefaults()

	fmt.Println()
	fmt.Println("  Kimi Code setup")
	fmt.Println()

	// API key is required
	fmt.Println("  Enter your Kimi Code API key:")
	key := prompt(scanner, "  Key: ")
	if key == "" {
		fmt.Println("  API key is required for Kimi Code. Aborting.")
		os.Exit(1)
	}
	cfg.APIKey = key

	// Connect and list models
	fmt.Println()
	fmt.Println("  Connecting to Kimi Code...")

	client := llm.NewClient(cfg)
	models, err := client.ListModels()
	if err != nil {
		fmt.Printf("  Could not connect: %v\n", err)
		fmt.Println("  Please check your API key and try again.")
		fmt.Println()
		fmt.Println("  You can still enter a model name manually.")

		model := prompt(scanner, "  Model: ")
		if model == "" {
			fmt.Println("  Model is required. Aborting.")
			os.Exit(1)
		}
		cfg.Model = model
		return cfg
	}

	cfg.Model = selectModel(scanner, models, current.Model)
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
