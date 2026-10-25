package agent

import "testing"

func TestAChangeIsAllowedOnlyOnAPartTheUserNamed(t *testing.T) {
	u := func(s string) Message { return Message{Role: "user", Content: s} }
	a := func(s string) Message { return Message{Role: "assistant", Content: s} }
	cases := []struct {
		name    string
		history []Message
		target  string
		verb    string
		allowed bool
	}{
		{"the dial asked for", []Message{u("Turn dial A to 4.")}, "dials.A", "turn", true},
		{"another dial", []Message{u("Turn dial A to 4.")}, "dials.B", "turn", false},
		{"written dial.A", []Message{u("Turn dial A to 4.")}, "dial.A", "turn", true},
		{"listening is always fine", []Message{u("Turn dial A to 4.")}, "dials.B", "listen", true},
		{"two named dials", []Message{u("Set dial A to 4 and dial C to 2.")}, "dials.C", "turn", true},
		{"a dial not among them", []Message{u("Set dial A to 4 and dial C to 2.")}, "dials.B", "turn", false},
		{"the dials, all of them", []Message{u("Set all the dials to 0.")}, "dials.B", "turn", true},
		{"the lid", []Message{u("Break the lid open.")}, "lid", "force", true},
		{"not the latch when asked about the lid", []Message{u("Open the lid.")}, "latch", "release", false},
		{"the latch", []Message{u("Release the latch.")}, "latch", "release", true},
		{"the grille", []Message{u("Say quartz to the grille.")}, "grille", "speak", true},
		{"speak without naming it", []Message{u("Say quartz.")}, "grille", "speak", false},
		{"the whole box", []Message{u("Open the box.")}, "dials.B", "turn", true},
		{"yes, do that, after a suggestion", []Message{u("What should I try next?"), a("I suggest turning dial B to 1."), u("Yes, do that.")}, "dials.B", "turn", true},
		{"yes, do that, but another part", []Message{u("What should I try next?"), a("I suggest turning dial B to 1."), u("Yes, do that.")}, "latch", "release", false},
		{"the box in an old message does not open everything", []Message{u("Where do things stand in the box?"), a("The lid is closed."), u("Turn dial A to 4.")}, "dials.C", "turn", false},
	}
	for _, c := range cases {
		got := intentOf(c.history).refusal(c.target, c.verb) == ""
		if got != c.allowed {
			t.Errorf("%s: %s %s allowed=%v, want %v", c.name, c.verb, c.target, got, c.allowed)
		}
	}
}
