package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"puzzleboxapp/internal/box"
	"puzzleboxapp/internal/mcp"
)

const (
	maxBody     = 1 << 16
	maxMessages = 20
	maxMsgChars = 4000
)

// Routes registers the pane's endpoints. The caller wraps them in the UI-token check.
func (m *Manager) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agent/config", m.config)
	mux.HandleFunc("GET /api/tools", m.listTools)
	mux.HandleFunc("POST /api/call", m.call)
	mux.HandleFunc("POST /api/chat", m.chat)
	mux.HandleFunc("POST /api/autopilot", m.autopilot)
	mux.HandleFunc("GET /api/job", m.job)
	mux.HandleFunc("POST /api/job/answer", m.answer)
	mux.HandleFunc("POST /api/job/stop", m.stop)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]any{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil && err != io.EOF {
		fail(w, 400, "bad JSON: "+err.Error())
		return false
	}
	return true
}

func (m *Manager) config(w http.ResponseWriter, r *http.Request) {
	agent := map[string]any{"enabled": false}
	if models, err := m.AvailableModels(r.Context()); err != nil {
		agent["reason"] = "Cannot reach Ollama: " + err.Error()
	} else if def, err := m.ResolveModel(r.Context(), ""); err != nil {
		agent["reason"] = err.Error()
	} else {
		agent["enabled"], agent["models"], agent["default"] = true, models, def
	}
	write(w, 200, map[string]any{"agent": agent, "max_steps": m.MaxSteps})
}

// listTools is static, so it is served in-process rather than traced as a request on every page load.
func (m *Manager) listTools(w http.ResponseWriter, r *http.Request) { write(w, 200, mcp.Tools()) }

// call is Manual mode: one tool call, forwarded to /mcp. A retry carries inputResponses and requestState.
func (m *Manager) call(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tool           string         `json:"tool"`
		Arguments      map[string]any `json:"arguments"`
		InputResponses map[string]any `json:"inputResponses"`
		RequestState   string         `json:"requestState"`
	}
	if !decode(w, r, &in) {
		return
	}
	extra := map[string]any{}
	if in.InputResponses != nil {
		extra["inputResponses"], extra["requestState"] = in.InputResponses, in.RequestState
	}
	if in.Arguments == nil {
		in.Arguments = map[string]any{}
	}
	res, err := m.client("manual").Tool(r.Context(), in.Tool, in.Arguments, extra)
	var rpc *RPCError
	switch {
	case errors.As(err, &rpc):
		write(w, 200, map[string]any{"rpc_error": map[string]any{"code": rpc.Code, "message": rpc.Message}})
	case err != nil:
		fail(w, 502, err.Error())
	default:
		write(w, 200, res)
	}
}

func (m *Manager) started(w http.ResponseWriter, j *Job, err error) {
	if err != nil {
		fail(w, http.StatusTooManyRequests, err.Error())
		return
	}
	write(w, 202, map[string]any{"job": j.ID})
}

func (m *Manager) autopilot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Seed string `json:"seed"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Seed) > 100 {
		fail(w, 400, "seed too long")
		return
	}
	j, err := m.Jobs.Start(func(ctx context.Context, j *Job) error { return m.Autopilot(ctx, j, in.Seed) })
	m.started(w, j, err)
}

func (m *Manager) chat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Messages []Message `json:"messages"`
		Model    string    `json:"model"`
		BoxID    string    `json:"box_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.BoxID != "" && !box.IDPattern.MatchString(in.BoxID) {
		fail(w, 400, "box ids look like box_0123456789")
		return
	}
	if n := len(in.Messages); n == 0 || n > maxMessages || in.Messages[n-1].Role != "user" {
		fail(w, 400, "send up to 20 messages, the last one from the user")
		return
	}
	history := make([]Message, len(in.Messages))
	for i, msg := range in.Messages {
		if (msg.Role != "user" && msg.Role != "assistant") || len(msg.Content) > maxMsgChars || strings.TrimSpace(msg.Content) == "" {
			fail(w, 400, "messages need a user or assistant role and 1 to 4000 characters")
			return
		}
		history[i] = Message{Role: msg.Role, Content: msg.Content}
	}
	model, err := m.ResolveModel(r.Context(), in.Model)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	j, err := m.Jobs.Start(func(ctx context.Context, j *Job) error { return m.Chat(ctx, j, model, history, in.BoxID) })
	m.started(w, j, err)
}

func (m *Manager) job(w http.ResponseWriter, r *http.Request) {
	j := m.Jobs.Get(r.URL.Query().Get("id"))
	if j == nil {
		fail(w, 404, "no such job")
		return
	}
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	steps, done := j.Since(since)
	write(w, 200, map[string]any{"steps": steps, "done": done, "next": since + len(steps)})
}

func (m *Manager) answer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID      string `json:"id"`
		Approve bool   `json:"approve"`
	}
	if !decode(w, r, &in) {
		return
	}
	j := m.Jobs.Get(in.ID)
	switch {
	case j == nil:
		fail(w, 404, "no such job")
	case j.Answer(in.Approve) != nil:
		fail(w, 409, ErrNotWaiting.Error())
	default:
		write(w, 200, map[string]any{"ok": true})
	}
}

func (m *Manager) stop(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if j := m.Jobs.Get(in.ID); j != nil {
		j.Stop()
	}
	write(w, 200, map[string]any{"ok": true})
}
