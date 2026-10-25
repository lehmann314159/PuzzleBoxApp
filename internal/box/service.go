package box

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Store is where box state lives. Update must run fn atomically against the stored state.
type Store interface {
	Load(id string) (*State, error)
	Save(id string, s *State) error
	Update(id string, fn func(*State)) (found bool, err error)
}

// IDPattern is the shape of every box handle.
var IDPattern = regexp.MustCompile(`^box_[0-9a-f]{10}$`)

// Service is the box's public behaviour. HTTP, MCP and the agent all call it.
type Service struct {
	Store Store
	// ValidateArgs rejects an act with no verb. Off reproduces the defect the QA talk finds.
	ValidateArgs bool
}

// Result is a domain answer. IsError means "the box said no", not "the server failed".
type Result struct {
	Text    string `json:"text"`
	Data    any    `json:"data,omitempty"`
	IsError bool   `json:"is_error"`
	// Missing means the box handle names no box on the table.
	Missing bool `json:"-"`
	// NeedsConfirm is set when the action must be approved first; nothing has changed.
	NeedsConfirm  bool   `json:"needs_confirmation,omitempty"`
	ConfirmPrompt string `json:"prompt,omitempty"`
}

// ConfirmPrompt is shown to the human before the lid is forced.
const ConfirmPrompt = "Forcing the lid may damage the box and costs extra moves. Do it anyway?"

func noBox(id string) Result {
	return Result{Text: fmt.Sprintf("There is no box '%s' on the table. Use open_box to get one.", id), IsError: true, Missing: true}
}

// BoxID derives the handle for a seed.
func BoxID(seed string) string {
	h := sha256.Sum256([]byte(seed))
	return "box_" + hex.EncodeToString(h[:])[:10]
}

// Open puts a box on the table. An empty seed makes a random one.
func (sv *Service) Open(seed string, reset bool) (Result, error) {
	if seed == "" {
		b := make([]byte, 4)
		rand.Read(b)
		seed = hex.EncodeToString(b)
	}
	id := BoxID(seed)
	existing, err := sv.Store.Load(id)
	if err != nil {
		return Result{}, err
	}
	text := fmt.Sprintf("%s is already on the table, exactly as you left it.", id)
	if existing == nil || reset {
		if err := sv.Store.Save(id, Fresh(seed)); err != nil {
			return Result{}, err
		}
		text = fmt.Sprintf("A brass box is on the table: %s. It is silent.", id)
	}
	return Result{Text: text, Data: map[string]any{"box_id": id}}, nil
}

func (sv *Service) world(id string) (Node, *Result, error) {
	s, err := sv.Store.Load(id)
	if err != nil {
		return nil, nil, err
	}
	if s == nil {
		r := noBox(id)
		return nil, &r, nil
	}
	return World(s), nil, nil
}

// Examine looks closely at one part of a box.
func (sv *Service) Examine(id, path string) (Result, error) {
	w, miss, err := sv.world(id)
	if err != nil || miss != nil {
		if miss != nil {
			return *miss, nil
		}
		return Result{}, err
	}
	if strings.TrimSpace(path) == "" {
		path = "box"
	}
	node, trail, err := GetNode(w, path)
	if err != nil {
		return Result{Text: err.Error(), IsError: true}, nil
	}
	d := Describe(node, trail)
	return Result{Text: toJSON(d), Data: d}, nil
}

// Query reads many things at once.
func (sv *Service) Query(id, selection string) (Result, error) {
	w, miss, err := sv.world(id)
	if err != nil || miss != nil {
		if miss != nil {
			return *miss, nil
		}
		return Result{}, err
	}
	sel, perr := ParseSelection(selection)
	if perr != nil {
		return Result{Text: "Could not read that selection: " + perr.Error(), IsError: true}, nil
	}
	errs := []QueryError{}
	out := map[string]any{"data": Resolve(w, sel, "box", &errs), "errors": errs}
	return Result{Text: toJSON(out), Data: out}, nil
}

// ActRequest is a change to the box. Confirmed means a human already approved a risky action.
type ActRequest struct {
	BoxID     string
	Target    string
	Verb      string
	Args      map[string]any
	Confirmed bool
}

// Act does something to a part of the box.
func (sv *Service) Act(req ActRequest) (Result, error) {
	verb := strings.ToLower(req.Verb)
	t, ok := ParseTarget(req.Target)
	if !ok {
		return Result{Text: fmt.Sprintf("Cannot find '%s'. Try examine with path 'box' to see what is there.", req.Target),
			IsError: true}, nil
	}
	if sv.ValidateArgs && verb == "" {
		return Result{Text: fmt.Sprintf("act needs a verb. Use examine on '%s' to see which verbs it accepts.", req.Target),
			IsError: true}, nil
	}
	s, err := sv.Store.Load(req.BoxID)
	if err != nil {
		return Result{}, err
	}
	if s == nil {
		return noBox(req.BoxID), nil
	}
	if t.Kind == "lid" && verb == "force" && !req.Confirmed {
		return Result{Text: "Forcing the lid needs confirmation.", NeedsConfirm: true, ConfirmPrompt: ConfirmPrompt}, nil
	}
	var text string
	var isErr bool
	var moves int
	found, err := sv.Store.Update(req.BoxID, func(s *State) {
		text, isErr = Apply(s, t, verb, req.Args)
		moves = s.Moves
	})
	if err != nil {
		return Result{}, err
	}
	if !found {
		return noBox(req.BoxID), nil
	}
	return Result{Text: text, Data: map[string]any{"message": text, "moves": moves}, IsError: isErr}, nil
}

// View is what a person standing at the table would see. Secrets only when reveal is set.
type View struct {
	BoxID      string         `json:"box_id"`
	Dials      [3]int         `json:"dials"`
	Released   bool           `json:"released"`
	LidOpen    bool           `json:"lid_open"`
	DrawerOpen bool           `json:"drawer_open"`
	Solved     bool           `json:"solved"`
	Moves      int            `json:"moves"`
	Damage     int            `json:"damage"`
	Card       *string        `json:"card"`
	Final      *string        `json:"final"`
	Secret     map[string]any `json:"secret,omitempty"`
}

// View returns nil if there is no such box.
func (sv *Service) View(id string, reveal bool) (*View, error) {
	s, err := sv.Store.Load(id)
	if err != nil || s == nil {
		return nil, err
	}
	combo, stars, word := Secrets(s.Seed)
	v := &View{BoxID: id, Dials: s.Dials, Released: s.Released, LidOpen: s.LidOpen, DrawerOpen: s.DrawerOpen,
		Solved: s.Solved, Moves: s.Moves, Damage: s.Forced}
	if s.DrawerOpen {
		c := CardText()
		v.Card = &c
	}
	if s.Solved {
		f := Final
		v.Final = &f
	}
	if reveal {
		v.Secret = map[string]any{"combo": combo, "stars": stars, "word": word}
	}
	return v, nil
}
