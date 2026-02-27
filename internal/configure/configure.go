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
	showStoredProviders(cfg)

	fmt.Println("  Select LLM provider:")
	fmt.Println()
	fmt.Println("    1) Local OpenAI-compatible (Ollama, LMStudio, vLLM, etc.)")
	fmt.Println("    2) Kimi Code (api.kimi.com)")
	fmt.Println("    3) z.ai (api.z.ai)")
	fmt.Println()

	choice := prompt(scanner, "  Choice [1/2/3]: ")

	var provider config.ProviderType
	var pc config.ProviderConfig

	switch choice {
	case "1":
		provider = config.ProviderLocal
		pc = configureProvider(scanner, cfg, provider, "Local OpenAI-compatible", config.LocalDefaults())
	case "2":
		provider = config.ProviderKimiCode
		pc = configureProvider(scanner, cfg, provider, "Kimi Code", config.KimiCodeDefaults())
	case "3":
		provider = config.ProviderZAI
		pc = configureProvider(scanner, cfg, provider, "z.ai", config.ZAIDefaults())
	default:
		fmt.Println("  Invalid choice. Aborting.")
		return
	}

	cfg.SetProvider(provider, pc)

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
	pt, pc := cfg.ActiveConfig()
	fmt.Println("  Active configuration:")
	fmt.Printf("    Provider : %s\n", pt)
	fmt.Printf("    Endpoint : %s\n", pc.BaseURL)
	if pc.APIKey != "" {
		fmt.Printf("    API Key  : %s\n", maskKey(pc.APIKey))
	}
	fmt.Printf("    Model    : %s\n", pc.Model)
	fmt.Println()
}

func showStoredProviders(cfg config.Config) {
	if len(cfg.Providers) <= 1 {
		return
	}
	fmt.Println("  Stored providers:")
	for pt, pc := range cfg.Providers {
		active := " "
		if pt == cfg.Active {
			active = "*"
		}
		keyInfo := ""
		if pc.APIKey != "" {
			keyInfo = ", key: " + maskKey(pc.APIKey)
		}
		modelInfo := ""
		if pc.Model != "" {
			modelInfo = ", model: " + pc.Model
		}
		fmt.Printf("    %s %s (%s%s%s)\n", active, pt, pc.BaseURL, keyInfo, modelInfo)
	}
	fmt.Println()
}

// configureProvider handles setup for any provider type. It checks if there's
// a stored config for this provider and offers to reuse the API key.
func configureProvider(scanner *bufio.Scanner, cfg config.Config, provider config.ProviderType, label string, defaults config.ProviderConfig) config.ProviderConfig {
	fmt.Println()
	fmt.Printf("  %s setup\n", label)
	fmt.Println()

	// Check if we already have stored config for this provider
	stored, hasStored := cfg.GetProvider(provider)

	// Start from defaults, then overlay stored values
	pc := defaults
	if hasStored {
		if stored.BaseURL != "" {
			pc.BaseURL = stored.BaseURL
		}
		if stored.APIKey != "" {
			pc.APIKey = stored.APIKey
		}
		if stored.Model != "" {
			pc.Model = stored.Model
		}
	}

	// If we have a stored key, offer to keep everything and jump to model selection
	if pc.APIKey != "" {
		fmt.Printf("  Stored endpoint: %s\n", pc.BaseURL)
		fmt.Printf("  Stored API key:  %s\n", maskKey(pc.APIKey))
		if pc.Model != "" {
			fmt.Printf("  Stored model:    %s\n", pc.Model)
		}
		fmt.Println()
		keep := prompt(scanner, "  Keep stored settings? [Y/n]: ")
		if keep == "" || strings.ToLower(keep) == "y" || strings.ToLower(keep) == "yes" {
			fmt.Println("  Keeping stored settings. Jumping to model selection...")
			return selectModelForProvider(scanner, provider, pc)
		}
	} else if provider == config.ProviderLocal && pc.BaseURL != "" {
		// Local provider may not need a key — offer to keep the URL
		fmt.Printf("  Stored endpoint: %s\n", pc.BaseURL)
		fmt.Println()
		keep := prompt(scanner, "  Keep stored endpoint? [Y/n]: ")
		if keep == "" || strings.ToLower(keep) == "y" || strings.ToLower(keep) == "yes" {
			fmt.Println("  Keeping stored endpoint. Jumping to model selection...")
			return selectModelForProvider(scanner, provider, pc)
		}
	}

	// Prompt for base URL
	fmt.Printf("  Enter base URL (default: %s)\n", pc.BaseURL)
	url := prompt(scanner, "  URL: ")
	if url != "" {
		pc.BaseURL = strings.TrimRight(url, "/")
	}

	// Prompt for API key
	if provider == config.ProviderLocal {
		fmt.Println()
		fmt.Println("  API key (leave empty if not needed):")
		key := prompt(scanner, "  Key: ")
		pc.APIKey = key
	} else {
		fmt.Println()
		fmt.Printf("  Enter your %s API key:\n", label)
		key := prompt(scanner, "  Key: ")
		if key == "" {
			fmt.Printf("  API key is required for %s. Aborting.\n", label)
			os.Exit(1)
		}
		pc.APIKey = key
	}

	return selectModelForProvider(scanner, provider, pc)
}

// selectModelForProvider connects to the endpoint and lets the user pick a model.
func selectModelForProvider(scanner *bufio.Scanner, provider config.ProviderType, pc config.ProviderConfig) config.ProviderConfig {
	fmt.Println()
	fmt.Println("  Connecting to endpoint...")

	// Build a temporary Config to create the LLM client
	tmpCfg := config.Config{}
	tmpCfg.SetProvider(provider, pc)
	client := llm.NewClient(tmpCfg)

	models, err := client.ListModels()
	if err != nil {
		fmt.Printf("  Could not list models: %v\n", err)
		fmt.Println("  You can still enter a model name manually.")
		fmt.Println()

		defaultModel := pc.Model
		if defaultModel == "" {
			defaultModel = "qwen2.5-coder:14b"
		}
		fmt.Printf("  Enter model name (default: %s)\n", defaultModel)
		model := prompt(scanner, "  Model: ")
		if model == "" {
			model = defaultModel
		}
		pc.Model = model
		return pc
	}

	pc.Model = selectModel(scanner, models, pc.Model)
	return pc
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
