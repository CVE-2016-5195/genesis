package core

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"genesis/internal/config"
	"genesis/internal/evaluator"
	"genesis/internal/forger"
	"genesis/internal/llm"
)

// Mode represents the agent's operating mode.
type Mode int

const (
	ModeForge  Mode = iota // Self-improvement loop active
	ModeListen             // Waiting for user input
)

func (m Mode) String() string {
	if m == ModeForge {
		return "Forge"
	}
	return "Listen"
}

const (
	// NumCandidates is the number of parallel mutation candidates per generation.
	NumCandidates = 4
	// ImprovementThreshold is the minimum % improvement required to accept a candidate.
	// Set to 0 so any positive improvement is accepted (stagnation detection handles stuck loops).
	ImprovementThreshold = 0.0
	// StagnationLimit is the number of consecutive failed iterations before prompting the user.
	StagnationLimit = 3
	// InitialGoal is the first goal created on a fresh start.
	InitialGoal = "Build a user interaction interface for Genesis-HS that allows users to interact with the agent, view status, submit goals, and monitor evolution progress."
)

// Engine is the central controller for Genesis-HS.
type Engine struct {
	ProjectRoot   string
	Goals         *GoalManager
	FitnessHist   *FitnessHistory
	LLM           *llm.Client
	Config        config.Config
	Generation    int
	Mode          Mode
	mu            sync.Mutex
	EventCallback func(msg string) // Called to broadcast events to web dashboard
}

// NewEngine creates a new engine rooted at the given project directory.
func NewEngine(projectRoot string) *Engine {
	cfg, err := config.Load(projectRoot)
	if err != nil {
		fmt.Printf("[genesis] WARNING: Could not load config: %v (using defaults)\n", err)
		cfg = config.DefaultConfig()
	}

	return &Engine{
		ProjectRoot: projectRoot,
		Goals:       NewGoalManager(projectRoot),
		FitnessHist: NewFitnessHistory(projectRoot),
		LLM:         llm.NewClient(cfg),
		Config:      cfg,
		Generation:  1,
	}
}

// Run is the main entry point. It seeds the first goal if needed,
// then enters the appropriate mode.
func (e *Engine) Run() {
	e.printBanner()

	// Seed initial goal on first run
	if e.Goals.Count() == 0 {
		fmt.Println("[genesis] First run detected. Creating initial goal...")
		if _, err := e.Goals.AddGoal(InitialGoal, e.Generation); err != nil {
			fmt.Printf("[genesis] ERROR: Failed to create initial goal: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[genesis] Goal #1 created (needs planning).\n")
	}

	// Planning phase: if any goal needs an approach, run interactive planning
	if e.Goals.NeedsPlanning() {
		fmt.Println("[genesis] Goals need planning. Running planning phase...")
		if err := e.runPlanningPhase(); err != nil {
			fmt.Printf("[genesis] Planning error: %v\n", err)
			fmt.Println("[genesis] Switching to Listen Mode.")
			e.Mode = ModeListen
			e.runListenMode()
			return
		}
	}

	// Determine mode based on goal status
	if e.Goals.HasPendingOrInProgress() {
		e.Mode = ModeForge
		fmt.Printf("[genesis] Active goals detected. Entering Forge Mode.\n")
		e.runForgeMode()
	} else {
		e.Mode = ModeListen
		fmt.Printf("[genesis] All goals completed. Entering Listen Mode.\n")
		e.runListenMode()
	}
}

func (e *Engine) printBanner() {
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════════╗")
	fmt.Println("  ║     GENESIS-HS  v0.1                  ║")
	fmt.Println("  ║     Human-Steered Self-Improving Go   ║")
	fmt.Printf("  ║     Generation: %-22d ║\n", e.Generation)
	fmt.Println("  ╚═══════════════════════════════════════╝")
	fmt.Println()
}

// runPlanningPhase handles interactive approach selection for goals in planning status.
func (e *Engine) runPlanningPhase() error {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		planning := e.Goals.PlanningGoals()
		if len(planning) == 0 {
			return nil
		}

		goal := planning[0]
		fmt.Println()
		fmt.Printf("[planning] Goal #%d: %s\n", goal.ID, goal.Description)
		fmt.Println("[planning] Asking LLM to propose implementation approaches...")
		fmt.Println("[planning] (This may take a few minutes with reasoning models...)")
		fmt.Println()

		approaches, err := e.LLM.RequestApproachOptions(goal.Description)
		if err != nil {
			return fmt.Errorf("LLM approach request for goal #%d: %w", goal.ID, err)
		}

		// Display approaches
		fmt.Println("[planning] ════════════════════════════════════════")
		fmt.Printf("[planning] %d approaches proposed for Goal #%d:\n", len(approaches), goal.ID)
		fmt.Println("[planning] ════════════════════════════════════════")
		fmt.Println()

		for i, a := range approaches {
			fmt.Printf("  [%d] %s\n", i+1, a.Title)
			fmt.Printf("      %s\n", a.Description)
			if len(a.Pros) > 0 {
				fmt.Printf("      Pros:  %s\n", strings.Join(a.Pros, ", "))
			}
			if len(a.Cons) > 0 {
				fmt.Printf("      Cons:  %s\n", strings.Join(a.Cons, ", "))
			}
			fmt.Println()
		}

		fmt.Printf("Pick an approach (1-%d), or 'r' to regenerate: ", len(approaches))

		if !scanner.Scan() {
			return fmt.Errorf("stdin closed during planning")
		}

		input := strings.TrimSpace(scanner.Text())

		if input == "r" || input == "R" {
			fmt.Println("[planning] Regenerating approaches...")
			continue
		}

		// Parse selection
		choice := 0
		if _, err := fmt.Sscanf(input, "%d", &choice); err != nil || choice < 1 || choice > len(approaches) {
			fmt.Printf("[planning] Invalid choice '%s'. Try again.\n", input)
			continue
		}

		selected := approaches[choice-1]
		approachText := fmt.Sprintf("%s: %s", selected.Title, selected.Description)

		fmt.Printf("[planning] Selected: %s\n", selected.Title)

		if err := e.Goals.SetApproach(goal.ID, approachText); err != nil {
			return fmt.Errorf("save approach for goal #%d: %w", goal.ID, err)
		}

		fmt.Printf("[planning] Goal #%d is now in-progress with approach set.\n", goal.ID)
	}
}

// runListenMode waits for user input on stdin.
func (e *Engine) runListenMode() {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("Goals:")
	fmt.Print(e.Goals.GoalsSummary())
	fmt.Println()
	fmt.Println("Commands: new goal: <description> | complete goal: <id> | goals | exit")
	fmt.Println()

	for {
		fmt.Print("genesis> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch {
		case strings.HasPrefix(input, "new goal:"):
			desc := strings.TrimSpace(strings.TrimPrefix(input, "new goal:"))
			if desc == "" {
				fmt.Println("  Usage: new goal: <description>")
				continue
			}
			goal, err := e.Goals.AddGoal(desc, e.Generation)
			if err != nil {
				fmt.Printf("  ERROR: %v\n", err)
				continue
			}
			fmt.Printf("  Goal #%d added: %s\n", goal.ID, desc)

			// Run planning phase for the new goal
			if e.Goals.NeedsPlanning() {
				fmt.Println("  Running planning phase...")
				if err := e.runPlanningPhase(); err != nil {
					fmt.Printf("  Planning error: %v\n", err)
					continue
				}
			}

			fmt.Println("  Switching to Forge Mode...")

			// Try to restart via binary; fall back to in-process forge
			binPath := filepath.Join(e.ProjectRoot, "genesis")
			if _, err := os.Stat(binPath); err == nil {
				if err := RestartSelf(e.ProjectRoot); err != nil {
					fmt.Printf("  [warn] Restart failed: %v. Running forge in-process.\n", err)
				}
			}
			// If RestartSelf succeeded, we never reach here.
			// Fall back to in-process forge mode.
			e.Mode = ModeForge
			e.runForgeMode()
			return

		case strings.HasPrefix(input, "complete goal:"):
			idStr := strings.TrimSpace(strings.TrimPrefix(input, "complete goal:"))
			var goalID int
			if _, err := fmt.Sscanf(idStr, "%d", &goalID); err != nil {
				fmt.Println("  Usage: complete goal: <id>")
				continue
			}
			if err := e.Goals.SetStatus(goalID, StatusCompleted); err != nil {
				fmt.Printf("  ERROR: %v\n", err)
			} else {
				fmt.Printf("  Goal #%d marked as completed.\n", goalID)
			}

		case input == "goals":
			fmt.Println()
			fmt.Println("Goals:")
			fmt.Print(e.Goals.GoalsSummary())
			fmt.Println()

		case input == "exit":
			fmt.Println("  Goodbye.")
			os.Exit(0)

		default:
			fmt.Println("  Unknown command. Try: new goal: <desc> | complete goal: <id> | goals | exit")
		}
	}
}

// runForgeMode runs the EvoLoop until all goals are completed.
func (e *Engine) runForgeMode() {
	fmt.Println()
	fmt.Println("[forge] ════════════════════════════════════")
	fmt.Println("[forge] EvoLoop starting...")
	fmt.Println("[forge] Goals:")
	fmt.Print(e.Goals.GoalsSummary())
	fmt.Println("[forge] ════════════════════════════════════")
	fmt.Println()
	if e.EventCallback != nil {
		e.EventCallback(`{"type": "evolution", "description": "Forge Mode started - Generation ` + fmt.Sprintf("%d", e.Generation) + `"}`)
	}

	// Mark first pending goal as in-progress (skip planning goals — they go through planning phase)
	pending := e.Goals.PendingGoals()
	if len(pending) > 0 {
		e.Goals.SetStatus(pending[0].ID, StatusInProgress)
	}

	// Check LLM availability
	if err := e.LLM.Ping(); err != nil {
		fmt.Printf("[forge] WARNING: LLM not available (%v)\n", err)
		fmt.Println("[forge] The EvoLoop requires an LLM backend.")
		fmt.Println("[forge] Run './genesis configure' to set up your LLM endpoint.")
		fmt.Printf("[forge] Current: %s (model: %s)\n", e.Config.BaseURL, e.Config.Model)
		fmt.Println()
		fmt.Println("[forge] Switching to Listen Mode (manual intervention needed).")

		// Mark goal back to pending
		inProg := e.Goals.InProgressGoals()
		for _, g := range inProg {
			e.Goals.SetStatus(g.ID, StatusPending)
		}

		e.Mode = ModeListen
		e.runListenMode()
		return
	}

	maxIterations := 50 // safety cap per forge session
	stagnationCount := 0
	for iter := 0; iter < maxIterations; iter++ {
		if !e.Goals.HasPendingOrInProgress() {
			fmt.Println("[forge] All goals completed!")
			break
		}

		fmt.Printf("[forge] === Generation %d, Iteration %d ===\n", e.Generation, iter+1)
		if e.EventCallback != nil {
			e.EventCallback(fmt.Sprintf(`{"type": "evolution", "description": "Starting iteration %d"}`, iter+1))
		}

		improved, err := e.runOneEvolution()
		if err != nil {
			fmt.Printf("[forge] Evolution error: %v\n", err)
			fmt.Println("[forge] Waiting 10s before retry...")
			time.Sleep(10 * time.Second)
			stagnationCount++
		} else if improved {
			stagnationCount = 0
			fmt.Printf("[forge] Generation %d -> %d (improvement accepted)\n", e.Generation, e.Generation+1)
			e.Generation++

			// The binary has been replaced; restart
			binPath := filepath.Join(e.ProjectRoot, "genesis")
			if _, err := os.Stat(binPath); err == nil {
				if err := RestartSelf(e.ProjectRoot); err != nil {
					fmt.Printf("[forge] Restart failed: %v (continuing in-process)\n", err)
				}
			}
		} else {
			stagnationCount++
			fmt.Printf("[forge] No improvement found (stagnation %d/%d).\n", stagnationCount, StagnationLimit)
		}

		// Stagnation detection: prompt user after N consecutive failures
		if stagnationCount >= StagnationLimit {
			action := e.handleStagnation()
			switch action {
			case "complete":
				// Mark all in-progress goals as completed
				for _, g := range e.Goals.InProgressGoals() {
					e.Goals.SetStatus(g.ID, StatusCompleted)
					fmt.Printf("[forge] Goal #%d marked complete by user.\n", g.ID)
				}
				stagnationCount = 0
			case "listen":
				fmt.Println("[forge] Switching to Listen Mode by user request.")
				e.Mode = ModeListen
				e.runListenMode()
				return
			case "continue":
				stagnationCount = 0
				fmt.Println("[forge] Continuing forge loop...")
			}

			if !e.Goals.HasPendingOrInProgress() {
				fmt.Println("[forge] All goals completed!")
				break
			}
		}

		if !improved {
			time.Sleep(2 * time.Second)
		}
	}

	// Switch to listen mode
	e.Mode = ModeListen
	fmt.Println("[genesis] Switching to Listen Mode.")
	e.runListenMode()
}

// handleStagnation prompts the user when the EvoLoop is stuck.
// Returns "complete", "listen", or "continue".
func (e *Engine) handleStagnation() string {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("[forge] ════════════════════════════════════════")
	fmt.Printf("[forge] Stagnation detected: %d consecutive iterations with no improvement.\n", StagnationLimit)
	fmt.Println("[forge] Current in-progress goals:")
	for _, g := range e.Goals.InProgressGoals() {
		fmt.Printf("[forge]   #%d: %s\n", g.ID, g.Description)
	}
	fmt.Println("[forge] ════════════════════════════════════════")
	fmt.Println()
	fmt.Println("  Options:")
	fmt.Println("    c) Mark current goal(s) as COMPLETE and move on")
	fmt.Println("    l) Switch to Listen Mode (pause forging)")
	fmt.Println("    r) Reset counter and keep trying")
	fmt.Println()

	for {
		fmt.Print("  Choice [c/l/r]: ")
		if !scanner.Scan() {
			return "listen"
		}
		input := strings.TrimSpace(strings.ToLower(scanner.Text()))
		switch input {
		case "c":
			return "complete"
		case "l":
			return "listen"
		case "r":
			return "continue"
		default:
			fmt.Println("  Invalid choice. Enter c, l, or r.")
		}
	}
}

// runOneEvolution runs a single EvoLoop iteration:
// 1. Read source tree
// 2. Evaluate current fitness
// 3. Request N candidate mutations from LLM
// 4. Build and evaluate each candidate in parallel
// 5. Select the fittest; apply if improvement >= threshold
func (e *Engine) runOneEvolution() (bool, error) {
	// Step 1: Read current source
	fmt.Println("[evo] Reading source tree...")
	sourceCtx, err := forger.ReadSourceTree(e.ProjectRoot)
	if err != nil {
		return false, fmt.Errorf("read source: %w", err)
	}

	// Step 2: Evaluate current fitness
	fmt.Println("[evo] Evaluating current fitness...")
	if e.EventCallback != nil {
		e.EventCallback(`{"type": "evolution", "description": "Evaluating current fitness"}`)
	}
	currentBin := filepath.Join(e.ProjectRoot, "genesis")

	// Build current if not built yet or if binary is empty (previous build failure)
	binInfo, statErr := os.Stat(currentBin)
	if os.IsNotExist(statErr) || (statErr == nil && binInfo.Size() == 0) {
		fmt.Println("[evo] Building current binary...")
		if err := BuildBinary(e.ProjectRoot, currentBin); err != nil {
			return false, fmt.Errorf("build current: %w", err)
		}
	}

	currentFitness := evaluator.Evaluate(e.ProjectRoot, currentBin)
	fmt.Printf("[evo] Current fitness: %.2f (%s)\n", currentFitness.Score, currentFitness.Details)

	// Record fitness history
	e.FitnessHist.Record(e.Generation, currentFitness.Score, currentFitness.Details)

	if e.EventCallback != nil {
		e.EventCallback(fmt.Sprintf(`{"type": "evolution", "description": "Current fitness: %.2f", "generation": %d, "score": %.2f}`, currentFitness.Score, e.Generation, currentFitness.Score))
	}

	// Step 3: Request mutation plans from LLM
	fmt.Printf("[evo] Requesting %d candidate mutation plans from LLM...\n", NumCandidates)
	goalsSummary := e.Goals.GoalsSummary()

	// Find the current in-progress goal's approach
	approach := ""
	inProgress := e.Goals.InProgressGoals()
	if len(inProgress) > 0 && inProgress[0].Approach != "" {
		approach = inProgress[0].Approach
	}

	plans, err := e.LLM.RequestMutationPlans(goalsSummary, approach, e.Generation, currentFitness.Score, sourceCtx, NumCandidates)
	if err != nil {
		return false, fmt.Errorf("llm request: %w", err)
	}
	fmt.Printf("[evo] Received %d candidate plans.\n", len(plans))

	// Step 4: Evaluate candidates in parallel
	type candidateResult struct {
		index   int
		plan    llm.MutationPlan
		fitness evaluator.FitnessResult
		srcDir  string
	}

	results := make(chan candidateResult, len(plans))
	var wg sync.WaitGroup

	for i, plan := range plans {
		wg.Add(1)
		go func(idx int, p llm.MutationPlan) {
			defer wg.Done()

			childDir := fmt.Sprintf("/tmp/genesis-child-%d", idx)
			os.RemoveAll(childDir) // clean slate
			os.MkdirAll(childDir, 0755)

			// Copy source tree
			if err := CopySourceTree(e.ProjectRoot, childDir); err != nil {
				fmt.Printf("[evo] Candidate %d: copy failed: %v\n", idx, err)
				return
			}

			// Apply mutations
			if err := forger.ApplyMutations(childDir, p.Mutations); err != nil {
				fmt.Printf("[evo] Candidate %d: mutation failed: %v\n", idx, err)
				return
			}

			// Create new tools if any
			for _, tool := range p.NewTools {
				if err := forger.CreateTool(childDir, tool); err != nil {
					fmt.Printf("[evo] Candidate %d: tool creation failed: %v\n", idx, err)
				}
			}

			// Build
			childBin := filepath.Join(childDir, "genesis")
			if err := BuildBinaryInDir(childDir, childBin); err != nil {
				fmt.Printf("[evo] Candidate %d: build failed: %v\n", idx, err)
				return
			}

			// Evaluate
			fitness := evaluator.Evaluate(childDir, childBin)
			fmt.Printf("[evo] Candidate %d: fitness=%.2f (%s)\n", idx, fitness.Score, fitness.Details)

			results <- candidateResult{
				index:   idx,
				plan:    p,
				fitness: fitness,
				srcDir:  childDir,
			}
		}(i, plan)
	}

	// Wait for all candidates to finish, then close channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// Step 5: Select fittest candidate
	var best *candidateResult
	for cr := range results {
		cr := cr
		if best == nil || cr.fitness.Score > best.fitness.Score {
			best = &cr
		}
	}

	if best == nil {
		if e.EventCallback != nil {
			e.EventCallback(`{"type": "evolution", "description": "No viable candidates produced"}`)
		}
		return false, fmt.Errorf("no viable candidates produced")
	}

	// Check improvement threshold
	improvement := 0.0
	if currentFitness.Score > 0 {
		improvement = ((best.fitness.Score - currentFitness.Score) / currentFitness.Score) * 100
	} else if best.fitness.Score > 0 {
		improvement = 100.0
	}

	fmt.Printf("[evo] Best candidate: #%d (fitness=%.2f, improvement=%.1f%%)\n",
		best.index, best.fitness.Score, improvement)
	fmt.Printf("[evo] Reasoning: %s\n", best.plan.Reasoning)
	if e.EventCallback != nil {
		e.EventCallback(fmt.Sprintf(`{"type": "evolution", "description": "Best candidate: fitness=%.2f, improvement=%.1f%%"}`, best.fitness.Score, improvement))
	}

	if improvement < ImprovementThreshold {
		// Clean up candidate dirs
		for i := 0; i < len(plans); i++ {
			os.RemoveAll(fmt.Sprintf("/tmp/genesis-child-%d", i))
		}
		if e.EventCallback != nil {
			e.EventCallback(fmt.Sprintf(`{"type": "evolution", "description": "Improvement %.1f%% below threshold, continuing..."}`, improvement))
		}
		return false, nil
	}

	// Step 6: Archive current version and apply the winner
	fmt.Println("[evo] Improvement accepted! Archiving current version...")
	if e.EventCallback != nil {
		e.EventCallback(fmt.Sprintf(`{"type": "evolution", "description": "Improvement accepted! %.1f%% - Deploying new generation"}`, improvement))
	}
	archivePath, err := ArchiveCurrentBinary(e.ProjectRoot, e.Generation)
	if err != nil {
		fmt.Printf("[evo] WARNING: Archive failed: %v\n", err)
	} else if archivePath != "" {
		fmt.Printf("[evo] Archived to: %s\n", archivePath)
	}

	// Replace source tree with the winning candidate
	fmt.Println("[evo] Applying winning mutations to source tree...")
	if err := forger.ApplyMutations(e.ProjectRoot, best.plan.Mutations); err != nil {
		return false, fmt.Errorf("apply winning mutations: %w", err)
	}

	// Create new tools in the main source tree
	for _, tool := range best.plan.NewTools {
		if err := forger.CreateTool(e.ProjectRoot, tool); err != nil {
			fmt.Printf("[evo] WARNING: Tool creation failed: %v\n", err)
		}
	}

	// Rebuild the main binary
	fmt.Println("[evo] Rebuilding main binary...")
	newBin := filepath.Join(e.ProjectRoot, "genesis.new")
	if err := BuildBinary(e.ProjectRoot, newBin); err != nil {
		return false, fmt.Errorf("rebuild after mutation: %w", err)
	}

	// Atomic replace
	if err := AtomicReplaceBinary(e.ProjectRoot, newBin); err != nil {
		return false, fmt.Errorf("atomic replace: %w", err)
	}
	os.Remove(newBin)

	// Clean up candidate dirs
	for i := 0; i < len(plans); i++ {
		os.RemoveAll(fmt.Sprintf("/tmp/genesis-child-%d", i))
	}

	fmt.Println("[evo] New generation deployed successfully!")
	return true, nil
}
