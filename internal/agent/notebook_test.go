package agent

import (
	"strings"
	"testing"
)

func TestTheNotebookIsEmptyUntilSomethingIsLearned(t *testing.T) {
	n := newNotebook()
	if n.prompt() != "" {
		t.Fatalf("a new notebook says %q", n.prompt())
	}
	if n.observe("examine", nil, `{"actions":{"turn":"args {to: 0-9}"},"path":"box.dials.A"}`) || n.prompt() != "" {
		t.Errorf("looking at a dial taught it something: %q", n.prompt())
	}
}

func TestTheNotebookRemembersWhatEachDialSaid(t *testing.T) {
	n := newNotebook()
	n.seed(map[string]any{"structuredContent": map[string]any{"data": map[string]any{"dials": []any{
		map[string]any{"id": "A", "position": 0.0}, map[string]any{"id": "B", "position": 0.0}, map[string]any{"id": "C", "position": 0.0}}}}})
	if n.observe("act", nil, "Dial A: silence.") {
		t.Error("silence is not worth announcing")
	}
	n.observe("act", nil, "Dial A now rests at 1.")
	n.observe("act", nil, "Dial A: a faint scratch.")
	n.observe("act", nil, "Dial A now rests at 2.")
	if !n.observe("act", nil, "Dial A: a clean, bright tick.") {
		t.Error("finding a tick should be announced")
	}
	n.observe("act", nil, "The grille carries three sounds. Dial A: a clean, bright tick. Dial B: silence. Dial C: silence.")
	got := n.prompt()
	for _, want := range []string{"Dial A, positions you have listened at: 0 silence; 1 scratch; 2 TICK.", "It is right at 2, so leave it there.", "Dial B, positions you have listened at: 0 silence."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestTheNotebookFollowsTheWholePuzzle(t *testing.T) {
	n := newNotebook()
	steps := []struct {
		text string
		want string
	}{
		{"The latch holds. The wheels inside are not aligned.", "The latch is still locked."},
		{"The latch clicks free.", "The latch is released."},
		{"Dial A now rests at 4. The latch settles back into place.", "The latch is still locked."},
		{"The lid swings up. Under it, flush with the floor of the box, you notice a drawer.", "The lid is open."},
		{"The drawer slides out with a brass card lying in it.", "The drawer is open."},
		{`{"description":"The underside is stamped with 5 tiny stars in a row."}`, "has 5 stars"},
		{`{"inscription":"Say the word...","text":"ember, lantern, meridian, moth, oak, quartz"}`, "lists these words: ember, lantern, meridian, moth, oak, quartz."},
	}
	for _, s := range steps {
		n.observe("examine", nil, s.text)
		if !strings.Contains(n.prompt(), s.want) {
			t.Errorf("after %q the notebook says %q, want %q", s.text, n.prompt(), s.want)
		}
	}
	n.observe("act", map[string]any{"args": map[string]any{"word": "Oak"}}, "The grille hums, then goes still. That was not it. The word it wants is longer.")
	if got := n.prompt(); !strings.Contains(got, `"oak" (the word is longer)`) {
		t.Errorf("a tried word is not remembered: %q", got)
	}
}
