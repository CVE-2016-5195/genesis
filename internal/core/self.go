package core

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
