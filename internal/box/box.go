// Package box is the puzzle box itself: state, the world tree, the query language and the
// rules for acting on it. It knows nothing about HTTP, MCP or storage.
package box

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

var words = []string{"ember", "lantern", "quartz", "willow", "cobalt", "saffron", "meridian"}

// Final is the note found once the box is solved.
const Final = "The grille exhales, and the lid's underside is scratched in a careful hand: " +
	"'A box that remembers nothing can still be opened by anyone who carries the right handle. " +
	"State was never the enemy. Hiding it was.'"

// State is everything that changes. Field names match the Python demo's JSON.
type State struct {
	Seed       string `json:"seed"`
	Dials      [3]int `json:"dials"`
	Released   bool   `json:"released"`
	LidOpen    bool   `json:"lid_open"`
	DrawerOpen bool   `json:"drawer_open"`
	Solved     bool   `json:"solved"`
	Moves      int    `json:"moves"`
	Forced     int    `json:"forced"`
}

// Fresh returns a box that has never been touched.
func Fresh(seed string) *State { return &State{Seed: seed} }

// The card in the drawer lists these words. Each has a different number of letters, so the number of
// stars stamped under the box picks exactly one of them.
var cardWords = []string{"ember", "lantern", "meridian", "moth", "oak", "quartz"}

// CardText is what the card says: every word, in alphabetical order.
func CardText() string { return strings.Join(cardWords, ", ") }

// WordFor is the word on the card with this many letters, "" if there is none.
func WordFor(stars int) string {
	for _, w := range cardWords {
		if len(w) == stars {
			return w
		}
	}
	return ""
}

// Secrets derives the combination, the number of stars and the word from a seed. The stars are the
// length of the word. The same seed always gives the same box. (Not the same box as the Python demo.)
func Secrets(seed string) (combo [3]int, stars int, word string) {
	h := sha256.Sum256([]byte(seed))
	r := rand.New(rand.NewPCG(binary.BigEndian.Uint64(h[:8]), binary.BigEndian.Uint64(h[8:16])))
	for i := range combo {
		combo[i] = r.IntN(10)
	}
	word = cardWords[r.IntN(len(cardWords))]
	return combo, len(word), word
}

// Node is one object in the world tree. Keys starting with '_' are internal.
type Node = map[string]any

type actions = map[string]string

// World builds the box as a tree of nodes.
func World(s *State) Node {
	_, stars, _ := Secrets(s.Seed)
	dials := make([]Node, 3)
	for i, p := range s.Dials {
		dials[i] = Node{
			"id": string("ABC"[i]), "position": p,
			"description": "A knurled brass wheel, marked 0 through 9. Held to the ear, it ticks when it is in the right place, scratches when it is close, and is silent when it is far.",
			"_actions":    actions{"turn": "args {to: 0-9} or {by: integer}", "listen": "press an ear against this wheel (tick = right, scratch = one step off, silence = farther away)"},
		}
	}
	w := Node{
		"description": "A brass box the size of a loaf of bread. It does not announce what it wants, but it seems to be waiting to hear a word.",
		"moves":       s.Moves,
		"damage":      s.Forced,
		"solved":      s.Solved,
		"base": Node{"description": fmt.Sprintf("The underside is stamped with %d tiny stars in a row.", stars),
			"_actions": actions{}},
		"dials": dials,
		"grille": Node{"description": "A small perforated grille on the front. It seems to be waiting for a word.",
			"_actions": actions{"listen": "listen to what every dial is doing (tick = right, scratch = one step off, silence = farther away)", "speak": "args {word: string}"}},
		"latch": Node{"released": s.Released, "description": "A tiny latch holds the lid down. It resists.",
			"_actions": actions{"release": "try to free the latch"}},
		"lid": Node{"open": s.LidOpen, "description": "A hinged brass lid, held by the latch.",
			"_actions": actions{"open": "lift the lid", "force": "pry at the lid (risky; asks for confirmation)"}},
	}
	if s.LidOpen {
		drawer := Node{"open": s.DrawerOpen, "description": "A shallow drawer under the lid, flush and a little stiff.",
			"_actions": actions{"pull": "slide the drawer out"}}
		if s.DrawerOpen {
			// The card lies in the drawer, as the story says. "card" also works from the box (see lookup).
			drawer["card"] = Node{"description": "A brass card, engraved with a short list of words.",
				"text": CardText(), "inscription": "Say the word that has as many letters as the stars stamped under the box.",
				"_actions": actions{}}
		}
		w["drawer"] = drawer
	}
	if s.Solved {
		w["note"] = Final
	}
	return w
}

// lookup finds a child by name. The card lies in the drawer, and "card" also works from the box itself,
// because that is where people and older clients look first.
func lookup(n Node, name string) (any, bool) {
	if v, ok := visible(n)[name]; ok {
		return v, true
	}
	if name == "card" {
		if d, ok := n["drawer"].(Node); ok {
			v, ok := visible(d)["card"]
			return v, ok
		}
	}
	return nil, false
}

func visible(n Node) Node {
	out := Node{}
	for k, v := range n {
		if !strings.HasPrefix(k, "_") {
			out[k] = v
		}
	}
	return out
}

func sortedKeys(n Node) []string {
	keys := make([]string, 0, len(n))
	for k := range n {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isContainer(v any) bool {
	switch v.(type) {
	case Node, []Node:
		return true
	}
	return false
}

func actionNames(n Node) []string {
	a, _ := n["_actions"].(actions)
	return sortedKeys(toNode(a))
}

func toNode(a actions) Node {
	n := Node{}
	for k, v := range a {
		n[k] = v
	}
	return n
}

var pathSplit = regexp.MustCompile(`[.\[\]]+`)

// GetNode walks a dotted path ("dials.A", "lid") from the box.
func GetNode(w Node, path string) (any, string, error) {
	var parts []string
	for _, p := range pathSplit.Split(strings.TrimSpace(path), -1) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 && strings.EqualFold(parts[0], "box") {
		parts = parts[1:]
	}
	var node any = w
	trail := "box"
	for _, part := range parts {
		switch n := node.(type) {
		case []Node:
			var hit Node
			for i, item := range n {
				if strings.EqualFold(fmt.Sprint(item["id"]), part) || strconv.Itoa(i) == part {
					hit = item
					break
				}
			}
			if hit == nil {
				ids := make([]string, len(n))
				for i, item := range n {
					ids[i] = fmt.Sprint(item["id"])
				}
				return nil, "", fmt.Errorf("nothing at '%s.%s'. Items here: %s", trail, part, strings.Join(ids, ", "))
			}
			node = hit
		case Node:
			v, ok := lookup(n, part)
			if !ok {
				return nil, "", fmt.Errorf("nothing at '%s.%s'. Here you can see: %s", trail, part,
					strings.Join(sortedKeys(visible(n)), ", "))
			}
			node = v
		default:
			return nil, "", fmt.Errorf("nothing at '%s.%s'. Here you can see: (nothing)", trail, part)
		}
		trail += "." + part
	}
	return node, trail, nil
}

// Describe says what is at a node, what it contains and which actions it accepts.
func Describe(node any, trail string) map[string]any {
	switch n := node.(type) {
	case []Node:
		ids := make([]any, len(n))
		for i, item := range n {
			ids[i] = item["id"]
		}
		return map[string]any{"path": trail, "items": ids, "hint": "address one with " + trail + ".<id>"}
	case Node:
		values, contains := Node{}, []string{}
		vis := visible(n)
		for _, k := range sortedKeys(vis) {
			if isContainer(vis[k]) {
				contains = append(contains, k)
			} else {
				values[k] = vis[k]
			}
		}
		acts, _ := n["_actions"].(actions)
		if acts == nil {
			acts = actions{}
		}
		return map[string]any{"path": trail, "values": values, "contains": contains, "actions": acts}
	}
	return map[string]any{"path": trail, "value": node}
}

// ---- query language: { lid { open } dials { id position } }

type field struct {
	name   string
	sub    []field
	hasSub bool
}

var token = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*|[{}]`)

// ParseSelection parses a GraphQL-style selection.
func ParseSelection(sel string) ([]field, error) {
	toks := token.FindAllString(sel, -1)
	if len(toks) > 0 && toks[0] == "query" {
		toks = toks[1:]
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty selection. Try '{ __fields }'")
	}
	bare := toks[0] != "{"
	if !bare {
		toks = toks[1:]
	}
	out, i, err := parseFields(toks, 0)
	if err != nil {
		return nil, err
	}
	if !bare {
		if i >= len(toks) || toks[i] != "}" {
			return nil, fmt.Errorf("unbalanced braces: missing closing '}'")
		}
		i++
	}
	if i != len(toks) {
		return nil, fmt.Errorf("unexpected token '%s'", toks[i])
	}
	return out, nil
}

func parseFields(toks []string, i int) ([]field, int, error) {
	var out []field
	for i < len(toks) && toks[i] != "}" {
		name := toks[i]
		if name == "{" {
			return nil, 0, fmt.Errorf("selection must follow a field name")
		}
		i++
		f := field{name: name}
		if i < len(toks) && toks[i] == "{" {
			var err error
			f.sub, i, err = parseFields(toks, i+1)
			if err != nil {
				return nil, 0, err
			}
			if i >= len(toks) {
				return nil, 0, fmt.Errorf("unbalanced braces: missing closing '}'")
			}
			f.hasSub = true
			i++
		}
		out = append(out, f)
	}
	return out, i, nil
}

// QueryError is a GraphQL-style error: the query still returns data for what it could resolve.
type QueryError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func shallow(v any) any {
	switch n := v.(type) {
	case []Node:
		out := make([]any, len(n))
		for i, item := range n {
			out[i] = shallow(item)
		}
		return out
	case Node:
		out := Node{}
		for k, val := range visible(n) {
			if !isContainer(val) {
				out[k] = val
			}
		}
		if _, ok := n["_actions"]; ok {
			out["actions"] = actionNames(n)
		}
		return out
	}
	return v
}

// Resolve runs a parsed selection against a node.
func Resolve(node any, sel []field, path string, errs *[]QueryError) any {
	switch n := node.(type) {
	case []Node:
		out := make([]any, len(n))
		for i, item := range n {
			out[i] = Resolve(item, sel, fmt.Sprintf("%s[%d]", path, i), errs)
		}
		return out
	case Node:
		out := Node{}
		vis := visible(n)
		for _, f := range sel {
			v, ok := lookup(n, f.name)
			switch {
			case f.name == "__fields":
				out[f.name] = append(sortedKeys(vis), "actions")
			case f.name == "actions":
				out[f.name] = actionNames(n)
			case !ok:
				*errs = append(*errs, QueryError{path + "." + f.name,
					fmt.Sprintf("no field '%s'. available: %s, actions", f.name, strings.Join(sortedKeys(vis), ", "))})
				out[f.name] = nil
			case !f.hasSub:
				out[f.name] = shallow(v)
			case isContainer(v):
				out[f.name] = Resolve(v, f.sub, path+"."+f.name, errs)
			default:
				*errs = append(*errs, QueryError{path + "." + f.name,
					fmt.Sprintf("'%s' is a plain value; it has no sub-fields", f.name)})
				out[f.name] = v
			}
		}
		return out
	}
	return node
}

// ---- actions

// sound is what a wheel tells you. The wheel wraps (9 is next to 0), and so does this.
func sound(pos, want int) string {
	switch d := ((pos-want)%10 + 10) % 10; {
	case d == 0:
		return "a clean, bright tick"
	case d == 1 || d == 9:
		return "a faint scratch"
	}
	return "silence"
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

var targetRe = regexp.MustCompile(`^dials?[.\[ ]*([a-c0-2])\]?$`)

// Target is a part of the box that can be acted on. Dial is 0-2 when Kind is "dial".
type Target struct {
	Kind string
	Dial int
}

// ParseTarget understands "dials.A", "dial 0", "lid", "box.latch" and so on.
func ParseTarget(t string) (Target, bool) {
	t = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(t)), "box.")
	if m := targetRe.FindStringSubmatch(t); m != nil {
		if i := strings.Index("abc", m[1]); i >= 0 {
			return Target{"dial", i}, true
		}
		n, _ := strconv.Atoi(m[1])
		return Target{"dial", n}, true
	}
	switch t {
	case "grille", "latch", "lid", "drawer", "card", "base", "box":
		return Target{Kind: t}, true
	}
	return Target{}, false
}

// Apply performs a verb on a target, mutating s. It returns the story text and whether it was an error.
func Apply(s *State, t Target, verb string, args map[string]any) (string, bool) {
	combo, _, word := Secrets(s.Seed)
	s.Moves++
	if s.Solved {
		return "The box is open and quiet. There is nothing left to ask of it.", false
	}
	switch {
	case t.Kind == "dial" && verb == "turn":
		name := string("ABC"[t.Dial])
		if v, ok := args["to"]; ok {
			n, isInt := toInt(v)
			if !isInt || n < 0 || n > 9 {
				return "The wheel only has positions 0 through 9.", true
			}
			s.Dials[t.Dial] = n
		} else if by, ok := toInt(args["by"]); ok {
			s.Dials[t.Dial] = ((s.Dials[t.Dial]+by)%10 + 10) % 10
		} else {
			return "Turn it how? Give {to: 0-9} or {by: integer}.", true
		}
		text := fmt.Sprintf("Dial %s now rests at %d.", name, s.Dials[t.Dial])
		if s.Released {
			text += " The latch settles back into place."
		}
		s.Released = false // moving a wheel re-seats the latch
		return text, false

	case t.Kind == "dial" && verb == "listen":
		return fmt.Sprintf("Dial %c: %s.", "ABC"[t.Dial], sound(s.Dials[t.Dial], combo[t.Dial])), false

	case t.Kind == "grille" && verb == "listen":
		parts := []string{"The grille carries three sounds."}
		for i := range s.Dials {
			parts = append(parts, fmt.Sprintf("Dial %c: %s.", "ABC"[i], sound(s.Dials[i], combo[i])))
		}
		return strings.Join(parts, " "), false

	case t.Kind == "grille" && verb == "speak":
		said, _ := args["word"].(string)
		said = strings.ToLower(strings.TrimSpace(said))
		switch {
		case said == "":
			return "You open your mouth, but give it no word. Give {word: string}.", true
		case !s.DrawerOpen:
			return "The grille hums faintly. It does not seem ready to listen yet. Perhaps the box has more to show you first.", false
		case said == word:
			s.Solved = true
			return Final, false
		}
		text := "The grille hums, then goes still. That was not it."
		switch {
		case len(said) < len(word):
			text += " The word it wants is longer."
		case len(said) > len(word):
			text += " The word it wants is shorter."
		default:
			text += " It is the right length, but not the right word."
		}
		return text, false

	case t.Kind == "latch" && verb == "release":
		if s.Dials == combo {
			s.Released = true
			return "The latch clicks free.", false
		}
		return "The latch holds. The wheels inside are not aligned.", false

	case t.Kind == "lid" && verb == "open":
		if !s.Released {
			return "The lid is held fast.", false
		}
		s.LidOpen = true
		return "The lid swings up. Under it, flush with the floor of the box, you notice a drawer.", false

	case t.Kind == "lid" && verb == "force":
		s.Forced++
		s.Moves += 4 // forcing is expensive
		n := min(s.Forced, 3)
		return fmt.Sprintf("The lid shudders and something cracks. Through the gap you hear dial %c's wheel seat itself: it belongs at %d.",
			"ABC"[n-1], combo[n-1]), false

	case t.Kind == "drawer" && verb == "pull":
		if !s.LidOpen {
			return "There is no drawer to pull.", true
		}
		s.DrawerOpen = true
		return "The drawer slides out with a brass card lying in it.", false
	}
	return unknownVerb(verb, t.Kind), true
}

// acceptedBy lists the verbs each kind of part accepts, read from the world itself so it cannot drift.
func acceptedBy() map[string][]string {
	w := World(&State{Seed: "-", LidOpen: true, DrawerOpen: true})
	out := map[string][]string{}
	for _, k := range []string{"grille", "latch", "lid", "drawer"} {
		if n, ok := w[k].(Node); ok {
			out[k] = actionNames(n)
		}
	}
	if ds, ok := w["dials"].([]Node); ok && len(ds) > 0 {
		out["dial"] = actionNames(ds[0])
	}
	return out
}

// unknownVerb is the reply to a verb the part does not accept. It says where the verb does work, or what the part accepts.
func unknownVerb(verb, kind string) string {
	text := fmt.Sprintf("Nothing happens. '%s' does not seem to be something you can do to the %s.", verb, kind)
	acc := acceptedBy()
	var where []string
	for _, k := range []string{"grille", "latch", "lid", "drawer", "dial"} {
		if k != kind && slices.Contains(acc[k], verb) {
			where = append(where, "the "+k)
		}
	}
	switch {
	case len(where) > 0:
		text += " You can do that to " + strings.Join(where, " or ") + "."
	case len(acc[kind]) > 0:
		text += " The " + kind + " accepts: " + strings.Join(acc[kind], ", ") + "."
	}
	return text
}
