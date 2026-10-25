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
	if r := act(t, sv, id, "grille", "speak", map[string]any{"word": "wrong"}); !strings.HasPrefix(r.Text, "The grille hums, then goes still. That was not it.") {
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

// Every star count from 3 to 8 picks exactly one word on the card, and a box's secret word is the one
// its stars pick.
func TestTheStarsPickOneWordOnTheCard(t *testing.T) {
	words := strings.Split(box.CardText(), ", ")
	seen := map[int]string{}
	for _, w := range words {
		if prev, dup := seen[len(w)]; dup {
			t.Fatalf("%q and %q both have %d letters", prev, w, len(w))
		}
		seen[len(w)] = w
	}
	stars := map[int]bool{}
	for i := 0; i < 500; i++ {
		_, n, word := box.Secrets("seed-" + strconv.Itoa(i))
		if len(word) != n || box.WordFor(n) != word || !strings.Contains(box.CardText(), word) {
			t.Fatalf("seed %d: %d stars, word %q", i, n, word)
		}
		stars[n] = true
	}
	if len(stars) != len(words) {
		t.Errorf("only %d of %d star counts came up in 500 boxes", len(stars), len(words))
	}
}

// A player with no inside knowledge can solve the last step from what the box says: the stars under the
// box, the words on the card, and the card's own instruction.
func TestThePlayerCanPickTheWordFromTheText(t *testing.T) {
	sv := newService(t)
	id := open(t, sv, "reader")
	v, _ := sv.View(id, true)
	combo := v.Secret["combo"].([3]int)
	for i, p := range combo {
		act(t, sv, id, "dials."+string("ABC"[i]), "turn", map[string]any{"to": float64(p)})
	}
	for _, step := range [][2]string{{"latch", "release"}, {"lid", "open"}, {"drawer", "pull"}} {
		act(t, sv, id, step[0], step[1], nil)
	}
	base, _ := sv.Examine(id, "base")
	card, _ := sv.Examine(id, "card")
	vals := card.Data.(map[string]any)["values"].(map[string]any)
	if !strings.Contains(fmtAny(vals["inscription"]), "as many letters as the stars stamped under the box") {
		t.Fatalf("the card does not say what to do: %v", vals)
	}
	n, _ := strconv.Atoi(regexp.MustCompile(`(\d+) tiny stars`).FindStringSubmatch(fmtAny(base.Data.(map[string]any)["values"].(map[string]any)["description"]))[1])
	var word string
	for _, w := range strings.Split(fmtAny(vals["text"]), ", ") {
		if len(w) == n {
			word = w
		}
	}
	if r := act(t, sv, id, "grille", "speak", map[string]any{"word": word}); r.Text != box.Final {
		t.Fatalf("%q (%d letters) was not accepted: %s", word, n, r.Text)
	}
}

func fmtAny(v any) string { s, _ := v.(string); return s }

// The wheel wraps, and so does what it tells you: 9 is one step from 0.
func TestTheWheelSoundWrapsAround(t *testing.T) {
	var zero, nine string
	for i := 0; zero == "" || nine == ""; i++ {
		seed := "wrap-" + strconv.Itoa(i)
		if c, _, _ := box.Secrets(seed); c[0] == 0 && zero == "" {
			zero = seed
		} else if c[0] == 9 && nine == "" {
			nine = seed
		}
	}
	listen := func(seed string, pos int) string {
		s := box.Fresh(seed)
		s.Dials[0] = pos
		tg, _ := box.ParseTarget("dials.A")
		text, _ := box.Apply(s, tg, "listen", nil)
		return text
	}
	if got := listen(zero, 9); !strings.Contains(got, "faint scratch") {
		t.Errorf("9 next to 0: %s", got)
	}
	if got := listen(nine, 0); !strings.Contains(got, "faint scratch") {
		t.Errorf("0 next to 9: %s", got)
	}
	if got := listen(zero, 5); !strings.Contains(got, "silence") {
		t.Errorf("far away: %s", got)
	}
}

func TestTurningADialSaysWhenTheLatchSettlesBack(t *testing.T) {
	sv := newService(t)
	id := open(t, sv, "settle")
	v, _ := sv.View(id, true)
	for i, p := range v.Secret["combo"].([3]int) {
		act(t, sv, id, "dials."+string("ABC"[i]), "turn", map[string]any{"to": float64(p)})
	}
	if r := act(t, sv, id, "latch", "release", nil); r.Text != "The latch clicks free." {
		t.Fatalf("latch: %s", r.Text)
	}
	if r := act(t, sv, id, "dials.A", "turn", map[string]any{"by": float64(1)}); !strings.Contains(r.Text, "The latch settles back into place.") {
		t.Errorf("no word about the latch: %s", r.Text)
	}
	if r := act(t, sv, id, "dials.A", "turn", map[string]any{"by": float64(1)}); strings.Contains(r.Text, "latch") {
		t.Errorf("the latch was already seated: %s", r.Text)
	}
}

// Speaking before the drawer is open must say there is more to find, not only that the grille is not ready.
func TestSpeakingTooEarlyPointsBackIntoTheBox(t *testing.T) {
	sv := newService(t)
	id := open(t, sv, "early")
	r := act(t, sv, id, "grille", "speak", map[string]any{"word": "ember"})
	if !strings.Contains(r.Text, "not seem ready to listen yet") || !strings.Contains(r.Text, "more to show you first") {
		t.Errorf("reply: %s", r.Text)
	}
	if v, _ := sv.View(id, false); v.Solved {
		t.Error("an early word solved the box")
	}
}

// A wrong word is told whether the right one is longer or shorter, like a wheel's scratch and tick.
func TestAWrongWordIsToldLongerOrShorter(t *testing.T) {
	sv := newService(t)
	id := open(t, sv, "length")
	v, _ := sv.View(id, true)
	for i, p := range v.Secret["combo"].([3]int) {
		act(t, sv, id, "dials."+string("ABC"[i]), "turn", map[string]any{"to": float64(p)})
	}
	for _, step := range [][2]string{{"latch", "release"}, {"lid", "open"}, {"drawer", "pull"}} {
		act(t, sv, id, step[0], step[1], nil)
	}
	word := v.Secret["word"].(string)
	say := func(w string) string {
		return act(t, sv, id, "grille", "speak", map[string]any{"word": w}).Text
	}
	if got := say(word + "x"); !strings.Contains(got, "shorter") {
		t.Errorf("too long: %s", got)
	}
	if got := say(word[:len(word)-1]); !strings.Contains(got, "longer") {
		t.Errorf("too short: %s", got)
	}
	if got := say(strings.Repeat("z", len(word))); !strings.Contains(got, "right length, but not the right word") {
		t.Errorf("right length: %s", got)
	}
}

// The card lies in the drawer, as the story says, and "card" still works from the box.
func TestTheCardLiesInTheDrawer(t *testing.T) {
	sv := newService(t)
	id := open(t, sv, "drawer-card")
	v, _ := sv.View(id, true)
	for i, p := range v.Secret["combo"].([3]int) {
		act(t, sv, id, "dials."+string("ABC"[i]), "turn", map[string]any{"to": float64(p)})
	}
	for _, step := range [][2]string{{"latch", "release"}, {"lid", "open"}} {
		act(t, sv, id, step[0], step[1], nil)
	}
	if r, _ := sv.Examine(id, "drawer.card"); !r.IsError {
		t.Errorf("the card was visible before the drawer was pulled: %s", r.Text)
	}
	act(t, sv, id, "drawer", "pull", nil)
	for _, path := range []string{"drawer.card", "box.drawer.card", "card"} {
		r, _ := sv.Examine(id, path)
		if r.IsError || !strings.Contains(r.Text, box.CardText()) {
			t.Errorf("examine %s: %s", path, r.Text)
		}
	}
	if d, _ := sv.Examine(id, "drawer"); !strings.Contains(d.Text, `"card"`) {
		t.Errorf("the open drawer does not say it holds a card: %s", d.Text)
	}
	q, _ := sv.Query(id, "{ drawer { card { text } } }")
	if !strings.Contains(q.Text, box.CardText()) {
		t.Errorf("query through the drawer: %s", q.Text)
	}
	q, _ = sv.Query(id, "{ card { text inscription } }") // how older clients ask
	if !strings.Contains(q.Text, box.CardText()) || strings.Contains(q.Text, "no field") {
		t.Errorf("query from the box: %s", q.Text)
	}
}

// A verb that belongs to another part says where it works; a part with no such verb says what it accepts.
func TestAWrongVerbSaysWhereItWorks(t *testing.T) {
	sv := newService(t)
	id := open(t, sv, "verbs")
	r := act(t, sv, id, "box", "speak", map[string]any{"word": "oak"})
	if !r.IsError || !strings.Contains(r.Text, "You can do that to the grille.") {
		t.Errorf("speak to the box: %s", r.Text)
	}
	r = act(t, sv, id, "lid", "break", nil)
	if !strings.Contains(r.Text, "The lid accepts: force, open.") {
		t.Errorf("break the lid: %s", r.Text)
	}
	r = act(t, sv, id, "dials.A", "listen", nil)
	if r.IsError {
		t.Errorf("listen to a dial: %s", r.Text)
	}
	r = act(t, sv, id, "dials.A", "release", nil)
	if !strings.Contains(r.Text, "You can do that to the latch.") {
		t.Errorf("release a dial: %s", r.Text)
	}
}
