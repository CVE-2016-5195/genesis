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
//
// Scoring breakdown (max 100):
//   - Compilation:  20 pts (binary exists and is non-empty)
//   - Go vet:        5 pts (passes go vet)
//   - Tests:        15 pts (pass=15, exist-but-fail=5, none=3)
//   - Code quality: 30 pts (packages, files, tests, lines, diversity)
//   - Features:     30 pts (http server, port, TUI, concurrency, config, CLI, etc.)
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

	// Score: compilation is worth 20 points
	result.Score = 20.0

	// Check 2: Run go vet (5 pts)
	vetCmd := exec.Command("go", "vet", "./...")
	vetCmd.Dir = srcDir
	vetOut, vetErr := vetCmd.CombinedOutput()
	if vetErr == nil {
		result.Score += 5.0
		details = append(details, "go vet: passed")
	} else {
		details = append(details, fmt.Sprintf("go vet: failed (%s)", strings.TrimSpace(string(vetOut))))
	}

	// Check 3: Run tests (15 pts)
	testCmd := exec.Command("go", "test", "./...", "-count=1", "-timeout=30s")
	testCmd.Dir = srcDir
	testOut, testErr := testCmd.CombinedOutput()
	testOutput := string(testOut)

	if testErr == nil {
		result.TestsPassed = true
		result.Score += 15.0
		details = append(details, "tests: passed")
	} else if strings.Contains(testOutput, "no test files") || strings.Contains(testOutput, "[no test files]") {
		result.TestsPassed = true
		result.Score += 3.0
		details = append(details, "tests: none found (minimal credit)")
	} else {
		result.Score += 5.0
		details = append(details, fmt.Sprintf("tests: failed (%s)", strings.TrimSpace(testOutput)))
	}

	// Check 4: Source code quality (up to 30 points)
	qualityScore, qualityDetails := evaluateCodeQuality(srcDir)
	result.Score += qualityScore
	details = append(details, qualityDetails)

	// Check 5: Feature detection (up to 30 points)
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
//   - Packages: 0.5pt each, max 8
//   - Files: 0.25pt each, max 5
//   - Test files present: +3
//   - Test file count: 0.5pt per test file, max 4
//   - Lines of code: tiered, max 5
//   - Import diversity: stdlib packages used, max 5
func evaluateCodeQuality(srcDir string) (float64, string) {
	score := 0.0
	goFiles := 0
	testFiles := 0
	totalLines := 0
	packages := map[string]bool{}
	stdlibImports := map[string]bool{}

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
		content := string(data)
		lines := strings.Count(content, "\n")
		totalLines += lines

		if strings.HasSuffix(path, "_test.go") {
			testFiles++
		}

		// Count stdlib import diversity
		for _, imp := range []string{
			"net/http", "encoding/json", "sync", "os/exec",
			"html/template", "crypto", "database/sql",
			"io/fs", "path/filepath", "regexp", "sort",
			"context", "embed", "reflect", "testing",
		} {
			// Use concatenation to avoid self-matching
			if strings.Contains(content, "\""+imp+"\"") {
				stdlibImports[imp] = true
			}
		}

		return nil
	})

	var parts []string

	// Package count: 0.5pt per package, up to 8
	pkgScore := float64(len(packages)) * 0.5
	if pkgScore > 8.0 {
		pkgScore = 8.0
	}
	score += pkgScore
	parts = append(parts, fmt.Sprintf("packages=%d(+%.1f)", len(packages), pkgScore))

	// File count: 0.25pt per file, up to 5
	fileScore := float64(goFiles) * 0.25
	if fileScore > 5.0 {
		fileScore = 5.0
	}
	score += fileScore
	parts = append(parts, fmt.Sprintf("files=%d(+%.1f)", goFiles, fileScore))

	// Tests present bonus
	if testFiles > 0 {
		score += 3.0
		parts = append(parts, "has_tests(+3)")

		// Extra credit for more test files
		testExtra := float64(testFiles) * 0.5
		if testExtra > 4.0 {
			testExtra = 4.0
		}
		score += testExtra
		parts = append(parts, fmt.Sprintf("test_files=%d(+%.1f)", testFiles, testExtra))
	}

	// Lines of code: tiered
	lineScore := 0.0
	if totalLines > 100 {
		lineScore = 1.0
	}
	if totalLines > 500 {
		lineScore = 2.0
	}
	if totalLines > 2000 {
		lineScore = 3.0
	}
	if totalLines > 5000 {
		lineScore = 5.0
	}
	if totalLines > 15000 {
		lineScore = 3.0 // too bloated
	}
	score += lineScore
	parts = append(parts, fmt.Sprintf("lines=%d(+%.0f)", totalLines, lineScore))

	// Import diversity: 0.5pt per unique stdlib import, up to 5
	impScore := float64(len(stdlibImports)) * 0.5
	if impScore > 5.0 {
		impScore = 5.0
	}
	score += impScore
	parts = append(parts, fmt.Sprintf("imports=%d(+%.1f)", len(stdlibImports), impScore))

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
// Scale: 0-30 points.
//   - HTTP server:   4 pts
//   - Port active:   4 pts
//   - TUI:           4 pts
//   - Concurrency:   4 pts (goroutines, channels, sync primitives)
//   - Config system: 3 pts (JSON config load/save)
//   - CLI subcommands: 3 pts (os.Args parsing or flag package)
//   - Logging:       2 pts (log package or structured logging)
//   - Error handling: 3 pts (custom error types, error wrapping)
//   - File I/O:      3 pts (file read/write beyond config)
func evaluateFeatures(srcDir, binPath string) (float64, string) {
	score := 0.0
	var parts []string

	// Collect all Go source content (excluding evaluator itself)
	var allContent string
	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if strings.HasSuffix(path, "evaluator.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		allContent += string(data) + "\n"
		return nil
	})

	// HTTP server capability
	hasHTTP := false
	for _, pat := range httpServerPatterns {
		if strings.Contains(allContent, pat) {
			hasHTTP = true
			break
		}
	}
	if hasHTTP {
		score += 4.0
		parts = append(parts, "http_server(+4)")
	}

	// Port active (smoke test)
	if hasHTTP {
		if portOpen := quickPortCheck(binPath); portOpen {
			score += 4.0
			parts = append(parts, "port_active(+4)")
		}
	}

	// TUI capability
	tuiPatterns := []string{
		"terminal" + ".Clear",
		"ansi" + " escape",
		"tcell", "bubbletea", "tview", "termbox", "readline",
	}
	for _, pat := range tuiPatterns {
		if strings.Contains(strings.ToLower(allContent), pat) {
			score += 4.0
			parts = append(parts, "tui_detected(+4)")
			break
		}
	}

	// Concurrency patterns
	concurrencyPatterns := []string{"go func", "chan ", "sync.Mutex", "sync.WaitGroup", "sync.RWMutex"}
	concurrencyHits := 0
	for _, pat := range concurrencyPatterns {
		if strings.Contains(allContent, pat) {
			concurrencyHits++
		}
	}
	if concurrencyHits > 0 {
		concScore := float64(concurrencyHits)
		if concScore > 4.0 {
			concScore = 4.0
		}
		score += concScore
		parts = append(parts, fmt.Sprintf("concurrency(%d patterns,+%.0f)", concurrencyHits, concScore))
	}

	// Config system (JSON config load/save)
	if strings.Contains(allContent, "config.json") || strings.Contains(allContent, "json.Marshal") {
		score += 3.0
		parts = append(parts, "config_system(+3)")
	}

	// CLI subcommands
	if strings.Contains(allContent, "os.Args") || strings.Contains(allContent, "flag.") {
		score += 3.0
		parts = append(parts, "cli_args(+3)")
	}

	// Logging
	if strings.Contains(allContent, "log.Print") || strings.Contains(allContent, "log.Fatal") || strings.Contains(allContent, "log.New") {
		score += 2.0
		parts = append(parts, "logging(+2)")
	}

	// Error handling quality (fmt.Errorf with %w = error wrapping)
	if strings.Contains(allContent, "fmt.Errorf") && strings.Contains(allContent, "%w") {
		score += 3.0
		parts = append(parts, "error_wrapping(+3)")
	}

	// File I/O beyond config
	fileIOPatterns := []string{"os.ReadFile", "os.WriteFile", "os.Create", "os.Open"}
	fileIOHits := 0
	for _, pat := range fileIOPatterns {
		if strings.Contains(allContent, pat) {
			fileIOHits++
		}
	}
	if fileIOHits >= 2 {
		score += 3.0
		parts = append(parts, "file_io(+3)")
	}

	if score > 30.0 {
		score = 30.0
	}

	if len(parts) == 0 {
		return score, ""
	}
	return score, "features: " + strings.Join(parts, ",")
}

// quickPortCheck tries to start the binary briefly and see if it opens a port.
// Checks common ports. Returns quickly; this is a best-effort check.
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

	// Check common ports
	ports := []string{"8080", "8081", "8090", "3000", "9000", "8000"}
	found := false
	for _, port := range ports {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			found = true
			break
		}
	}

	cmd.Process.Kill()
	cmd.Wait()
	return found
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
