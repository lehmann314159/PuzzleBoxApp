package agent_test

import (
	"strings"

	"puzzleboxapp/internal/agent"
	"puzzleboxapp/internal/box"
)

// The buttons the page offers (agent.Suggestions) are rows of the same table, so the prompts a visitor
// can click are the prompts that are measured against the model. They are questions: the box must not
// change, and the model must answer in words.
func init() {
	user := func(s string) agent.Message { return agent.Message{Role: "user", Content: s} }
	said := func(s string) agent.Message { return agent.Message{Role: "assistant", Content: s} }
	has := func(answer string, any ...string) bool {
		for _, w := range any {
			if strings.Contains(answer, w) {
				return true
			}
		}
		return false
	}
	look := callTool("examine", map[string]any{"box_id": tableBox, "path": "box"})
	turnA := callTool("act", map[string]any{"box_id": tableBox, "target": "dials.A", "verb": "turn", "args": map[string]any{"to": 4}})

	table = append(table,
		promptCase{
			name: "button: Where do things stand?", prompt: agent.SuggestionPrompt("stand"), seeded: true,
			check: readOnly(true, func(a string) string {
				if !has(a, "lid") || !has(a, "closed", "shut") {
					return "the answer does not say the lid is closed"
				}
				return ""
			}),
			good: []map[string]any{look, say("The lid is closed and the latch is still on.")},
			bad: [][]map[string]any{
				{say("The lid is closed.")}, // never looked
				{look, say("")},             // no answer
				{turnA, say("I turned dial A. The lid is closed.")},    // changed the box
				{look, say("Everything is wide open, nothing to do.")}, // wrong facts
			},
		},
		promptCase{
			name: "button: What haven't I tried yet?", prompt: agent.SuggestionPrompt("untried"), seeded: true,
			check: readOnly(true, nil),
			good:  []map[string]any{look, say("We haven't touched the dials, the grille or the latch.")},
			bad: [][]map[string]any{
				{say("Nothing.")},
				{turnA, say("I tried dial A for you.")},
			},
		},
		promptCase{
			name: "button: What should I try next?", prompt: agent.SuggestionPrompt("next"), seeded: true,
			check: readOnly(true, nil),
			good:  []map[string]any{look, say("Listen to dial A, because it shows what the dial is doing.")},
			bad: [][]map[string]any{
				{look, say("")},
				{turnA, say("Done, I turned it.")}, // did it instead of suggesting it
			},
		},
		promptCase{
			name: "button: What did that just tell me?", prompt: agent.SuggestionPrompt("told"), seeded: true,
			history: []agent.Message{user("Listen to dial A."), said("I listened to dial A and heard silence.")},
			check:   readOnly(false, nil),
			good:    []map[string]any{say("Silence means dial A is not in the right position yet.")},
			bad: [][]map[string]any{
				{say("")},
				{turnA, say("I turned dial A.")},
			},
		},
		promptCase{
			name: "button: Is it safe to force the lid?", prompt: agent.SuggestionPrompt("lid"), seeded: true,
			check: readOnly(false, nil),
			good:  []map[string]any{say("Forcing it could damage the box and costs extra moves, so I would not.")},
			bad: [][]map[string]any{
				{callTool("act", map[string]any{"box_id": tableBox, "target": "lid", "verb": "force"}), say("I forced it.")},
				{say("")},
			},
		},
		promptCase{
			name:    "button: Do that (after a suggestion)",
			history: []agent.Message{user(agent.SuggestionPrompt("next")), said("I suggest listening to dial A, because it shows what the dial is doing.")},
			prompt:  "Yes, do that.", seeded: true,
			check: func(o outcome) string {
				acts := o.acts()
				if len(acts) == 0 {
					return "did not do what it had suggested"
				}
				for _, s := range acts {
					a := argsOf(s)
					// The box accepts "dial.A" as well as "dials.A", so compare what the target means, not how it is spelled.
					t, ok := box.ParseTarget(asString(a["target"]))
					if !ok || t.Kind != "dial" || t.Dial != 0 || !strings.EqualFold(strings.TrimSpace(asString(a["verb"])), "listen") {
						return "did something other than what it had suggested: " + describe([]agent.Step{s})
					}
				}
				return ""
			},
			good: []map[string]any{callTool("act", map[string]any{"box_id": tableBox, "target": "dials.A", "verb": "listen"}), say("Dial A: silence.")},
			bad: [][]map[string]any{
				{say("Sure, I listened.")}, // claimed it without doing it
				{turnA, say("Done.")},      // a different move
			},
		},
	)
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
