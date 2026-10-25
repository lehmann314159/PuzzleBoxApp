package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"puzzleboxapp/internal/agent"
	"puzzleboxapp/internal/box"
	"puzzleboxapp/internal/mcp"
	"puzzleboxapp/internal/store"
)

// stubOllama plays back scripted replies and records what the loop sent.
type stubOllama struct {
	mu     sync.Mutex
	script []map[string]any // each is a "message" object; the last one repeats if the script runs out
	sent   []map[string]any
	models []string
	srv    *httptest.Server
}

func newStub(t *testing.T, script ...map[string]any) *stubOllama {
	s := &stubOllama{script: script, models: []string{"stub:1b", "stub:2b"}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/api/tags" {
			var ms []map[string]any
			for _, n := range s.models {
				ms = append(ms, map[string]any{"name": n})
			}
			json.NewEncoder(w).Encode(map[string]any{"models": ms})
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		s.sent = append(s.sent, req)
		i := min(len(s.sent)-1, len(s.script)-1)
		json.NewEncoder(w).Encode(map[string]any{"message": s.script[i], "done": true})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func say(text string) map[string]any { return map[string]any{"role": "assistant", "content": text} }

// callTool builds a tool-calling reply; args may be an object or a JSON string.
func callTool(name string, args any) map[string]any {
	return map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
		map[string]any{"function": map[string]any{"name": name, "arguments": args}}}}
}

func newManager(t *testing.T, o *stubOllama, tweak func(*agent.Config)) (*agent.Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "box.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mcpSrv := &mcp.Server{Box: &box.Service{Store: st, ValidateArgs: true}, Store: st, Instance: "t", Secret: []byte("k")}
	mux := http.NewServeMux()
	mcpSrv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	cfg := agent.Config{SelfURL: ts.URL + "/mcp", MaxSteps: 8, MaxRunning: 4}
	if o != nil {
		cfg.OllamaURL = o.srv.URL
	}
	if tweak != nil {
		tweak(&cfg)
	}
	return agent.New(cfg), st
}

// runTimeout bounds one job. Scripted models answer at once; the live prompt table raises it.
var runTimeout = 20 * time.Second

// run starts fn as a job and waits for it, answering any question the box asks with approve.
func run(t *testing.T, m *agent.Manager, approve *bool, fn func(context.Context, *agent.Job) error) []agent.Step {
	t.Helper()
	j, err := m.Jobs.Start(fn)
	if err != nil {
		t.Fatal(err)
	}
	var all []agent.Step
	for deadline := time.Now().Add(runTimeout); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		steps, done := j.Since(len(all))
		all = append(all, steps...)
		for _, s := range steps {
			if s.Kind == "ask" && approve != nil {
				if err := j.Answer(*approve); err != nil {
					t.Fatal(err)
				}
			}
		}
		if done {
			more, _ := j.Since(len(all))
			return append(all, more...)
		}
	}
	t.Fatalf("job did not finish; steps: %+v", all)
	return nil
}

func kinds(steps []agent.Step) string {
	var k []string
	for _, s := range steps {
		k = append(k, s.Kind)
	}
	return strings.Join(k, ",")
}

func chat(m *agent.Manager, prompt, boxID string) func(context.Context, *agent.Job) error {
	return func(ctx context.Context, j *agent.Job) error {
		return m.Chat(ctx, j, "stub:1b", []agent.Message{{Role: "user", Content: prompt}}, boxID)
	}
}

func TestAutopilotSolvesTheBox(t *testing.T) {
	m, st := newManager(t, nil, nil)
	steps := run(t, m, nil, func(ctx context.Context, j *agent.Job) error { return m.Autopilot(ctx, j, "autopilot-test") })
	last := steps[len(steps)-1]
	if last.Kind != "final" || !strings.Contains(last.Text, "Solved") {
		t.Fatalf("last step: %+v", last)
	}
	if steps[0].Kind != "note" || !strings.Contains(steps[0].Text, "not a model") {
		t.Errorf("the first step must say it is scripted: %+v", steps[0])
	}
	rows, _, _ := st.Trace(0, "")
	var autopilot int
	for _, r := range rows {
		if strings.Contains(string(r.Doc), `"name":"autopilot"`) {
			autopilot++
		}
	}
	if autopilot == 0 || autopilot != len(rows) {
		t.Errorf("every call should be traced as client 'autopilot': %d of %d", autopilot, len(rows))
	}
}

func TestAgentOpensABoxAndAnswers(t *testing.T) {
	o := newStub(t,
		callTool("open_box", map[string]any{"seed": "agent"}),
		callTool("examine", `{"box_id":"`+box.BoxID("agent")+`","path":"dials.A"}`), // arguments as a JSON string
		say("Dial A turns; I'll listen to it next."))
	m, _ := newManager(t, o, nil)
	steps := run(t, m, nil, chat(m, "Open the box", ""))
	if kinds(steps) != "tool,tool,final" {
		t.Fatalf("steps: %s", kinds(steps))
	}
	if steps[1].IsError || !strings.Contains(steps[1].Text, `"turn"`) {
		t.Errorf("string arguments should have worked: %+v", steps[1])
	}
	id := box.BoxID("agent")
	for i, req := range o.sent {
		msgs := req["messages"].([]any)
		sys := msgs[0].(map[string]any)["content"].(string)
		if (i == 0) == strings.Contains(sys, id) { // absent on turn 1, present afterwards
			t.Errorf("turn %d system prompt box_id handling wrong: %q", i, sys)
		}
		if len(req["tools"].([]any)) != 4 {
			t.Errorf("turn %d: tools = %d", i, len(req["tools"].([]any)))
		}
	}
	last := o.sent[2]["messages"].([]any)
	if tm := last[len(last)-1].(map[string]any); tm["role"] != "tool" || tm["tool_name"] != "examine" {
		t.Errorf("tool result not sent back as a tool message: %v", tm)
	}
}

func TestAgentSurvivesBadToolCalls(t *testing.T) {
	o := newStub(t,
		callTool("solve", map[string]any{}),                             // no such tool
		callTool("examine", "{not json"),                                // malformed arguments
		callTool("examine", map[string]any{"box_id": "box_aaaaaaaaaa"}), // a box that does not exist
		say("I could not find a box."))
	m, _ := newManager(t, o, nil)
	steps := run(t, m, nil, chat(m, "go", ""))
	if kinds(steps) != "tool,tool,tool,final" {
		t.Fatalf("steps: %s", kinds(steps))
	}
	for i, want := range []string{`no tool called "solve"`, "not a JSON object", "no box"} {
		if !steps[i].IsError || !strings.Contains(steps[i].Text, want) {
			t.Errorf("step %d: %+v, want %q", i, steps[i], want)
		}
	}
}

func TestAgentIsToldAboutAndStoppedFromRepeatingItself(t *testing.T) {
	id := box.BoxID("circle")
	o := newStub(t,
		callTool("open_box", map[string]any{"seed": "circle"}),
		callTool("act", map[string]any{"box_id": id, "target": "grille", "verb": "speak"})) // the last call repeats forever
	m, _ := newManager(t, o, func(c *agent.Config) { c.MaxSteps = 30 })
	steps := run(t, m, nil, chat(m, "go", ""))
	last := steps[len(steps)-1]
	if last.Kind != "error" || !strings.Contains(last.Text, "same call 5 times") {
		t.Fatalf("last: %+v", last)
	}
	if n := len(o.sent); n != 6 { // open_box, then the same act five times
		t.Errorf("the model was called %d times, want 6", n)
	}
	// What the model saw: no nudge on the second identical answer, a nudge from the third on.
	var tools []string
	for _, msg := range o.sent[len(o.sent)-1]["messages"].([]any) {
		if mm := msg.(map[string]any); mm["role"] == "tool" {
			tools = append(tools, fmt.Sprint(mm["content"]))
		}
	}
	if len(tools) != 5 || strings.Contains(tools[2], "exact call") || !strings.Contains(tools[3], "exact call 3 times") ||
		!strings.Contains(tools[4], "exact call 4 times") {
		t.Errorf("tool messages the model saw: %q", tools)
	}
}

func TestAgentStopsAtMaxSteps(t *testing.T) {
	o := newStub(t, callTool("open_box", map[string]any{"seed": "loop"})) // repeats forever
	m, _ := newManager(t, o, func(c *agent.Config) { c.MaxSteps = 4 })
	steps := run(t, m, nil, chat(m, "go", ""))
	last := steps[len(steps)-1]
	if last.Kind != "error" || !strings.Contains(last.Text, "4 model turns") || len(o.sent) != 4 {
		t.Fatalf("last: %+v, model calls: %d", last, len(o.sent))
	}
}

func TestForceTheLidAsksAHuman(t *testing.T) {
	id := box.BoxID("force")
	script := []map[string]any{
		callTool("open_box", map[string]any{"seed": "force"}),
		callTool("act", map[string]any{"box_id": id, "target": "lid", "verb": "force"}),
		say("done"),
	}
	for _, c := range []struct {
		name    string
		approve bool
		want    string
	}{{"approve", true, "cracks"}, {"decline", false, "stays shut"}} {
		t.Run(c.name, func(t *testing.T) {
			m, _ := newManager(t, newStub(t, script...), nil)
			steps := run(t, m, &c.approve, chat(m, "break the lid open", ""))
			if kinds(steps) != "tool,ask,tool,final" {
				t.Fatalf("a pause must never be a silent success; steps: %s", kinds(steps))
			}
			if !strings.Contains(steps[1].Prompt, "Forcing the lid") || !strings.Contains(steps[2].Text, c.want) {
				t.Errorf("ask %q, result %q", steps[1].Prompt, steps[2].Text)
			}
		})
	}
}

func TestTwoChatsDoNotAffectEachOther(t *testing.T) {
	a, b := box.BoxID("chat-a"), box.BoxID("chat-b")
	o := newStub(t, say("unused"))
	m, st := newManager(t, o, nil)
	var wg sync.WaitGroup
	for _, seed := range []string{"chat-a", "chat-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := m // each chat gets its own script through its own stub
			run(t, c, nil, func(ctx context.Context, j *agent.Job) error { return c.Autopilot(ctx, j, seed) })
		}()
	}
	wg.Wait()
	for _, id := range []string{a, b} {
		s, _ := st.Load(id)
		if s == nil || !s.Solved {
			t.Errorf("%s not solved", id)
		}
	}
}

func TestStopCancelsAJob(t *testing.T) {
	m, _ := newManager(t, nil, func(c *agent.Config) { c.Pause = time.Hour })
	j, _ := m.Jobs.Start(func(ctx context.Context, j *agent.Job) error { return m.Autopilot(ctx, j, "stop") })
	time.Sleep(100 * time.Millisecond)
	j.Stop()
	for i := 0; i < 200; i++ {
		steps, done := j.Since(0)
		if done {
			if steps[len(steps)-1].Text != "stopped" {
				t.Fatalf("last step: %+v", steps[len(steps)-1])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not stop")
}

func TestBusyAndModelChoice(t *testing.T) {
	o := newStub(t, say("hi"))
	m, _ := newManager(t, o, func(c *agent.Config) { c.MaxRunning = 1; c.Pause = time.Hour })
	if _, err := m.ResolveModel(context.Background(), "gpt-9"); err == nil {
		t.Error("a model Ollama does not have must be refused")
	}
	if got, _ := m.ResolveModel(context.Background(), "stub:2b"); got != "stub:2b" {
		t.Errorf("got %q", got)
	}
	if got, _ := m.ResolveModel(context.Background(), ""); got != "stub:1b" {
		t.Errorf("the first model should be the default, got %q", got)
	}
	j, _ := m.Jobs.Start(func(ctx context.Context, j *agent.Job) error { return m.Autopilot(ctx, j, "busy") })
	defer j.Stop()
	if _, err := m.Jobs.Start(func(context.Context, *agent.Job) error { return nil }); err != agent.ErrBusy {
		t.Errorf("second job: %v, want ErrBusy", err)
	}
}

func TestParseThink(t *testing.T) {
	for in, want := range map[string]string{"": "nil", "auto": "nil", "0": "false", "Off": "false", "false": "false", "1": "true", "on": "true"} {
		got := "nil"
		if v := agent.ParseThink(in); v != nil {
			got = fmt.Sprint(*v)
		}
		if got != want {
			t.Errorf("ParseThink(%q) = %s, want %s", in, got, want)
		}
	}
}
