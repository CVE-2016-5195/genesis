package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FitnessRecord stores a single generation's fitness score.
type FitnessRecord struct {
	Generation int       `json:"generation"`
	Score      float64   `json:"score"`
	Timestamp  time.Time `json:"timestamp"`
	Details    string    `json:"details,omitempty"`
}

// FitnessHistory tracks fitness scores over time.
type FitnessHistory struct {
	mu       sync.Mutex
	records  []FitnessRecord
	filePath string
}

// NewFitnessHistory creates a new history tracker.
func NewFitnessHistory(projectRoot string) *FitnessHistory {
	fh := &FitnessHistory{
		filePath: filepath.Join(projectRoot, "mission", "fitness.json"),
	}
	fh.load()
	return fh
}

// load reads history from disk.
func (fh *FitnessHistory) load() {
	data, err := os.ReadFile(fh.filePath)
	if err != nil {
		fh.records = []FitnessRecord{}
		return
	}
	var records []FitnessRecord
	if err := json.Unmarshal(data, &records); err != nil {
		fh.records = []FitnessRecord{}
		return
	}
	fh.records = records
}

// save writes history to disk atomically.
func (fh *FitnessHistory) save() error {
	if err := os.MkdirAll(filepath.Dir(fh.filePath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(fh.records, "", "  ")
	if err != nil {
		return err
	}
	tmp := fh.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, fh.filePath)
}

// Record adds a new fitness score to history.
func (fh *FitnessHistory) Record(generation int, score float64, details string) error {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	record := FitnessRecord{
		Generation: generation,
		Score:      score,
		Timestamp:  time.Now(),
		Details:    details,
	}
	fh.records = append(fh.records, record)
	return fh.save()
}

// GetHistory returns all recorded fitness scores.
func (fh *FitnessHistory) GetHistory() []FitnessRecord {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	out := make([]FitnessRecord, len(fh.records))
	copy(out, fh.records)
	return out
}

// GetLatest returns the most recent fitness record, or nil if empty.
func (fh *FitnessHistory) GetLatest() *FitnessRecord {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	if len(fh.records) == 0 {
		return nil
	}
	return &fh.records[len(fh.records)-1]
}

// String returns a formatted summary of fitness progression.
func (fh *FitnessHistory) String() string {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	if len(fh.records) == 0 {
		return "No fitness history recorded."
	}

	s := "Fitness History:\n"
	for _, r := range fh.records {
		s += fmt.Sprintf("  Gen %d: %.2f (%s)\n", r.Generation, r.Score, r.Timestamp.Format("15:04:05"))
	}
	
	if len(fh.records) >= 2 {
		first := fh.records[0].Score
		last := fh.records[len(fh.records)-1].Score
		change := ((last - first) / first) * 100
		s += fmt.Sprintf("\nTotal improvement: %.1f%%\n", change)
	}
	
	return s
}
