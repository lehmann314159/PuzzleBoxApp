package agent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var stars = regexp.MustCompile(`(\d+) tiny stars`)

// Autopilot solves a box with a fixed script, through the real /mcp endpoint. It is labelled "scripted"
// in the transcript so nobody mistakes it for a model, and it is a baseline to compare a model with.
func (m *Manager) Autopilot(ctx context.Context, j *Job, seed string) error {
	c := m.client("autopilot")
	j.emit(Step{Kind: "note", Text: "Scripted solver: a fixed sequence of tool calls, not a model."})

	run := func(name string, args map[string]any) (map[string]any, error) {
		res, err := m.callTool(ctx, j, c, name, args)
		if err != nil {
			return nil, err
		}
		j.emit(Step{Kind: "tool", Name: name, Args: args, Text: Text(res), IsError: IsError(res)})
		return res, m.sleep(ctx)
	}
	var box string
	act := func(target, verb string, a map[string]any) (string, error) {
		args := map[string]any{"box_id": box, "target": target, "verb": verb}
		if a != nil {
			args["args"] = a
		}
		res, err := run("act", args)
		return Text(res), err
	}
	data := func(sel string) (map[string]any, error) {
		res, err := run("query", map[string]any{"box_id": box, "select": sel})
		d, _ := structured(res)["data"].(map[string]any)
		if err == nil && d == nil {
			err = fmt.Errorf("query %q returned no data", sel)
		}
		return d, err
	}

	args := map[string]any{"reset": true}
	if seed != "" {
		args["seed"] = seed
	}
	res, err := run("open_box", args)
	if err != nil {
		return err
	}
	if box, _ = structured(res)["box_id"].(string); box == "" {
		return fmt.Errorf("open_box gave no box_id")
	}
	if _, err := data("{ __fields }"); err != nil {
		return err
	}
	for _, d := range "ABC" {
		found := false
		for pos := 0; pos <= 9 && !found; pos++ {
			if _, err := act("dials."+string(d), "turn", map[string]any{"to": pos}); err != nil {
				return err
			}
			said, err := act("dials."+string(d), "listen", nil)
			if err != nil {
				return err
			}
			found = strings.Contains(said, "clean")
		}
		if !found {
			return fmt.Errorf("could not find dial %c", d)
		}
	}
	for _, step := range [][2]string{{"latch", "release"}, {"lid", "open"}, {"drawer", "pull"}} {
		if _, err := act(step[0], step[1], nil); err != nil {
			return err
		}
	}
	d, err := data("{ base { description } card { text inscription } }")
	if err != nil {
		return err
	}
	base, _ := d["base"].(map[string]any)
	card, _ := d["card"].(map[string]any)
	desc, _ := base["description"].(string)
	cipher, _ := card["text"].(string)
	m1 := stars.FindStringSubmatch(desc)
	if m1 == nil || cipher == "" {
		return fmt.Errorf("could not read the stars or the card")
	}
	k, _ := strconv.Atoi(m1[1])
	word := make([]byte, len(cipher))
	for i := range cipher {
		word[i] = byte(((int(cipher[i])-'a'-k)%26+26)%26) + 'a'
	}
	j.emit(Step{Kind: "note", Text: fmt.Sprintf("The card says %q and the base has %d stars, so the word is %q.", cipher, k, word)})
	if _, err := act("grille", "speak", map[string]any{"word": string(word)}); err != nil {
		return err
	}
	final, err := data("{ solved moves }")
	if err != nil {
		return err
	}
	if final["solved"] != true {
		return fmt.Errorf("the script finished but the box is not solved: %v", final)
	}
	j.emit(Step{Kind: "final", Text: fmt.Sprintf("Solved in %v moves.", final["moves"])})
	return nil
}
