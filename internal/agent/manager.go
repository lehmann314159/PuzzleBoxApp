package agent

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Config is all server-side. The page can choose only among Models.
type Config struct {
	SelfURL      string // this server's own /mcp
	Bearer       string // MCP_BEARER, if set
	OllamaURL    string
	Models       []string // allow-list; empty means "whatever Ollama reports"
	DefaultModel string
	MaxSteps     int // model turns per chat; a safety net
	Temperature  float64
	Pause        time.Duration // between scripted calls, so an audience can watch the box move
	MaxRunning   int
}

type Manager struct {
	Config
	Jobs   Jobs
	Ollama *Ollama

	mu       sync.Mutex
	tagsAt   time.Time
	tagCache []string
}

func New(c Config) *Manager {
	if c.MaxSteps <= 0 {
		c.MaxSteps = 30
	}
	m := &Manager{Config: c, Ollama: &Ollama{URL: c.OllamaURL, Temperature: c.Temperature}}
	m.Jobs.MaxRunning = c.MaxRunning
	return m
}

func (m *Manager) client(name string) *Client {
	return &Client{URL: m.SelfURL, Bearer: m.Bearer, Name: name}
}

// AvailableModels is the allow-list if one is set, else what Ollama has (cached for 30 seconds).
func (m *Manager) AvailableModels(ctx context.Context) ([]string, error) {
	if len(m.Models) > 0 {
		return m.Models, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.tagsAt) < 30*time.Second {
		return m.tagCache, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	names, err := m.Ollama.Models(ctx)
	if err != nil {
		return nil, err
	}
	m.tagCache, m.tagsAt = names, time.Now()
	return names, nil
}

// ResolveModel picks the requested model if it is allowed, else the default.
func (m *Manager) ResolveModel(ctx context.Context, requested string) (string, error) {
	models, err := m.AvailableModels(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot reach Ollama: %v", err)
	}
	if requested == "" {
		requested = m.DefaultModel
	}
	if requested == "" && len(models) > 0 {
		requested = models[0]
	}
	if requested == "" {
		return "", fmt.Errorf("Ollama has no models; pull one or set OLLAMA_MODEL")
	}
	if !slices.Contains(models, requested) && requested != m.DefaultModel {
		return "", fmt.Errorf("model %q is not available here", requested)
	}
	return requested, nil
}

// callTool calls a tool and, if the box pauses to ask a human (MRTR), puts the question to the person
// and retries with their answer and the server's requestState copied unchanged.
func (m *Manager) callTool(ctx context.Context, j *Job, c *Client, name string, args map[string]any) (map[string]any, error) {
	var extra map[string]any
	for range 3 {
		res, err := c.Tool(ctx, name, args, extra)
		if err != nil || res["resultType"] != "input_required" {
			return res, err
		}
		reqs, _ := res["inputRequests"].(map[string]any)
		prompt, answers := "The box asks for confirmation.", map[string]any{}
		for key, v := range reqs {
			if r, _ := v.(map[string]any); r != nil {
				if p, _ := r["params"].(map[string]any); p != nil {
					if msg, _ := p["message"].(string); msg != "" {
						prompt = msg
					}
				}
			}
			answers[key] = nil
		}
		approve, err := j.ask(ctx, prompt)
		if err != nil {
			return nil, err
		}
		action := "decline"
		if approve {
			action = "accept"
		}
		for key := range answers {
			answers[key] = map[string]any{"action": action, "content": map[string]any{"proceed": approve}}
		}
		extra = map[string]any{"inputResponses": answers, "requestState": res["requestState"]}
	}
	return nil, fmt.Errorf("the box kept asking for confirmation")
}

// sleep waits for the pause between scripted calls, or until the job is stopped.
func (m *Manager) sleep(ctx context.Context) error {
	select {
	case <-time.After(m.Pause):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
