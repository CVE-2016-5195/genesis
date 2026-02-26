package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"genesis/internal/core"
)

// DashboardServer serves the web interface and handles SSE.
type DashboardServer struct {
	goals     *core.GoalManager
	engine    *core.Engine
	clients   map[chan string]bool
	clientsMu sync.RWMutex
	tmpl      *template.Template
}

// NewDashboardServer creates a new dashboard server.
func NewDashboardServer(goals *core.GoalManager) *DashboardServer {
	return &DashboardServer{
		goals:   goals,
		clients: make(map[chan string]bool),
		tmpl:    template.Must(template.New("dashboard").Parse(dashboardTemplate)),
	}
}

// SetEngine sets the engine reference for status access.
func (s *DashboardServer) SetEngine(engine *core.Engine) {
	s.engine = engine
}

// Start begins listening on the given address.
func (s *DashboardServer) Start(addr string) error {
	http.HandleFunc("/", s.handleIndex)
	http.HandleFunc("/api/status", s.handleStatus)
	http.HandleFunc("/api/goals", s.handleGoals)
	http.HandleFunc("/api/archive", s.handleArchive)
	http.HandleFunc("/api/events", s.handleEvents)
	http.HandleFunc("/api/constitution", s.handleConstitution)
	http.HandleFunc("/api/mission", s.handleMissionFile)
	return http.ListenAndServe(addr, nil)
}

// BroadcastEvent sends a message to all connected SSE clients.
func (s *DashboardServer) BroadcastEvent(msg string) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
			// Channel full, skip
		}
	}
}

func (s *DashboardServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	ch := make(chan string, 10)
	s.clientsMu.Lock()
	s.clients[ch] = true
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ch)
		s.clientsMu.Unlock()
		close(ch)
	}()

	for {
		select {
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *DashboardServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	mode := "Unknown"
	generation := 0
	var fitness []core.FitnessRecord
	if s.engine != nil {
		mode = s.engine.Mode.String()
		generation = s.engine.Generation
		fitness = s.engine.FitnessHist.GetHistory()
	}
	
	status := map[string]interface{}{
		"goals":      s.goals.AllGoals(),
		"mode":       mode,
		"generation": generation,
		"fitness":    fitness,
	}
	json.NewEncoder(w).Encode(status)
}

func (s *DashboardServer) handleArchive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	type archiveItem struct {
		Name string `json:"name"`
	}
	
	var archives []archiveItem
	if s.engine != nil {
		archiveDir := filepath.Join(s.engine.ProjectRoot, "archive")
		entries, err := os.ReadDir(archiveDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					archives = append(archives, archiveItem{
						Name: entry.Name(),
					})
				}
			}
		}
	}
	
	json.NewEncoder(w).Encode(archives)
}

func (s *DashboardServer) handleGoals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	desc := r.FormValue("description")
	if desc == "" {
		http.Error(w, "Description required", http.StatusBadRequest)
		return
	}

	gen := 1
	if s.engine != nil {
		gen = s.engine.Generation
	}

	_, err := s.goals.AddGoal(desc, gen)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.BroadcastEvent(fmt.Sprintf(`{"type": "goal_added", "description": %q}`, desc))
	w.WriteHeader(http.StatusCreated)
}

func (s *DashboardServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	s.tmpl.Execute(w, nil)
}

func (s *DashboardServer) handleConstitution(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown")
	if s.engine == nil {
		http.Error(w, "Engine not available", http.StatusServiceUnavailable)
		return
	}
	path := filepath.Join(s.engine.ProjectRoot, "mission", "constitution.md")
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "Constitution not found", http.StatusNotFound)
		return
	}
	w.Write(data)
}

func (s *DashboardServer) handleMissionFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.engine == nil {
		http.Error(w, "Engine not available", http.StatusServiceUnavailable)
		return
	}
	
	file := r.URL.Query().Get("file")
	if file == "" {
		file = "active.json"
	}
	
	if strings.Contains(file, "..") || strings.Contains(file, "/") || strings.Contains(file, "\\") {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}
	
	path := filepath.Join(s.engine.ProjectRoot, "mission", file)
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	w.Write(data)
}

const dashboardTemplate = `<!DOCTYPE html>
<html>
<head>
    <title>Genesis-HS Dashboard</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 20px; background: #1a1a2e; color: #eee; line-height: 1.6; }
        .container { max-width: 1200px; margin: 0 auto; }
        .header { border-bottom: 2px solid #16213e; padding-bottom: 10px; margin-bottom: 20px; }
        .status { background: #16213e; padding: 15px; border-radius: 8px; margin-bottom: 20px; display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; }
        .status-item { background: #0f3460; padding: 10px; border-radius: 5px; }
        .status-label { font-size: 0.85em; color: #7ec8e3; text-transform: uppercase; }
        .status-value { font-size: 1.2em; font-weight: bold; color: #e94560; }
        .goals { background: #16213e; padding: 15px; border-radius: 8px; margin-bottom: 20px; }
        .goal { margin: 10px 0; padding: 12px; background: #1a1a2e; border-left: 4px solid #e94560; border-radius: 4px; }
        .goal.pending { border-left-color: #ffc107; }
        .goal.in-progress { border-left-color: #4fbdba; }
        .goal.completed { border-left-color: #4caf50; }
        .goal.planning { border-left-color: #9c27b0; }
        .approach { margin-top: 8px; padding: 8px; background: #0f3460; border-radius: 4px; font-size: 0.9em; color: #b8c5d6; }
        .archive { background: #16213e; padding: 15px; border-radius: 8px; margin-bottom: 20px; }
        .archive-list { max-height: 200px; overflow-y: auto; }
        .archive-item { padding: 5px 0; border-bottom: 1px solid #0f3460; font-family: monospace; font-size: 0.9em; }
        .events { background: #0a0a14; padding: 15px; border-radius: 8px; height: 300px; overflow-y: auto; font-family: 'Courier New', monospace; font-size: 13px; border: 1px solid #16213e; }
        .event { margin: 3px 0; padding: 4px; border-radius: 3px; }
        .event.info { color: #7ec8e3; }
        .event.evolution { color: #4fbdba; }
        .event.goal { color: #e94560; }
        .fitness-chart { background: #16213e; padding: 15px; border-radius: 8px; margin-bottom: 20px; }
        .chart-container { position: relative; height: 200px; margin-top: 10px; }
        svg.chart { width: 100%; height: 100%; }
        .chart-line { fill: none; stroke: #4fbdba; stroke-width: 2; }
        .chart-area { fill: rgba(79, 189, 186, 0.1); stroke: none; }
        .chart-point { fill: #e94560; }
        .chart-grid { stroke: #0f3460; stroke-width: 1; stroke-dasharray: 4; }
        .chart-text { fill: #7ec8e3; font-size: 10px; }
        input[type="text"] { padding: 10px; margin: 5px 0; background: #0f3460; color: white; border: 1px solid #16213e; border-radius: 4px; width: 70%; font-size: 14px; }
        button { padding: 10px 20px; margin: 5px; background: #e94560; color: white; border: none; border-radius: 4px; cursor: pointer; font-size: 14px; }
        button:hover { background: #ff6b6b; }
        .badge { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 0.75em; margin-left: 10px; background: #0f3460; }
        .grid { display: grid; grid-template-columns: 2fr 1fr; gap: 20px; }
        @media (max-width: 768px) { .grid { grid-template-columns: 1fr; } }
        .stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 10px; margin-top: 10px; }
        .stat-box { background: #0f3460; padding: 10px; border-radius: 5px; text-align: center; }
        .stat-value { font-size: 1.5em; font-weight: bold; color: #4fbdba; }
        .stat-label { font-size: 0.8em; color: #7ec8e3; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Genesis-HS Dashboard</h1>
            <p>Human-Steered Self-Improving Agent</p>
        </div>
        
        <div class="status">
            <div class="status-item">
                <div class="status-label">Mode</div>
                <div class="status-value" id="mode">Loading...</div>
            </div>
            <div class="status-item">
                <div class="status-label">Generation</div>
                <div class="status-value" id="generation">-</div>
            </div>
            <div class="status-item">
                <div class="status-label">Active Goals</div>
                <div class="status-value" id="goal-count">-</div>
            </div>
            <div class="status-item">
                <div class="status-label">Current Fitness</div>
                <div class="status-value" id="current-fitness">-</div>
            </div>
        </div>
        
        <div class="fitness-chart">
            <h3>Fitness Progress</h3>
            <div class="chart-container">
                <svg class="chart" id="fitness-svg" viewBox="0 0 600 200" preserveAspectRatio="none">
                    <g id="chart-grid"></g>
                    <path id="chart-area" class="chart-area" />
                    <path id="chart-line" class="chart-line" />
                    <g id="chart-points"></g>
                </svg>
            </div>
            <div class="stats-grid" id="fitness-stats"></div>
        </div>
        
        <div class="grid">
            <div class="left-col">
                <div class="goals">
                    <h2>Submit New Goal</h2>
                    <form id="goal-form" onsubmit="submitGoal(event)">
                        <input type="text" id="goal-desc" placeholder="Enter goal description..." required>
                        <button type="submit">Add Goal</button>
                    </form>
                    <h3>Current Goals <span class="badge" id="goals-badge">0</span></h3>
                    <div id="goals-list">Loading...</div>
                </div>
                
                <div class="events">
                    <h3>Evolution Log</h3>
                    <div id="event-log"></div>
                </div>
            </div>
            
            <div class="right-col">
                <div class="archive">
                    <h3>Generation Archive</h3>
                    <div class="archive-list" id="archive-list">Loading...</div>
                </div>
            </div>
        </div>
    </div>
    
    <script>
        let fitnessHistory = [];
        
        const evtSource = new EventSource('/api/events');
        const log = document.getElementById('event-log');
        
        evtSource.onmessage = function(e) {
            const div = document.createElement('div');
            div.className = 'event info';
            try {
                const data = JSON.parse(e.data);
                div.textContent = '[' + new Date().toLocaleTimeString() + '] ' + (data.type || 'event').toUpperCase() + ': ' + (data.description || e.data);
                if (data.type === 'evolution') {
                    div.className = 'event evolution';
                    if (data.score !== undefined) {
                        fitnessHistory.push({generation: data.generation, score: data.score});
                        drawFitnessChart();
                        document.getElementById('current-fitness').textContent = data.score.toFixed(2);
                    }
                }
                if (data.type === 'goal_added') div.className = 'event goal';
            } catch {
                div.textContent = '[' + new Date().toLocaleTimeString() + '] ' + e.data;
            }
            log.appendChild(div);
            log.scrollTop = log.scrollHeight;
        };
        
        evtSource.onerror = function(e) {
            const div = document.createElement('div');
            div.className = 'event';
            div.style.color = '#ff6b6b';
            div.textContent = '[' + new Date().toLocaleTimeString() + '] Connection lost. Retrying...';
            log.appendChild(div);
            log.scrollTop = log.scrollHeight;
        };
        
        function drawFitnessChart() {
            if (fitnessHistory.length < 1) return;
            
            const svg = document.getElementById('fitness-svg');
            const width = 600, height = 200;
            const padding = {top: 20, right: 30, bottom: 30, left: 40};
            const chartW = width - padding.left - padding.right;
            const chartH = height - padding.top - padding.bottom;
            
            const minScore = Math.min(...fitnessHistory.map(d => d.score)) * 0.9;
            const maxScore = Math.max(...fitnessHistory.map(d => d.score)) * 1.1;
            const minGen = fitnessHistory[0].generation;
            const maxGen = fitnessHistory[fitnessHistory.length - 1].generation || minGen + 1;
            
            const xScale = (gen) => padding.left + ((gen - minGen) / (maxGen - minGen)) * chartW;
            const yScale = (score) => padding.top + chartH - ((score - minScore) / (maxScore - minScore)) * chartH;
            
            // Draw grid lines
            const gridGroup = document.getElementById('chart-grid');
            gridGroup.innerHTML = '';
            for (let i = 0; i <= 5; i++) {
                const y = padding.top + (i * chartH / 5);
                const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
                line.setAttribute('x1', padding.left);
                line.setAttribute('y1', y);
                line.setAttribute('x2', width - padding.right);
                line.setAttribute('y2', y);
                line.setAttribute('class', 'chart-grid');
                gridGroup.appendChild(line);
                
                const text = document.createElementNS('http://www.w3.org/2000/svg', 'text');
                text.setAttribute('x', padding.left - 5);
                text.setAttribute('y', y + 3);
                text.setAttribute('text-anchor', 'end');
                text.setAttribute('class', 'chart-text');
                text.textContent = (maxScore - (i * (maxScore - minScore) / 5)).toFixed(0);
                gridGroup.appendChild(text);
            }
            
            // Draw area under curve
            let areaPath = 'M' + xScale(fitnessHistory[0].generation) + ',' + (padding.top + chartH);
            fitnessHistory.forEach(d => {
                areaPath += ' L' + xScale(d.generation) + ',' + yScale(d.score);
            });
            areaPath += ' L' + xScale(fitnessHistory[fitnessHistory.length-1].generation) + ',' + (padding.top + chartH) + ' Z';
            document.getElementById('chart-area').setAttribute('d', areaPath);
            
            // Draw line
            let linePath = '';
            fitnessHistory.forEach((d, i) => {
                const cmd = i === 0 ? 'M' : 'L';
                linePath += cmd + xScale(d.generation) + ',' + yScale(d.score);
            });
            document.getElementById('chart-line').setAttribute('d', linePath);
            
            // Draw points
            const pointsGroup = document.getElementById('chart-points');
            pointsGroup.innerHTML = '';
            fitnessHistory.forEach(d => {
                const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
                circle.setAttribute('cx', xScale(d.generation));
                circle.setAttribute('cy', yScale(d.score));
                circle.setAttribute('r', 3);
                circle.setAttribute('class', 'chart-point');
                pointsGroup.appendChild(circle);
            });
            
            // Update stats
            const scores = fitnessHistory.map(d => d.score);
            const current = scores[scores.length - 1];
            const max = Math.max(...scores);
            const min = Math.min(...scores);
            const avg = scores.reduce((a,b) => a+b, 0) / scores.length;
            const improvement = scores.length > 1 ? ((current - scores[0]) / scores[0] * 100) : 0;
            
            document.getElementById('fitness-stats').innerHTML = 
                '<div class="stat-box"><div class="stat-value">' + current.toFixed(1) + '</div><div class="stat-label">Current</div></div>' +
                '<div class="stat-box"><div class="stat-value">' + max.toFixed(1) + '</div><div class="stat-label">Best</div></div>' +
                '<div class="stat-box"><div class="stat-value">' + avg.toFixed(1) + '</div><div class="stat-label">Average</div></div>' +
                '<div class="stat-box"><div class="stat-value">' + improvement.toFixed(1) + '%</div><div class="stat-label">Improvement</div></div>';
        }
        
        async function loadStatus() {
            try {
                const resp = await fetch('/api/status');
                const data = await resp.json();
                document.getElementById('mode').textContent = data.mode;
                document.getElementById('generation').textContent = data.generation;
                document.getElementById('goal-count').textContent = data.goals.filter(g => g.status !== 'completed').length;
                document.getElementById('goals-badge').textContent = data.goals.length;
                
                if (data.fitness && data.fitness.length > 0) {
                    fitnessHistory = data.fitness;
                    drawFitnessChart();
                    document.getElementById('current-fitness').textContent = data.fitness[data.fitness.length-1].score.toFixed(2);
                }
                
                let goalsHtml = '';
                data.goals.forEach(g => {
                    const statusClass = g.status.replace(/-/g, '');
                    let html = '<div class="goal ' + statusClass + '"><strong>#' + g.id + '</strong> ' + 
                                g.description + '<span class="badge">' + g.status + '</span>';
                    if (g.approach) {
                        html += '<div class="approach"><strong>Approach:</strong> ' + g.approach + '</div>';
                    }
                    html += '</div>';
                    goalsHtml += html;
                });
                document.getElementById('goals-list').innerHTML = goalsHtml || '<p>No active goals</p>';
            } catch (err) {
                console.error('Failed to load status:', err);
            }
        }
        
        async function loadArchive() {
            try {
                const resp = await fetch('/api/archive');
                const data = await resp.json();
                let html = '';
                if (data.length === 0) {
                    html = '<p>No archived generations</p>';
                } else {
                    data.forEach(item => {
                        html += '<div class="archive-item">' + item.name + '</div>';
                    });
                }
                document.getElementById('archive-list').innerHTML = html;
            } catch (err) {
                document.getElementById('archive-list').innerHTML = '<p>Error loading archive</p>';
            }
        }
        
        async function submitGoal(e) {
            e.preventDefault();
            const desc = document.getElementById('goal-desc').value;
            if (!desc) return;
            
            const formData = new FormData();
            formData.append('description', desc);
            
            try {
                await fetch('/api/goals', { method: 'POST', body: formData });
                document.getElementById('goal-desc').value = '';
                loadStatus();
            } catch (err) {
                alert('Failed to add goal: ' + err);
            }
        }
        
        loadStatus();
        loadArchive();
        setInterval(loadStatus, 5000);
    </script>
</body>
</html>`
