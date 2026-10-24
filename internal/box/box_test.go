package box_test

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"puzzleboxapp/internal/box"
	"puzzleboxapp/internal/store"
)

func newService(t *testing.T) *box.Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "box.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &box.Service{Store: st, ValidateArgs: true}
}

func open(t *testing.T, sv *box.Service, seed string) string {
	t.Helper()
	res, err := sv.Open(seed, false)
	if err != nil {
		t.Fatal(err)
	}
	return res.Data.(map[string]any)["box_id"].(string)
}

func act(t *testing.T, sv *box.Service, id, target, verb string, args map[string]any) box.Result {
	t.Helper()
	res, err := sv.Act(box.ActRequest{BoxID: id, Target: target, Verb: verb, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSeedIsDeterministic(t *testing.T) {
	a1, b1, c1 := box.Secrets("solve")
	a2, b2, c2 := box.Secrets("solve")
	if a1 != a2 || b1 != b2 || c1 != c2 {
		t.Fatal("same seed gave different boxes")
	}
	if box.BoxID("solve") != box.BoxID("solve") || !box.IDPattern.MatchString(box.BoxID("x")) {
		t.Fatal("bad box id")
	}
}

func TestSolveUsingOnlyTheToolInterface(t *testing.T) {
	sv := newService(t)
	id := open(t, sv, "solve")

	// Use forcing as the in-world way to learn the combination, as the demo's solver does.
	for i := 0; i < 3; i++ {
		res, _ := sv.Act(box.ActRequest{BoxID: id, Target: "lid", Verb: "force"})
		if !res.NeedsConfirm {
			t.Fatal("force must ask for confirmation first")
		}
		res, _ = sv.Act(box.ActRequest{BoxID: id, Target: "lid", Verb: "force", Confirmed: true})
		m := hint.FindStringSubmatch(res.Text)
		if m == nil {
			t.Fatalf("force message had no hint: %s", res.Text)
		}
		want, _ := strconv.Atoi(m[1])
		act(t, sv, id, "dials."+string("ABC"[i]), "turn", map[string]any{"to": float64(want)})
	}
	if r := act(t, sv, id, "latch", "release", nil); r.Text != "The latch clicks free." {
		t.Fatalf("latch: %s", r.Text)
	}
	act(t, sv, id, "lid", "open", nil)
	act(t, sv, id, "drawer", "pull", nil)
	v, _ := sv.View(id, true)
	if v.Card == nil {
		t.Fatal("no card after pulling the drawer")
	}
	word := v.Secret["word"].(string)
	if r := act(t, sv, id, "grille", "speak", map[string]any{"word": "wrong"}); r.Text != "The grille hums, then goes still. That was not it." {
		t.Fatalf("wrong word: %s", r.Text)
	}
	if r := act(t, sv, id, "grille", "speak", map[string]any{"word": word}); r.Text != box.Final {
		t.Fatalf("right word: %s", r.Text)
	}
	if v, _ := sv.View(id, false); !v.Solved || v.Secret != nil {
		t.Fatal("expected solved and no secret without reveal")
	}
}

var hint = regexp.MustCompile(`belongs at (\d)`)

func TestTwoBoxesAreIndependent(t *testing.T) {
	sv := newService(t)
	a, b := open(t, sv, "one"), open(t, sv, "two")
	act(t, sv, a, "dials.A", "turn", map[string]any{"to": float64(7)})
	va, _ := sv.View(a, false)
	vb, _ := sv.View(b, false)
	if va.Dials[0] != 7 || vb.Dials[0] != 0 {
		t.Fatalf("boxes leaked: %v %v", va.Dials, vb.Dials)
	}
}

func TestNegativeCases(t *testing.T) {
	sv := newService(t)
	id := open(t, sv, "neg")
	cases := []struct {
		name         string
		target, verb string
		args         map[string]any
		contains     string
	}{
		{"missing verb", "dials.A", "", nil, "act needs a verb"},
		{"bad target", "toaster", "turn", nil, "Cannot find"},
		{"out of range", "dials.A", "turn", map[string]any{"to": float64(12)}, "positions 0 through 9"},
		{"fractional", "dials.A", "turn", map[string]any{"to": 1.5}, "positions 0 through 9"},
		{"no amount", "dials.A", "turn", nil, "Turn it how?"},
		{"wrong verb", "lid", "sing", nil, "Nothing happens"},
		{"drawer too early", "drawer", "pull", nil, "no drawer to pull"},
	}
	for _, c := range cases {
		r := act(t, sv, id, c.target, c.verb, c.args)
		if !r.IsError || !strings.Contains(r.Text, c.contains) {
			t.Errorf("%s: got %+v", c.name, r)
		}
	}
	if v, _ := sv.View(id, false); v.Moves != 5 {
		// the two rejected-before-the-box cases cost nothing; the other five each cost a move
		t.Errorf("moves = %d, want 5", v.Moves)
	}
	if r, _ := sv.Examine("box_0000000000", "box"); !r.Missing {
		t.Error("missing box not reported")
	}
}

func TestValidateArgsOffReproducesTheDefect(t *testing.T) {
	sv := newService(t)
	sv.ValidateArgs = false
	id := open(t, sv, "defect")
	r := act(t, sv, id, "lid", "", nil)
	if !strings.Contains(r.Text, "Nothing happens") {
		t.Fatalf("got %q", r.Text)
	}
	if v, _ := sv.View(id, false); v.Moves != 1 {
		t.Fatal("the missing verb should have cost a move")
	}
}

func TestExamineAndQuery(t *testing.T) {
	sv := newService(t)
	id := open(t, sv, "q")
	r, _ := sv.Examine(id, "dials.A")
	if r.IsError || !strings.Contains(r.Text, `"turn"`) {
		t.Fatalf("examine dial: %+v", r)
	}
	if r, _ = sv.Examine(id, "box.nothing"); !r.IsError || !strings.Contains(r.Text, "Here you can see") {
		t.Fatalf("examine bad path: %+v", r)
	}
	r, _ = sv.Query(id, "{ lid { open } dials { id position } bogus }")
	out := r.Data.(map[string]any)
	if errs := out["errors"].([]box.QueryError); len(errs) != 1 || !strings.Contains(errs[0].Message, "available:") {
		t.Fatalf("query errors: %+v", out["errors"])
	}
	if r, _ = sv.Query(id, "{ lid { "); !r.IsError {
		t.Fatal("unbalanced selection should be an error")
	}
	if r, _ = sv.Query(id, "{ __fields }"); !strings.Contains(r.Text, "dials") {
		t.Fatalf("introspection: %s", r.Text)
	}
}
