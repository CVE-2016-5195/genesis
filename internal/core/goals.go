package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// GoalStatus represents the lifecycle state of a goal.
type GoalStatus string

const (
	StatusPending    GoalStatus = "pending"
	StatusPlanning   GoalStatus = "planning"
	StatusInProgress GoalStatus = "in-progress"
	StatusCompleted  GoalStatus = "completed"
	StatusFailed     GoalStatus = "failed"
)

// Goal represents a single user-defined objective.
type Goal struct {
	ID          int        `json:"id"`
	Description string     `json:"description"`
	Status      GoalStatus `json:"status"`
	Generation  int        `json:"generation"`         // generation when created
	Approach    string     `json:"approach,omitempty"` // selected implementation approach
}

// GoalManager handles loading, saving, and querying goals.
type GoalManager struct {
	mu       sync.Mutex
	goals    []Goal
	filePath string
	nextID   int
}

// NewGoalManager creates a GoalManager backed by the given JSON file.
func NewGoalManager(projectRoot string) *GoalManager {
	fp := filepath.Join(projectRoot, "mission", "active.json")
	gm := &GoalManager{filePath: fp}
	gm.load()
	return gm
}

// load reads goals from disk. If the file doesn't exist, starts empty.
func (gm *GoalManager) load() {
	data, err := os.ReadFile(gm.filePath)
	if err != nil {
		gm.goals = []Goal{}
		gm.nextID = 1
		return
	}
	var goals []Goal
	if err := json.Unmarshal(data, &goals); err != nil {
		gm.goals = []Goal{}
		gm.nextID = 1
		return
	}
	gm.goals = goals
	maxID := 0
	for _, g := range goals {
		if g.ID > maxID {
			maxID = g.ID
		}
	}
	gm.nextID = maxID + 1
}

// save writes all goals to disk atomically.
func (gm *GoalManager) save() error {
	if err := os.MkdirAll(filepath.Dir(gm.filePath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(gm.goals, "", "  ")
	if err != nil {
		return err
	}
	tmp := gm.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, gm.filePath)
}

// AddGoal creates a new goal with planning status and persists it.
func (gm *GoalManager) AddGoal(description string, generation int) (Goal, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g := Goal{
		ID:          gm.nextID,
		Description: description,
		Status:      StatusPlanning,
		Generation:  generation,
	}
	gm.nextID++
	gm.goals = append(gm.goals, g)
	return g, gm.save()
}

// SetStatus updates the status of a goal by ID.
func (gm *GoalManager) SetStatus(id int, status GoalStatus) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	for i := range gm.goals {
		if gm.goals[i].ID == id {
			gm.goals[i].Status = status
			return gm.save()
		}
	}
	return fmt.Errorf("goal %d not found", id)
}

// AllGoals returns a copy of all goals.
func (gm *GoalManager) AllGoals() []Goal {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	out := make([]Goal, len(gm.goals))
	copy(out, gm.goals)
	return out
}

// HasPendingOrInProgress returns true if any goal is pending, planning, or in-progress.
func (gm *GoalManager) HasPendingOrInProgress() bool {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	for _, g := range gm.goals {
		if g.Status == StatusPending || g.Status == StatusPlanning || g.Status == StatusInProgress {
			return true
		}
	}
	return false
}

// PendingGoals returns all goals with pending status.
func (gm *GoalManager) PendingGoals() []Goal {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	var out []Goal
	for _, g := range gm.goals {
		if g.Status == StatusPending {
			out = append(out, g)
		}
	}
	return out
}

// InProgressGoals returns all goals with in-progress status.
func (gm *GoalManager) InProgressGoals() []Goal {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	var out []Goal
	for _, g := range gm.goals {
		if g.Status == StatusInProgress {
			out = append(out, g)
		}
	}
	return out
}

// Count returns the total number of goals.
func (gm *GoalManager) Count() int {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	return len(gm.goals)
}

// SetApproach sets the approach for a goal by ID and transitions it to in-progress.
func (gm *GoalManager) SetApproach(id int, approach string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	for i := range gm.goals {
		if gm.goals[i].ID == id {
			gm.goals[i].Approach = approach
			gm.goals[i].Status = StatusInProgress
			return gm.save()
		}
	}
	return fmt.Errorf("goal %d not found", id)
}

// PlanningGoals returns all goals with planning status.
func (gm *GoalManager) PlanningGoals() []Goal {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	var out []Goal
	for _, g := range gm.goals {
		if g.Status == StatusPlanning {
			out = append(out, g)
		}
	}
	return out
}

// NeedsPlanning returns true if any goal is in planning status.
func (gm *GoalManager) NeedsPlanning() bool {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	for _, g := range gm.goals {
		if g.Status == StatusPlanning {
			return true
		}
	}
	return false
}

// GoalsSummary returns a formatted string of all goals.
func (gm *GoalManager) GoalsSummary() string {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if len(gm.goals) == 0 {
		return "  (no goals)"
	}
	s := ""
	for _, g := range gm.goals {
		marker := " "
		switch g.Status {
		case StatusCompleted:
			marker = "+"
		case StatusInProgress:
			marker = ">"
		case StatusPlanning:
			marker = "?"
		case StatusFailed:
			marker = "!"
		}
		s += fmt.Sprintf("  [%s] #%d: %s (%s)\n", marker, g.ID, g.Description, g.Status)
		if g.Approach != "" {
			s += fmt.Sprintf("       Approach: %s\n", g.Approach)
		}
	}
	return s
}
