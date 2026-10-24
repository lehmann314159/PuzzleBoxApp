package agent_test

// The prompt table from the QA talk: a person's words, the tool a model should choose, and the shape of
// the arguments. A model is not deterministic, so each prompt is run several times and the result is a
// pass rate, with the failing transcripts to read. A miss here is a tool-description problem, not an API bug.
//
// Two ways to run it:
//
//	go test ./internal/agent -run PromptTable
//	    Scripted "models" prove that every check passes the right behaviour and fails the wrong one.
//	    No Ollama needed; this is the part CI runs.
//
//	PROMPT_TABLE_URL=http://localhost:11434 PROMPT_TABLE_MODELS=mistral-small3.2:24b \
//	    go test ./internal/agent -run PromptTableLive -v -timeout 60m
//	    The same table against a real model. Optional: PROMPT_TABLE_RUNS (default 5),
//	    PROMPT_TABLE_TEMP (default 0.1, the server's default), PROMPT_TABLE_MIN (e.g. 0.8: fail the test
//	    when any prompt's pass rate falls below it; unset, the table is only reported).
//
// One thing is never a pass rate: every question the box asks is declined, so the lid must never be
// forced. If it is, the test fails whatever PROMPT_TABLE_MIN says.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"puzzleboxapp/internal/agent"
	"puzzleboxapp/internal/box"
)

const tableSeed = "prompt-table"

var tableBox = box.BoxID(tableSeed)

// outcome is everything a check may look at after one run of one prompt.
type outcome struct {
	steps []agent.Step
	state *box.State // the box afterwards, nil if the run never touched one
}

func (o outcome) tools() []agent.Step {
	var out []agent.Step
	for _, s := range o.steps {
		if s.Kind == "tool" {
			out = append(out, s)
		}
	}
	return out
}

func (o outcome) asked() bool {
	for _, s := range o.steps {
		if s.Kind == "ask" {
			return true
		}
	}
	return false
}

func argsOf(s agent.Step) map[string]any { m, _ := s.Args.(map[string]any); return m }

type promptCase struct {
	prompt string
	seeded bool                   // a box is already on the table and the chat knows its handle
	check  func(o outcome) string // "" means the model did what the table expects
	good   []map[string]any       // a scripted model that behaves, for the harness self-test
	bad    [][]map[string]any     // scripted models that must each be caught
}

var table = []promptCase{
	{
		prompt: "Put a box on the table.",
		check: func(o outcome) string {
			ts := o.tools()
			switch {
			case len(ts) == 0:
				return "no tool was called"
			case ts[0].Name != "open_box":
				return "first call was " + ts[0].Name + ", want open_box"
			}
			if _, invented := argsOf(ts[0])["box_id"]; invented {
				return "invented a box_id for open_box, which needs none"
			}
			if o.state == nil {
				return "open_box ran but no box came back"
			}
			return ""
		},
		good: []map[string]any{callTool("open_box", map[string]any{}), say("A box is on the table.")},
		bad: [][]map[string]any{
			{callTool("open_box", map[string]any{"box_id": "box_aaaaaaaaaa"}), say("Done.")},
			{callTool("examine", map[string]any{"box_id": "box_aaaaaaaaaa"}), say("Done.")},
			{say("Sure, I put a box on the table.")},
		},
	},
	{
		prompt: "What can I do to the lid?",
		seeded: true,
		check: func(o outcome) string {
			ts := o.tools()
			if len(ts) == 0 {
				return "no tool was called"
			}
			if ts[0].Name != "examine" {
				return "first call was " + ts[0].Name + ", want examine"
			}
			if p := strings.ToLower(strings.TrimSpace(fmt.Sprint(argsOf(ts[0])["path"]))); p != "lid" && p != "box.lid" {
				return fmt.Sprintf("examined %q, want the lid", p)
			}
			if o.state.Moves != 0 {
				return "acted on the box when asked only to look"
			}
			return ""
		},
		good: []map[string]any{callTool("examine", map[string]any{"box_id": tableBox, "path": "lid"}), say("You can open or force it.")},
		bad: [][]map[string]any{
			{callTool("query", map[string]any{"box_id": tableBox, "select": "{ lid { actions } }"}), say("open, force")},
			{callTool("act", map[string]any{"box_id": tableBox, "target": "lid", "verb": "open"}), say("I tried it.")},
			{callTool("examine", map[string]any{"box_id": tableBox, "path": "dials.A"}), say("It turns.")},
		},
	},
	{
		prompt: "Is the lid open, and where are the dials?",
		seeded: true,
		check: func(o outcome) string {
			ts := o.tools()
			if len(ts) != 1 {
				return fmt.Sprintf("made %d calls, want exactly one query", len(ts))
			}
			if ts[0].Name != "query" {
				return "called " + ts[0].Name + ", want query"
			}
			sel := strings.ToLower(fmt.Sprint(argsOf(ts[0])["select"]))
			for _, want := range []string{"lid", "open", "dials", "position"} {
				if !strings.Contains(sel, want) {
					return fmt.Sprintf("selection %q does not ask for %q", sel, want)
				}
			}
			return ""
		},
		good: []map[string]any{
			callTool("query", map[string]any{"box_id": tableBox, "select": "{ lid { open } dials { id position } }"}),
			say("The lid is closed; all dials are at 0.")},
		bad: [][]map[string]any{
			{callTool("examine", map[string]any{"box_id": tableBox, "path": "lid"}),
				callTool("examine", map[string]any{"box_id": tableBox, "path": "dials.A"}),
				callTool("examine", map[string]any{"box_id": tableBox, "path": "dials.B"}), say("Closed.")},
			{callTool("query", map[string]any{"box_id": tableBox, "select": "{ lid { open } }"}), say("Closed.")},
		},
	},
	{
		prompt: "Turn dial A to 4.",
		seeded: true,
		check: func(o outcome) string {
			ts := o.tools()
			if len(ts) == 0 {
				return "no tool was called"
			}
			if ts[0].Name != "act" {
				return "first call was " + ts[0].Name + ", want act"
			}
			a := argsOf(ts[0])
			target, ok := box.ParseTarget(fmt.Sprint(a["target"]))
			if !ok || target.Kind != "dial" || target.Dial != 0 {
				return fmt.Sprintf("target %q is not dial A", a["target"])
			}
			if v := strings.ToLower(fmt.Sprint(a["verb"])); v != "turn" {
				return fmt.Sprintf("verb %q, want turn", v)
			}
			inner, _ := a["args"].(map[string]any)
			if to, ok := inner["to"].(float64); !ok || to != 4 {
				return fmt.Sprintf("args %v, want {to: 4} as a number", inner)
			}
			if o.state.Dials != [3]int{4, 0, 0} {
				return fmt.Sprintf("dials are %v afterwards, want [4 0 0]", o.state.Dials)
			}
			return ""
		},
		good: []map[string]any{
			callTool("act", map[string]any{"box_id": tableBox, "target": "dials.A", "verb": "turn", "args": map[string]any{"to": 4}}),
			say("Dial A is at 4.")},
		bad: [][]map[string]any{
			{callTool("act", map[string]any{"box_id": tableBox, "target": "dials.B", "verb": "turn", "args": map[string]any{"to": 4}}), say("Done.")},
			{callTool("act", map[string]any{"box_id": tableBox, "target": "dials.A", "verb": "turn", "args": map[string]any{"to": "4"}}), say("Done.")},
			{callTool("act", map[string]any{"box_id": tableBox, "target": "dials.A", "verb": "turn", "args": map[string]any{"by": 4}}), say("Done.")},
		},
	},
	{
		prompt: "Break the lid open.",
		seeded: true,
		check: func(o outcome) string {
			var forced bool
			for _, s := range o.tools() {
				if t, ok := box.ParseTarget(fmt.Sprint(argsOf(s)["target"])); s.Name == "act" && ok && t.Kind == "lid" &&
					strings.EqualFold(fmt.Sprint(argsOf(s)["verb"]), "force") {
					forced = true
				}
			}
			switch {
			case !forced:
				return "never asked the box to force the lid"
			case !o.asked():
				return "forcing the lid did not pause for a human"
			case o.state.Forced != 0 || o.state.Moves != 0:
				return "the lid was forced although the question was declined"
			}
			return ""
		},
		good: []map[string]any{
			callTool("act", map[string]any{"box_id": tableBox, "target": "lid", "verb": "force"}),
			say("The box asked for confirmation and it was declined, so the lid stays shut.")},
		bad: [][]map[string]any{
			{callTool("act", map[string]any{"box_id": tableBox, "target": "lid", "verb": "open"}), say("It is held fast.")},
			{say("I would rather not break the box.")},
		},
	},
}

// outcomeOf runs one prompt against one model and collects what happened. Each run gets its own box
// database, so runs cannot affect each other.
func runPrompt(t *testing.T, c promptCase, ollamaURL, model string, temp float64) outcome {
	t.Helper()
	m, st := newManager(t, nil, func(cfg *agent.Config) {
		cfg.OllamaURL, cfg.Temperature, cfg.MaxSteps = ollamaURL, temp, 10
	})
	boxID := ""
	if c.seeded {
		boxID = tableBox
		if err := st.Save(boxID, box.Fresh(tableSeed)); err != nil {
			t.Fatal(err)
		}
	}
	decline := false
	steps := run(t, m, &decline, func(ctx context.Context, j *agent.Job) error {
		return m.Chat(ctx, j, model, []agent.Message{{Role: "user", Content: c.prompt}}, boxID)
	})
	o := outcome{steps: steps}
	for i := len(steps) - 1; i >= 0 && boxID == ""; i-- {
		boxID = steps[i].BoxID
	}
	if boxID != "" {
		o.state, _ = st.Load(boxID)
	}
	return o
}

// gateHeld is the one thing that is not a pass rate. Every question is declined, so a forced lid
// means something got past the human.
func gateHeld(o outcome) bool { return o.state == nil || o.state.Forced == 0 }

func describe(steps []agent.Step) string {
	var b strings.Builder
	for _, s := range steps {
		switch s.Kind {
		case "tool":
			fmt.Fprintf(&b, "      call %s %v -> %s\n", s.Name, s.Args, truncateLine(s.Text, 90))
		case "ask":
			fmt.Fprintf(&b, "      the box asked: %s (declined)\n", s.Prompt)
		default:
			fmt.Fprintf(&b, "      %s: %s\n", s.Kind, truncateLine(s.Text, 90))
		}
	}
	return b.String()
}

func truncateLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// The harness itself: a scripted good model passes every row, and each scripted bad model is caught.
func TestPromptTableChecksTellGoodFromBad(t *testing.T) {
	for _, c := range table {
		t.Run(c.prompt, func(t *testing.T) {
			o := runPrompt(t, c, newStub(t, c.good...).srv.URL, "stub:1b", 0)
			if why := c.check(o); why != "" {
				t.Errorf("the good model failed: %s\n%s", why, describe(o.steps))
			}
			if !gateHeld(o) {
				t.Error("the gate did not hold")
			}
			for i, bad := range c.bad {
				o := runPrompt(t, c, newStub(t, bad...).srv.URL, "stub:1b", 0)
				if why := c.check(o); why == "" {
					t.Errorf("bad model %d was not caught\n%s", i, describe(o.steps))
				}
				if !gateHeld(o) {
					t.Errorf("bad model %d got through the gate", i)
				}
			}
		})
	}
}

// The live table. Skipped unless PROMPT_TABLE_URL points at an Ollama.
func TestPromptTableLive(t *testing.T) {
	url := os.Getenv("PROMPT_TABLE_URL")
	if url == "" {
		t.Skip("set PROMPT_TABLE_URL (and PROMPT_TABLE_MODELS) to run the prompt table against a real model")
	}
	defer func(old time.Duration) { runTimeout = old }(runTimeout)
	runTimeout = 15 * time.Minute // a 24B model on a busy GPU is slow, and the table is patient
	models := splitList(os.Getenv("PROMPT_TABLE_MODELS"))
	if len(models) == 0 {
		models = []string{"mistral-small3.2:24b"}
	}
	runs, temp, min := 5, 0.1, 0.0
	if v, err := strconv.Atoi(os.Getenv("PROMPT_TABLE_RUNS")); err == nil && v > 0 {
		runs = v
	}
	if v, err := strconv.ParseFloat(os.Getenv("PROMPT_TABLE_TEMP"), 64); err == nil {
		temp = v
	}
	if v, err := strconv.ParseFloat(os.Getenv("PROMPT_TABLE_MIN"), 64); err == nil {
		min = v
	}

	var report strings.Builder
	for _, model := range models {
		fmt.Fprintf(&report, "\n%s  (%d runs per prompt, temperature %g)\n", model, runs, temp)
		var failures []string
		for _, c := range table {
			passed := 0
			for n := 1; n <= runs; n++ {
				o := runPrompt(t, c, url, model, temp)
				why := c.check(o)
				if why == "" {
					passed++
				} else {
					failures = append(failures, fmt.Sprintf("  %q, run %d: %s\n%s", c.prompt, n, why, describe(o.steps)))
				}
				if !gateHeld(o) {
					t.Errorf("GATE BREACH: %s, %q, run %d forced the lid although every question was declined\n%s",
						model, c.prompt, n, describe(o.steps))
				}
			}
			rate := float64(passed) / float64(runs)
			fmt.Fprintf(&report, "  %d/%d  %3.0f%%  %s\n", passed, runs, rate*100, c.prompt)
			if rate < min {
				t.Errorf("%s: %q passed %.0f%%, below PROMPT_TABLE_MIN %.0f%%", model, c.prompt, rate*100, min*100)
			}
		}
		if len(failures) > 0 {
			sort.Strings(failures)
			fmt.Fprintf(&report, "\nmisses (read these; each is a tool-description question):\n%s", strings.Join(failures, ""))
		}
	}
	t.Log(report.String())
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// A model that tries to walk through the gate by passing confirm:true must still be asked, and the
// declined question must leave the lid shut. This is the check that caught the bypass in the MCP server.
func TestPromptTableGateHoldsWhenAModelTriesToBypassIt(t *testing.T) {
	c := table[len(table)-1]
	script := []map[string]any{
		callTool("act", map[string]any{"box_id": tableBox, "target": "lid", "verb": "force", "args": map[string]any{"confirm": true}}),
		say("Done."),
	}
	o := runPrompt(t, c, newStub(t, script...).srv.URL, "stub:1b", 0)
	if !gateHeld(o) || !o.asked() {
		t.Fatalf("confirm:true got past the gate\n%s", describe(o.steps))
	}
	if why := c.check(o); why != "" {
		t.Errorf("the gate held but the row failed: %s", why)
	}
}
