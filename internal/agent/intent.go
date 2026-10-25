package agent

import (
	"regexp"
	"strings"

	"puzzleboxapp/internal/box"
)

// The chat carries what the user wants to the box; it is not there to solve the box on its own. A move that
// changes the box is allowed only on a part the user named, in their latest message or in the suggestion they
// are answering ("Yes, do that"). Looking and listening are always allowed. "The box" or "everything" in the
// user's message allows any part, so "open the box" still works.

var changingVerbs = map[string]bool{"turn": true, "release": true, "open": true, "pull": true, "force": true, "speak": true}

var (
	dialLetter = regexp.MustCompile(`(?i)\bdials?[ .]*([abc])\b`)
	wholeBox   = regexp.MustCompile(`(?i)\b(box|everything|all of it|solve|unlock)\b`)
)

// allowedParts says which parts of the box a change may touch, given the conversation so far.
type allowedParts struct {
	all   bool
	kinds map[string]bool // lid, latch, grille, drawer
	dials map[int]bool
}

func intentOf(history []Message) allowedParts {
	a := allowedParts{kinds: map[string]bool{}, dials: map[int]bool{}}
	var user, before string
	for i := len(history) - 1; i >= 0; i-- {
		switch {
		case history[i].Role == "user" && user == "":
			user = history[i].Content
		case history[i].Role == "assistant" && user != "" && before == "":
			before = history[i].Content
		}
	}
	a.all = wholeBox.MatchString(user)
	for _, text := range []string{user, before} {
		low := strings.ToLower(text)
		for _, k := range []string{"lid", "latch", "grille", "drawer"} {
			if strings.Contains(low, k) {
				a.kinds[k] = true
			}
		}
		for _, m := range dialLetter.FindAllStringSubmatch(text, -1) {
			a.dials[int(strings.ToLower(m[1])[0]-'a')] = true
		}
		if strings.Contains(low, "dials") && !dialLetter.MatchString(text) || strings.Contains(low, "every dial") || strings.Contains(low, "each dial") || strings.Contains(low, "all the dials") {
			for i := 0; i < 3; i++ {
				a.dials[i] = true
			}
		}
		if strings.Contains(low, "dial") && !dialLetter.MatchString(text) { // "the dial" with no letter: any dial
			for i := 0; i < 3; i++ {
				a.dials[i] = true
			}
		}
	}
	return a
}

// refusal is "" if the change is allowed, otherwise what to tell the model.
func (a allowedParts) refusal(target, verb string) string {
	if !changingVerbs[strings.ToLower(verb)] || a.all {
		return ""
	}
	t, ok := box.ParseTarget(target)
	if !ok {
		return "" // an unknown target is the box's own error to give
	}
	switch {
	case t.Kind == "dial" && a.dials[t.Dial]:
		return ""
	case t.Kind != "dial" && a.kinds[t.Kind]:
		return ""
	}
	name := t.Kind
	if t.Kind == "dial" {
		name = "dial " + string(rune('A'+t.Dial))
	}
	return "You were not asked to change the " + name + ". Do only what the user asked, then stop and say what happened."
}
