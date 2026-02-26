package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"genesis/internal/config"
)

// Mutation describes a single code change proposed by the LLM.
type Mutation struct {
	File    string `json:"file"`
	Action  string `json:"action"` // "replace", "append", "create", "delete"
	Content string `json:"content"`
	// For replace action: the old content to find and replace
	OldContent string `json:"old_content,omitempty"`
}

// NewTool describes a new tool the agent wants to create.
type NewTool struct {
	Name        string `json:"name"`
	Package     string `json:"package"`
	Description string `json:"description"`
	Code        string `json:"code"`
}

// MutationPlan is the structured response from the LLM.
type MutationPlan struct {
	Reasoning                  string     `json:"reasoning"`
	Mutations                  []Mutation `json:"mutations"`
	NewTools                   []NewTool  `json:"new_tools"`
	FitnessImprovementEstimate float64    `json:"fitness_improvement_estimate"`
}

// ApproachOption describes one possible implementation approach proposed by the LLM.
type ApproachOption struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Pros        []string `json:"pros"`
	Cons        []string `json:"cons"`
}

// ApproachResponse is the structured JSON the LLM returns for approach planning.
type ApproachResponse struct {
	Approaches []ApproachOption `json:"approaches"`
}

// Client talks to an OpenAI-compatible LLM backend.
type Client struct {
	BaseURL  string // e.g. "http://localhost:11434/v1" or "https://api.z.ai/api/paas/v4"
	APIKey   string // empty for local, required for Kimi Code and z.ai
	Model    string
	Provider config.ProviderType // used for provider-specific headers
	Timeout  time.Duration
}

// NewClient creates a client from a config.
func NewClient(cfg config.Config) *Client {
	return &Client{
		BaseURL:  cfg.BaseURL,
		APIKey:   cfg.APIKey,
		Model:    cfg.Model,
		Provider: cfg.Provider,
		Timeout:  600 * time.Second,
	}
}

// setProviderHeaders sets provider-specific HTTP headers on a request.
func (c *Client) setProviderHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	// Kimi Code requires a specific User-Agent or returns 403.
	if c.Provider == config.ProviderKimiCode {
		req.Header.Set("User-Agent", "KimiCLI/1.0")
	}
}

// ---- OpenAI-compatible chat completions types ----

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature,omitempty"`
	ResponseFormat *respFormat   `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type respFormat struct {
	Type string `json:"type"` // "json_object"
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ModelsResponse is the response from GET /models.
type ModelsResponse struct {
	Data []ModelInfo `json:"data"`
}

// ModelInfo describes a single model from the /models endpoint.
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// systemPromptTemplate is the core system prompt for Genesis-HS.
const systemPromptTemplate = `You are Genesis-HS, a human-steered self-improving Go agent.
Current goals: %s
Selected approach: %s
Current version: %d
Fitness score: %.2f

You must output ONLY valid JSON with this exact schema:
{
  "reasoning": "your step-by-step reasoning",
  "mutations": [
    {"file": "relative/path.go", "action": "replace|append|create|delete", "content": "full file content or new content", "old_content": "content to find (for replace only)"}
  ],
  "new_tools": [
    {"name": "tool_name", "package": "toolpkg", "description": "what it does", "code": "full Go source"}
  ],
  "fitness_improvement_estimate": 12.5
}

Rules:
- All file paths are relative to the project root.
- For "create" action: content is the full file content.
- For "append" action: content is appended to the file.
- For "replace" action: old_content is found and replaced with content.
- For "delete" action: the file is removed.
- Follow the selected approach when implementing changes.
- Focus on the highest-priority pending or in-progress goal.
- Make small, incremental, testable changes.
- Ensure all Go code compiles. Use only stdlib unless adding a dependency is clearly justified.
- Never output anything except valid JSON.`

// RequestMutationPlans asks the LLM for N candidate mutation plans in parallel.
func (c *Client) RequestMutationPlans(goals string, approach string, version int, fitness float64, sourceContext string, n int) ([]MutationPlan, error) {
	systemPrompt := fmt.Sprintf(systemPromptTemplate, goals, approach, version, fitness)

	userPrompt := fmt.Sprintf(`Here is the current source tree:

%s

Generate a mutation plan to advance the highest-priority goal. Make practical, compilable changes.`, sourceContext)

	type result struct {
		index int
		plan  MutationPlan
		err   error
	}

	results := make(chan result, n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			temp := 0.7 + float64(idx)*0.1
			fmt.Printf("[llm] Candidate %d: requesting (temp=%.1f)...\n", idx, temp)
			plan, err := c.chatCompletion(systemPrompt, userPrompt, temp)
			if err != nil {
				fmt.Printf("[llm] Candidate %d: failed: %v\n", idx, err)
			} else {
				fmt.Printf("[llm] Candidate %d: received response\n", idx)
			}
			results <- result{index: idx, plan: plan, err: err}
		}(i)
	}

	var plans []MutationPlan
	for i := 0; i < n; i++ {
		r := <-results
		if r.err != nil {
			continue
		}
		plans = append(plans, r.plan)
	}

	if len(plans) == 0 {
		return nil, fmt.Errorf("all %d candidates failed", n)
	}

	return plans, nil
}

// chatCompletion sends a single chat completion request and parses the JSON response.
func (c *Client) chatCompletion(systemPrompt, userPrompt string, temperature float64) (MutationPlan, error) {
	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: temperature,
		ResponseFormat: &respFormat{
			Type: "json_object",
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return MutationPlan{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return MutationPlan{}, fmt.Errorf("create request: %w", err)
	}
	c.setProviderHeaders(req)

	httpClient := &http.Client{Timeout: c.Timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return MutationPlan{}, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return MutationPlan{}, fmt.Errorf("LLM returned %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return MutationPlan{}, fmt.Errorf("decode response: %w", err)
	}

	if chatResp.Error != nil {
		return MutationPlan{}, fmt.Errorf("LLM error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return MutationPlan{}, fmt.Errorf("LLM returned no choices")
	}

	content := chatResp.Choices[0].Message.Content

	var plan MutationPlan
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return MutationPlan{}, fmt.Errorf("parse mutation plan: %w (raw: %s)", err, content)
	}

	return plan, nil
}

// Ping checks if the LLM endpoint is reachable by hitting GET /models.
func (c *Client) Ping() error {
	_, err := c.ListModels()
	return err
}

// ListModels fetches available models from the endpoint.
func (c *Client) ListModels() ([]ModelInfo, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setProviderHeaders(req)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LLM not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LLM returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var modelsResp ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}

	return modelsResp.Data, nil
}

// approachSystemPrompt is the system prompt for the planning phase.
const approachSystemPrompt = `You are Genesis-HS, a human-steered self-improving Go agent.
You are in PLANNING mode. The user has a goal, and you must propose exactly 4 different
implementation approaches for achieving it.

Constraints:
- The project is a pure Go binary (stdlib only, no external deps unless strongly justified).
- All approaches must be feasible within a single Go binary.
- Each approach should be meaningfully different from the others.
- Be creative but practical.

You must output ONLY valid JSON with this exact schema:
{
  "approaches": [
    {
      "title": "Short title (3-7 words)",
      "description": "2-4 sentence description of the approach, what it builds, how it works",
      "pros": ["advantage 1", "advantage 2"],
      "cons": ["disadvantage 1", "disadvantage 2"]
    }
  ]
}

Output exactly 4 approaches. Never output anything except valid JSON.`

// RequestApproachOptions asks the LLM to propose 4 implementation approaches for a goal.
func (c *Client) RequestApproachOptions(goalDescription string) ([]ApproachOption, error) {
	userPrompt := fmt.Sprintf(`Goal: %s

Propose 4 different implementation approaches for this goal. Each should be a meaningfully different strategy.`, goalDescription)

	resp, err := c.chatCompletionRaw(approachSystemPrompt, userPrompt, 0.8)
	if err != nil {
		return nil, fmt.Errorf("approach request: %w", err)
	}

	var parsed ApproachResponse
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		return nil, fmt.Errorf("parse approach response: %w (raw: %s)", err, resp)
	}

	if len(parsed.Approaches) == 0 {
		return nil, fmt.Errorf("LLM returned no approaches")
	}

	return parsed.Approaches, nil
}

// chatCompletionRaw sends a chat completion and returns the raw content string.
func (c *Client) chatCompletionRaw(systemPrompt, userPrompt string, temperature float64) (string, error) {
	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: temperature,
		ResponseFormat: &respFormat{
			Type: "json_object",
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	c.setProviderHeaders(req)

	httpClient := &http.Client{Timeout: c.Timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("LLM returned %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("LLM error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("LLM returned no choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}
