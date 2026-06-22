package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// toolContent is one item of an MCP tools/call result.
type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// Job is the compact job record returned by the domain tools and get_status.
type Job struct {
	ID        string          `json:"job_id"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	Done      bool            `json:"done"`
	Outputs   map[string]any  `json:"outputs"`
	StatusURL string          `json:"status_url"`
	Error     json.RawMessage `json:"error"`
}

// Terminal reports whether the job has reached a final state.
func (j Job) Terminal() bool {
	switch j.Status {
	case "succeeded", "failed", "cancelled":
		return true
	}
	return j.Done
}

// ResultURL returns the primary output URL, trying the known output keys.
func (j Job) ResultURL() string {
	for _, k := range []string{"video_url", "image_url", "audio_url", "url", "result_url", "out"} {
		if v, ok := j.Outputs[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// CallTool invokes an MCP tool and returns the inner JSON payload the tool
// produced (the text of its first content item). A tool-level error (isError
// or a "Error: …" text) is surfaced as a Go error so callers see server hints.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	raw, err := c.Call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	var tr toolResult
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("decode tool result: %w", err)
	}
	if len(tr.Content) == 0 {
		return nil, fmt.Errorf("tool %q returned no content", name)
	}
	text := tr.Content[0].Text
	if tr.IsError || strings.HasPrefix(text, "Error:") {
		return nil, fmt.Errorf("%s", strings.TrimSpace(strings.TrimPrefix(text, "Error:")))
	}
	return json.RawMessage(text), nil
}

// Submit calls a domain tool once and returns the (possibly non-terminal) job
// without polling. Used by the interactive UI, which drives polling itself.
func (c *Client) Submit(ctx context.Context, tool string, args map[string]any) (Job, error) {
	raw, err := c.CallTool(ctx, tool, args)
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return Job{}, fmt.Errorf("decode job: %w", err)
	}
	return j, nil
}

// GetStatus polls a job by id.
func (c *Client) GetStatus(ctx context.Context, jobID string) (Job, error) {
	raw, err := c.CallTool(ctx, "get_status", map[string]any{"job_id": jobID})
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return Job{}, fmt.Errorf("decode job: %w", err)
	}
	return j, nil
}

// Generate submits a generation job and blocks until it reaches a terminal
// state, polling get_status as needed. progress (optional) is called with each
// status update so a UI can render a spinner.
func (c *Client) Generate(ctx context.Context, tool string, args map[string]any, progress func(Job)) (Job, error) {
	raw, err := c.CallTool(ctx, tool, args)
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return Job{}, fmt.Errorf("decode job: %w", err)
	}
	if progress != nil {
		progress(j)
	}
	if j.Terminal() || j.ID == "" {
		return j, nil
	}
	// Poll until terminal.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return j, ctx.Err()
		case <-ticker.C:
			st, err := c.GetStatus(ctx, j.ID)
			if err != nil {
				return j, err
			}
			j = st
			if progress != nil {
				progress(j)
			}
			if j.Terminal() {
				return j, nil
			}
		}
	}
}

// Balance returns the current credit balance via the billing tool.
func (c *Client) Balance(ctx context.Context) (json.RawMessage, error) {
	return c.CallTool(ctx, "billing", map[string]any{"action": "balance"})
}

// Plan returns the current subscription plan summary.
func (c *Client) Plan(ctx context.Context) (json.RawMessage, error) {
	return c.CallTool(ctx, "billing", map[string]any{"action": "plan"})
}

// Plans lists the available subscription steps.
func (c *Client) Plans(ctx context.Context) (json.RawMessage, error) {
	return c.CallTool(ctx, "billing", map[string]any{"action": "plans"})
}
