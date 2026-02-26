package evaluator

import (
	"fmt"
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

	// Score: compilation is worth 40 points
	result.Score = 40.0

	// Check 2: Run go vet
	vetCmd := exec.Command("go", "vet", "./...")
	vetCmd.Dir = srcDir
	vetOut, vetErr := vetCmd.CombinedOutput()
	if vetErr == nil {
		result.Score += 15.0
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
		result.Score += 25.0
		details = append(details, "tests: passed")
	} else if strings.Contains(testOutput, "no test files") || strings.Contains(testOutput, "[no test files]") {
		// No tests is not a failure, but worth fewer points
		result.TestsPassed = true
		result.Score += 10.0
		details = append(details, "tests: none found (partial credit)")
	} else {
		details = append(details, fmt.Sprintf("tests: failed (%s)", strings.TrimSpace(testOutput)))
	}

	// Check 4: Source code quality heuristics
	qualityScore := evaluateCodeQuality(srcDir)
	result.Score += qualityScore
	details = append(details, fmt.Sprintf("code quality: +%.1f", qualityScore))

	// Check 5: Binary size penalty (prefer smaller binaries)
	// Baseline ~5MB, penalize over 20MB
	if result.BinarySize < 10*1024*1024 {
		result.Score += 5.0
		details = append(details, "binary size: good (<10MB)")
	} else if result.BinarySize < 20*1024*1024 {
		result.Score += 2.5
		details = append(details, "binary size: acceptable (<20MB)")
	}

	result.Details = strings.Join(details, "; ")
	return result
}

// evaluateCodeQuality does basic source analysis.
func evaluateCodeQuality(srcDir string) float64 {
	score := 0.0
	goFiles := 0
	totalLines := 0
	hasTests := false

	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		goFiles++
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

	// More Go files = more modular = better (up to a point)
	if goFiles >= 3 {
		score += 3.0
	} else if goFiles >= 2 {
		score += 1.5
	}

	// Having tests is good
	if hasTests {
		score += 5.0
	}

	// Reasonable total size
	if totalLines > 100 && totalLines < 10000 {
		score += 2.0
	}

	// Cap at 10
	if score > 10.0 {
		score = 10.0
	}

	return score
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
