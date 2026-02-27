package core

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ArchiveCurrentBinary copies the running binary to the archive directory
// with a timestamp-based name for rollback purposes.
func ArchiveCurrentBinary(projectRoot string, generation int) (string, error) {
	archiveDir := filepath.Join(projectRoot, "archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return "", fmt.Errorf("create archive dir: %w", err)
	}

	binPath := filepath.Join(projectRoot, "genesis")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		// No binary to archive yet (running via go run)
		return "", nil
	}

	ts := time.Now().Format("20060102-150405")
	archiveName := fmt.Sprintf("genesis-gen%d-%s", generation, ts)
	archivePath := filepath.Join(archiveDir, archiveName)

	src, err := os.Open(binPath)
	if err != nil {
		return "", fmt.Errorf("open current binary: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return "", fmt.Errorf("create archive file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("copy binary: %w", err)
	}

	return archivePath, nil
}

// BuildBinary compiles the project and outputs the binary to the given path.
func BuildBinary(projectRoot, outputPath string) error {
	cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/genesis")
	cmd.Dir = projectRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build failed: %w\n%s", err, string(out))
	}
	return nil
}

// BuildBinaryInDir compiles a project in the given source directory.
func BuildBinaryInDir(srcDir, outputPath string) error {
	cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/genesis")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build failed: %w\n%s", err, string(out))
	}
	return nil
}

// AtomicReplaceBinary replaces the running binary with the new one.
// Uses rename for atomicity on the same filesystem.
func AtomicReplaceBinary(projectRoot, newBinaryPath string) error {
	targetPath := filepath.Join(projectRoot, "genesis")

	// Copy to a temp file next to the target first (same filesystem for rename).
	// Use .tmp suffix to avoid collision with genesis.new build output.
	tmpPath := targetPath + ".tmp"
	src, err := os.Open(newBinaryPath)
	if err != nil {
		return fmt.Errorf("open new binary: %w", err)
	}
	defer src.Close()

	// Verify the source is not empty
	srcInfo, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat new binary: %w", err)
	}
	if srcInfo.Size() == 0 {
		return fmt.Errorf("new binary is empty (0 bytes)")
	}

	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create temp binary: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("copy new binary: %w", err)
	}
	dst.Close()

	// Atomic rename
	if err := os.Rename(tmpPath, targetPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("atomic rename: %w", err)
	}

	return nil
}

// RestartSelf replaces the current process with the built binary.
// This never returns on success.
func RestartSelf(projectRoot string) error {
	binPath := filepath.Join(projectRoot, "genesis")

	// Verify the binary exists and is executable
	info, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("binary not found: %w", err)
	}
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("binary is not executable")
	}

	fmt.Println("[genesis] Restarting with new binary...")
	return syscall.Exec(binPath, []string{binPath}, os.Environ())
}

// CopySourceTree copies the entire project source to a destination directory,
// excluding the binary, archive, and temporary files.
func CopySourceTree(srcRoot, dstRoot string) error {
	return filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}

		// Skip non-source directories
		switch rel {
		case "archive", ".git":
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary files and temp files
		if !info.IsDir() {
			base := filepath.Base(rel)
			if base == "genesis" || base == "genesis.new" {
				return nil
			}
		}

		dstPath := filepath.Join(dstRoot, rel)

		if info.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}

		return copyFile(path, dstPath)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// VirginReset restores Genesis-HS to a clean state by removing all
// evolution artifacts, goals, fitness history, and archived binaries.
// Prompts the user for confirmation before proceeding.
func VirginReset(projectRoot string) {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("  ┌───────────────────────────────────────────┐")
	fmt.Println("  │        Genesis-HS — Virgin Reset           │")
	fmt.Println("  └───────────────────────────────────────────┘")
	fmt.Println()
	fmt.Println("  This will DELETE all evolution state:")
	fmt.Println()

	// Show what exists
	items := []struct {
		path string
		desc string
	}{
		{filepath.Join(projectRoot, "mission", "active.json"), "Goals & approaches"},
		{filepath.Join(projectRoot, "mission", "fitness.json"), "Fitness history"},
		{filepath.Join(projectRoot, "archive"), "Archived binaries"},
		{filepath.Join(projectRoot, "genesis"), "Compiled binary"},
		{filepath.Join(projectRoot, "config.json"), "LLM configuration"},
	}

	found := 0
	for _, item := range items {
		info, err := os.Stat(item.path)
		if err != nil {
			continue
		}
		found++
		if info.IsDir() {
			entries, _ := os.ReadDir(item.path)
			fmt.Printf("    - %s (%s, %d entries)\n", item.desc, item.path, len(entries))
		} else {
			fmt.Printf("    - %s (%s, %d bytes)\n", item.desc, item.path, info.Size())
		}
	}

	// Check for /tmp/genesis-child-* leftovers
	for i := 0; i < 10; i++ {
		childDir := fmt.Sprintf("/tmp/genesis-child-%d", i)
		if _, err := os.Stat(childDir); err == nil {
			found++
			fmt.Printf("    - Candidate work dir (%s)\n", childDir)
		}
	}

	if found == 0 {
		fmt.Println("    (nothing to clean — already in virgin state)")
		fmt.Println()
		return
	}

	fmt.Println()
	fmt.Print("  Keep LLM config (config.json)? [Y/n]: ")
	keepConfig := true
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer == "n" || answer == "no" {
			keepConfig = false
		}
	}

	fmt.Println()
	fmt.Println("  WARNING: This cannot be undone.")
	fmt.Print("  Type 'yes' to confirm virgin reset: ")
	if !scanner.Scan() {
		fmt.Println("  Aborted.")
		return
	}
	confirm := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if confirm != "yes" {
		fmt.Println("  Aborted.")
		return
	}

	fmt.Println()

	// Delete state files
	stateFiles := []string{
		filepath.Join(projectRoot, "mission", "active.json"),
		filepath.Join(projectRoot, "mission", "active.json.tmp"),
		filepath.Join(projectRoot, "mission", "fitness.json"),
		filepath.Join(projectRoot, "mission", "fitness.json.tmp"),
		filepath.Join(projectRoot, "genesis"),
		filepath.Join(projectRoot, "genesis.new"),
		filepath.Join(projectRoot, "genesis.tmp"),
	}

	if !keepConfig {
		stateFiles = append(stateFiles,
			filepath.Join(projectRoot, "config.json"),
			filepath.Join(projectRoot, "config.json.tmp"),
		)
	}

	for _, f := range stateFiles {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			fmt.Printf("  [warn] Could not remove %s: %v\n", f, err)
		} else if err == nil {
			fmt.Printf("  Removed: %s\n", f)
		}
	}

	// Delete archive directory contents
	archiveDir := filepath.Join(projectRoot, "archive")
	if entries, err := os.ReadDir(archiveDir); err == nil {
		for _, e := range entries {
			p := filepath.Join(archiveDir, e.Name())
			if err := os.Remove(p); err != nil {
				fmt.Printf("  [warn] Could not remove %s: %v\n", p, err)
			} else {
				fmt.Printf("  Removed: %s\n", p)
			}
		}
	}

	// Delete tmp directory contents
	tmpDir := filepath.Join(projectRoot, "tmp")
	if entries, err := os.ReadDir(tmpDir); err == nil {
		for _, e := range entries {
			p := filepath.Join(tmpDir, e.Name())
			os.RemoveAll(p)
		}
		fmt.Printf("  Cleaned: %s\n", tmpDir)
	}

	// Delete /tmp/genesis-child-* directories
	for i := 0; i < 10; i++ {
		childDir := fmt.Sprintf("/tmp/genesis-child-%d", i)
		if err := os.RemoveAll(childDir); err == nil {
			if _, statErr := os.Stat(childDir); statErr != nil {
				// was removed
			}
		}
	}

	fmt.Println()
	fmt.Println("  Virgin reset complete.")
	if keepConfig {
		fmt.Println("  LLM configuration preserved (config.json).")
	}

	// Auto-build the virgin binary
	fmt.Println()
	fmt.Println("  Building virgin binary...")
	binPath := filepath.Join(projectRoot, "genesis")
	if err := BuildBinary(projectRoot, binPath); err != nil {
		fmt.Printf("  [warn] Build failed: %v\n", err)
		fmt.Println("  You can build manually with: go build -o genesis ./cmd/genesis")
	} else {
		info, _ := os.Stat(binPath)
		fmt.Printf("  Built: %s (%d bytes)\n", binPath, info.Size())
	}

	fmt.Println()
	fmt.Println("  Run './genesis' to start fresh.")
	fmt.Println()
}
