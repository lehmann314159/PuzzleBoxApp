package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A notebook is what the agent has already found out in this chat, rebuilt from its own tool results and
// handed back to the model on every turn. A small model forgets which dial positions it has tried and
// goes round again; this is specific to the puzzle box on purpose, because this chat only ever talks to one.
// Nothing here is shown to the model until it has learned something, so a plain question gets the plain prompt.
type notebook struct {
	pos     [3]int // where each dial is, -1 if not known yet
	heard   [3]map[int]string
	latch   string // "", "released" or "locked"
	lid     bool
	drawer  bool
	stars   int
	card    string
	spoken  map[string]string // word -> what the grille said about it
	changed bool
}

func newNotebook() *notebook {
	n := &notebook{pos: [3]int{-1, -1, -1}, spoken: map[string]string{}}
	for i := range n.heard {
		n.heard[i] = map[int]string{}
	}
	return n
}

var (
	rested  = regexp.MustCompile(`Dial ([ABC]) now rests at (\d)`)
	soundRe = regexp.MustCompile(`Dial ([ABC]): (silence|a faint scratch|a clean, bright tick)`)
	starsRe = regexp.MustCompile(`(\d+) tiny stars`)
	cardRe  = regexp.MustCompile(`"text":\s*"([a-z, ]+)"`)
)

func short(sound string) string {
	switch sound {
	case "a faint scratch":
		return "scratch"
	case "a clean, bright tick":
		return "TICK"
	}
	return "silence"
}

// seed sets where the dials are, from a query result like {"dials":[{"id":"A","position":0},...]}.
func (n *notebook) seed(res map[string]any) {
	data, _ := structured(res)["data"].(map[string]any)
	ds, _ := data["dials"].([]any)
	for i, d := range ds {
		if dm, _ := d.(map[string]any); dm != nil && i < 3 {
			if p, ok := dm["position"].(float64); ok {
				n.pos[i] = int(p)
			}
		}
	}
}

// observe takes one tool result. It reports whether something worth telling the user changed.
func (n *notebook) observe(name string, args map[string]any, text string) bool {
	before := n.state()
	for _, m := range rested.FindAllStringSubmatch(text, -1) {
		d := int(m[1][0] - 'A')
		n.pos[d] = int(m[2][0] - '0')
	}
	if strings.Contains(text, "The latch settles back into place") {
		n.latch = "locked"
	}
	for _, m := range soundRe.FindAllStringSubmatch(text, -1) {
		d := int(m[1][0] - 'A')
		if p := n.pos[d]; p >= 0 {
			n.heard[d][p] = short(m[2])
		}
	}
	switch {
	case strings.Contains(text, "The latch clicks free"):
		n.latch = "released"
	case strings.Contains(text, "The latch holds"):
		n.latch = "locked"
	}
	if strings.Contains(text, "The lid swings up") {
		n.lid = true
	}
	if strings.Contains(text, "The drawer slides out") {
		n.drawer = true
	}
	if m := starsRe.FindStringSubmatch(text); m != nil {
		fmt.Sscan(m[1], &n.stars)
	}
	if m := cardRe.FindStringSubmatch(text); m != nil && strings.Contains(m[1], ", ") {
		n.card = m[1]
	}
	if strings.Contains(text, "That was not it") && name == "act" {
		inner, _ := args["args"].(map[string]any)
		if w, _ := inner["word"].(string); w != "" {
			switch {
			case strings.Contains(text, "longer"):
				n.spoken[strings.ToLower(w)] = "the word is longer"
			case strings.Contains(text, "shorter"):
				n.spoken[strings.ToLower(w)] = "the word is shorter"
			default:
				n.spoken[strings.ToLower(w)] = "right length, wrong word"
			}
		}
	}
	return n.state() != before // a conclusion changed: a tick found, the latch, the lid, the drawer, the card
}

// state is the part of the notebook worth announcing: what has been settled, not every sound heard.
func (n *notebook) state() string {
	var ticks []string
	for d := range n.heard {
		for p, s := range n.heard[d] {
			if s == "TICK" {
				ticks = append(ticks, fmt.Sprintf("%c%d", 'A'+d, p))
			}
		}
	}
	sort.Strings(ticks)
	return fmt.Sprintf("%v|%s|%v|%v|%d|%s|%d", ticks, n.latch, n.lid, n.drawer, n.stars, n.card, len(n.spoken))
}

// lines is the notebook as text, nothing if nothing has been learned.
func (n *notebook) lines() []string {
	var out []string
	for d := range n.heard {
		if len(n.heard[d]) == 0 {
			continue
		}
		var ps []int
		for p := range n.heard[d] {
			ps = append(ps, p)
		}
		sort.Ints(ps)
		var parts []string
		for _, p := range ps {
			parts = append(parts, fmt.Sprintf("%d %s", p, n.heard[d][p]))
		}
		line := fmt.Sprintf("Dial %c, positions you have listened at: %s.", 'A'+d, strings.Join(parts, "; "))
		for _, p := range ps {
			if n.heard[d][p] == "TICK" {
				line += fmt.Sprintf(" It is right at %d, so leave it there.", p)
			}
		}
		out = append(out, line)
	}
	switch n.latch {
	case "released":
		out = append(out, "The latch is released.")
	case "locked":
		out = append(out, "The latch is still locked.")
	}
	if n.lid {
		out = append(out, "The lid is open.")
	}
	if n.drawer {
		out = append(out, "The drawer is open.")
	}
	if n.stars > 0 {
		out = append(out, fmt.Sprintf("The underside of the box has %d stars.", n.stars))
	}
	if n.card != "" {
		out = append(out, "The card lists these words: "+n.card+".")
	}
	if len(n.spoken) > 0 {
		var ws []string
		for w, why := range n.spoken {
			ws = append(ws, fmt.Sprintf("%q (%s)", w, why))
		}
		sort.Strings(ws)
		out = append(out, "Words already tried at the grille: "+strings.Join(ws, ", ")+".")
	}
	return out
}

// prompt is added to the system prompt, "" while nothing has been learned.
func (n *notebook) prompt() string {
	l := n.lines()
	if len(l) == 0 {
		return ""
	}
	return " What you have already found out in this chat, which you can rely on: " + strings.Join(l, " ")
}
