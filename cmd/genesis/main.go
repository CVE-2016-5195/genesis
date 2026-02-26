package main

import (
	"fmt"
	"os"
	"path/filepath"

	"genesis/internal/configure"
	"genesis/internal/core"
)

func main() {
	projectRoot, err := findProjectRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Cannot find project root: %v\n", err)
		os.Exit(1)
	}

	// Handle subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "configure":
			configure.Run(projectRoot)
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
			fmt.Fprintf(os.Stderr, "Usage: genesis [configure]\n")
			os.Exit(1)
		}
	}

	engine := core.NewEngine(projectRoot)
	engine.Run()
}

// findProjectRoot walks up from the executable (or working directory)
// looking for go.mod to determine the project root.
func findProjectRoot() (string, error) {
	// First try: directory of the running binary
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		if _, err := os.Stat(filepath.Join(exeDir, "go.mod")); err == nil {
			return exeDir, nil
		}
	}

	// Second try: working directory
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	// Walk up from working directory
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return wd, nil
}
