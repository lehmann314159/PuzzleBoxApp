package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Message is one chat message in Ollama's /api/chat format.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

// ToolCall is a model's request to call a tool. Arguments arrive as an object or as a JSON string,
// depending on model and Ollama version, so it is decoded loosely.
type ToolCall struct {
	Function struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	} `json:"function"`
}

// Args returns the arguments as an object.
func (t ToolCall) Args() (map[string]any, error) {
	switch a := t.Function.Arguments.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return a, nil
	case string:
		if strings.TrimSpace(a) == "" {
			return map[string]any{}, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(a), &m); err != nil || m == nil {
			return nil, fmt.Errorf("arguments were not a JSON object: %q", truncate(a, 80))
		}
		return m, nil
	}
	return nil, fmt.Errorf("arguments were not a JSON object")
}

// Ollama talks to a server-configured Ollama. The page never chooses the URL.
type Ollama struct {
	URL         string
	Temperature float64
	MaxReply    int   // most tokens one reply may have, so one runaway call cannot hold the server; 0 means 512
	Think       *bool // nil leaves it to the model; false stops a reasoning model from thinking before it answers
	HTTP        *http.Client
}

// ParseThink reads OLLAMA_THINK: "0", "false" or "off" turn thinking off, "1", "true" or "on" turn it on,
// anything else (including empty) leaves the choice to the model.
func ParseThink(s string) *bool {
	var v bool
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "false", "off":
	case "1", "true", "on":
		v = true
	default:
		return nil
	}
	return &v
}

func (o *Ollama) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (o *Ollama) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(o.URL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("Ollama answered HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	return json.Unmarshal(raw, out)
}

// Chat asks the model for its next message. Streaming is off.
func (o *Ollama) Chat(ctx context.Context, model string, msgs []Message, tools []map[string]any) (Message, error) {
	var out struct {
		Message            Message `json:"message"`
		DoneReason         string  `json:"done_reason"`
		TotalDuration      int64   `json:"total_duration"`
		LoadDuration       int64   `json:"load_duration"`
		PromptEvalCount    int     `json:"prompt_eval_count"`
		PromptEvalDuration int64   `json:"prompt_eval_duration"`
		EvalCount          int     `json:"eval_count"`
		EvalDuration       int64   `json:"eval_duration"`
	}
	body := map[string]any{"model": model, "messages": msgs, "tools": tools,
		"stream": false, "options": map[string]any{"temperature": o.Temperature, "num_predict": cmp.Or(o.MaxReply, 512)}}
	if o.Think != nil {
		body["think"] = *o.Think
	}
	began := time.Now()
	err := o.do(ctx, "POST", "/api/chat", body, &out)
	if os.Getenv("OLLAMA_DEBUG") != "" { // what Ollama says about each call: tokens, cache use, and where the time went
		if err != nil {
			log.Printf("ollama %s: %d messages, failed after %.1fs: %v", model, len(msgs), time.Since(began).Seconds(), err)
		} else {
			log.Printf("ollama %s: %d messages, prompt %d tokens (%.1fs), wrote %d tokens (%.1fs), load %.1fs, total %.1fs, done=%s",
				model, len(msgs), out.PromptEvalCount, float64(out.PromptEvalDuration)/1e9, out.EvalCount, float64(out.EvalDuration)/1e9,
				float64(out.LoadDuration)/1e9, float64(out.TotalDuration)/1e9, out.DoneReason)
		}
	}
	return out.Message, err
}

// Models lists what the server has pulled.
func (o *Ollama) Models(ctx context.Context) ([]string, error) {
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := o.do(ctx, "GET", "/api/tags", nil, &out); err != nil {
		return nil, err
	}
	names := make([]string, len(out.Models))
	for i, m := range out.Models {
		names[i] = m.Name
	}
	return names, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
