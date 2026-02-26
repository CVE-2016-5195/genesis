package evaluator

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FitnessResult holds the outcome of evaluating a candidate.
type FitnessResult struct {
	Score       float64
	Compiles    bool
	TestsPassed bool
	BuildTime   time.Duration
	BinarySize  int64
	Details     string
}

// Evaluate runs the fitness suite on a candidate in the given directory.
// The candidate binary should already be built at binPath.
func Evaluate(srcDir, binPath string) FitnessResult {
	result := FitnessResult{}
	var details []string

	// Check 1: Does the binary exist?
	info, err := os.Stat(binPath)
	if err != nil {
		result.Details = fmt.Sprintf("binary not found: %v", err)
		return result
	}
	result.BinarySize = info.Size()
	result.Compiles = true
	details = append(details, fmt.Sprintf("binary size: %d bytes", result.BinarySize))

	// Score: compilation is worth 30 points
	result.Score = 30.0

	// Check 2: Run go vet
	vetCmd := exec.Command("go", "vet", "./...")
	vetCmd.Dir = srcDir
	vetOut, vetErr := vetCmd.CombinedOutput()
	if vetErr == nil {
		result.Score += 10.0
		details = append(details, "go vet: passed")
	} else {
		details = append(details, fmt.Sprintf("go vet: failed (%s)", strings.TrimSpace(string(vetOut))))
	}

	// Check 3: Run tests if any exist
	testCmd := exec.Command("go", "test", "./...", "-count=1", "-timeout=30s")
	testCmd.Dir = srcDir
	testOut, testErr := testCmd.CombinedOutput()
	testOutput := string(testOut)

	if testErr == nil {
		result.TestsPassed = true
		result.Score += 20.0
		details = append(details, "tests: passed")
	} else if strings.Contains(testOutput, "no test files") || strings.Contains(testOutput, "[no test files]") {
		result.TestsPassed = true
		result.Score += 10.0
		details = append(details, "tests: none found (partial credit)")
	} else {
		details = append(details, fmt.Sprintf("tests: failed (%s)", strings.TrimSpace(testOutput)))
	}

	// Check 4: Source code quality and capability breadth (up to 30 points)
	qualityScore, qualityDetails := evaluateCodeQuality(srcDir)
	result.Score += qualityScore
	details = append(details, qualityDetails)

	// Check 5: Feature detection (up to 10 points)
	featureScore, featureDetails := evaluateFeatures(srcDir, binPath)
	result.Score += featureScore
	if featureDetails != "" {
		details = append(details, featureDetails)
	}

	result.Details = strings.Join(details, "; ")
	return result
}

// evaluateCodeQuality scores modular growth and code breadth.
// Scale: 0-30 points.
func evaluateCodeQuality(srcDir string) (float64, string) {
	score := 0.0
	goFiles := 0
	totalLines := 0
	hasTests := false
	packages := map[string]bool{}

	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		goFiles++
		packages[filepath.Dir(path)] = true

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Count(string(data), "\n")
		totalLines += lines

		if strings.HasSuffix(path, "_test.go") {
			hasTests = true
		}

		return nil
	})

	var parts []string

	// Package count: reward modular growth (1pt per package, up to 12)
	pkgScore := float64(len(packages))
	if pkgScore > 12.0 {
		pkgScore = 12.0
	}
	score += pkgScore
	parts = append(parts, fmt.Sprintf("packages=%d(+%.0f)", len(packages), pkgScore))

	// File count: reward more files (0.5pt per file, up to 8)
	fileScore := float64(goFiles) * 0.5
	if fileScore > 8.0 {
		fileScore = 8.0
	}
	score += fileScore
	parts = append(parts, fmt.Sprintf("files=%d(+%.1f)", goFiles, fileScore))

	// Tests bonus
	if hasTests {
		score += 5.0
		parts = append(parts, "has_tests(+5)")
	}

	// Reasonable total size
	if totalLines > 100 {
		lineScore := 2.0
		if totalLines > 500 {
			lineScore = 3.0
		}
		if totalLines > 2000 {
			lineScore = 5.0
		}
		if totalLines > 10000 {
			lineScore = 3.0 // too bloated
		}
		score += lineScore
		parts = append(parts, fmt.Sprintf("lines=%d(+%.0f)", totalLines, lineScore))
	}

	// Cap at 30
	if score > 30.0 {
		score = 30.0
	}

	return score, "quality: " + strings.Join(parts, ",")
}

// httpServerPatterns are strings that indicate actual HTTP server setup.
// Stored as concatenations so the evaluator's own source doesn't match.
var httpServerPatterns = []string{
	"http.Listen" + "AndServe",
	"http.Server" + "{",
	"http.Handle" + "Func",
	"http.Handle" + "(",
}

// evaluateFeatures detects specific capabilities in the binary.
// Scale: 0-10 points.
func evaluateFeatures(srcDir, binPath string) (float64, string) {
	score := 0.0
	var parts []string

	// Check for HTTP server capability (web dashboard / TUI)
	hasHTTP := false
	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		// Skip the evaluator itself
		if strings.HasSuffix(path, "evaluator.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		for _, pat := range httpServerPatterns {
			if strings.Contains(content, pat) {
				hasHTTP = true
				break
			}
		}
		return nil
	})

	if hasHTTP {
		score += 5.0
		parts = append(parts, "http_server(+5)")
	}

	// Quick smoke test: try running the binary briefly to see if it starts
	// and check if it opens a port
	if hasHTTP {
		if portOpen := quickPortCheck(binPath); portOpen {
			score += 5.0
			parts = append(parts, "port_active(+5)")
		}
	}

	if score > 10.0 {
		score = 10.0
	}

	if len(parts) == 0 {
		return score, ""
	}
	return score, "features: " + strings.Join(parts, ",")
}

// quickPortCheck tries to start the binary briefly and see if port 8080 opens.
// Returns quickly; this is a best-effort check.
func quickPortCheck(binPath string) bool {
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), "GENESIS_SMOKE_TEST=1")
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return false
	}

	// Give it a moment to start
	time.Sleep(500 * time.Millisecond)

	// Check if port 8080 is open
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8080", 500*time.Millisecond)
	if err == nil {
		conn.Close()
		cmd.Process.Kill()
		cmd.Wait()
		return true
	}

	cmd.Process.Kill()
	cmd.Wait()
	return false
}

// QuickBuildTest does a fast compile check without full evaluation.
func QuickBuildTest(srcDir string) error {
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build failed: %w\n%s", err, string(out))
	}
	return nil
}
