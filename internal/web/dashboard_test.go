package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"genesis/internal/core"
)

func TestNewDashboardServer(t *testing.T) {
	goals := core.NewGoalManager("/tmp/testgenesis")
	server := NewDashboardServer(goals)
	
	if server == nil {
		t.Fatal("NewDashboardServer returned nil")
	}
	
	if server.goals != goals {
		t.Error("DashboardServer goals not set correctly")
	}
}

func TestHandleIndex(t *testing.T) {
	goals := core.NewGoalManager("/tmp/testgenesis")
	server := NewDashboardServer(goals)
	
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	
	server.handleIndex(w, req)
	
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	
	if ct := resp.Header.Get("Content-Type"); ct != "text/html" {
		t.Errorf("expected text/html, got %s", ct)
	}
}

func TestHandleConstitutionNoEngine(t *testing.T) {
	goals := core.NewGoalManager("/tmp/testgenesis")
	server := NewDashboardServer(goals)
	
	req := httptest.NewRequest(http.MethodGet, "/api/constitution", nil)
	w := httptest.NewRecorder()
	
	server.handleConstitution(w, req)
	
	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", resp.StatusCode)
	}
}

func TestHandleMissionFileNoEngine(t *testing.T) {
	goals := core.NewGoalManager("/tmp/testgenesis")
	server := NewDashboardServer(goals)
	
	req := httptest.NewRequest(http.MethodGet, "/api/mission", nil)
	w := httptest.NewRecorder()
	
	server.handleMissionFile(w, req)
	
	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", resp.StatusCode)
	}
}

func TestHandleMissionFileTraversal(t *testing.T) {
	goals := core.NewGoalManager("/tmp/testgenesis")
	server := NewDashboardServer(goals)
	
	// Test directory traversal attempt
	req := httptest.NewRequest(http.MethodGet, "/api/mission?file=../../../etc/passwd", nil)
	w := httptest.NewRecorder()
	
	server.handleMissionFile(w, req)
	
	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		// Should fail due to nil engine first, but if engine existed, should be 400
		t.Errorf("expected status 503 (nil engine), got %d", resp.StatusCode)
	}
}