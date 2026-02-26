package forger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"genesis/internal/llm"
)

// ApplyMutations applies a list of mutations to a source tree rooted at dir.
func ApplyMutations(dir string, mutations []llm.Mutation) error {
	for i, m := range mutations {
		if err := applyOne(dir, m); err != nil {
			return fmt.Errorf("mutation %d (%s %s): %w", i, m.Action, m.File, err)
		}
	}
	return nil
}

func applyOne(dir string, m llm.Mutation) error {
	target := filepath.Join(dir, m.File)

	switch m.Action {
	case "create":
		return createFile(target, m.Content)
	case "append":
		return appendFile(target, m.Content)
	case "replace":
		return replaceInFile(target, m.OldContent, m.Content)
	case "delete":
		return os.Remove(target)
	default:
		return fmt.Errorf("unknown action: %s", m.Action)
	}
}

func createFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

func replaceInFile(path, old, new string) error {
	if old == "" {
		// If no old content specified, replace entire file
		return os.WriteFile(path, []byte(new), 0644)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	content := string(data)
	if !strings.Contains(content, old) {
		return fmt.Errorf("old_content not found in %s", path)
	}

	content = strings.Replace(content, old, new, 1)
	return os.WriteFile(path, []byte(content), 0644)
}

// CreateTool creates a new tool package under internal/tools/<name>.
func CreateTool(projectDir string, tool llm.NewTool) error {
	toolDir := filepath.Join(projectDir, "internal", "tools", tool.Package)
	if err := os.MkdirAll(toolDir, 0755); err != nil {
		return fmt.Errorf("create tool dir: %w", err)
	}

	toolFile := filepath.Join(toolDir, tool.Package+".go")
	return os.WriteFile(toolFile, []byte(tool.Code), 0644)
}

// ReadSourceTree reads all .go files and go.mod/go.sum in the project,
// returning a formatted string representation for the LLM.
func ReadSourceTree(projectRoot string) (string, error) {
	var sb strings.Builder

	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return err
		}

		// Skip non-essential directories
		if info.IsDir() {
			switch rel {
			case "archive", ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary and non-source files
		base := filepath.Base(rel)
		if base == "genesis" || base == "genesis.new" {
			return nil
		}

		// Include .go, go.mod, go.sum, .md, .json files
		ext := filepath.Ext(rel)
		include := ext == ".go" || ext == ".mod" || ext == ".sum" ||
			ext == ".md" || ext == ".json"
		if !include {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		sb.WriteString(fmt.Sprintf("=== %s ===\n", rel))
		sb.WriteString(string(data))
		sb.WriteString("\n\n")

		return nil
	})

	return sb.String(), err
}
