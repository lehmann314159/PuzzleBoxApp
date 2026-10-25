package agent

// A Suggestion is a button the chat pane offers. The prompts ask for a read, a judgement or one
// proposed step, and say so, because that is where a small model is reliable; single moves (turn a
// dial, open the lid) belong to the display and Manual mode, and can still be typed in plain words.
type Suggestion struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
	// Show says when the page offers it: "" always; "reply" once the model has answered something;
	// "after:<id>" only straight after the answer to that suggestion.
	Show string `json:"show,omitempty"`
}

var Suggestions = []Suggestion{
	{ID: "stand", Label: "Where do things stand?",
		Prompt: "Look over the whole box and tell me where things stand: what is open, what is shut, and what has changed. Don't change anything."},
	{ID: "untried", Label: "What haven't I tried yet?",
		Prompt: "Look over the box and tell me what we haven't tried yet. Don't change anything."},
	{ID: "next", Label: "What should I try next?",
		Prompt: "Look at the box and suggest one good next step, and say why. Don't do it yet; wait until I say so."},
	{ID: "told", Label: "What did that just tell me?", Show: "reply",
		Prompt: "Remember: a tick means that dial is right, a scratch means it is one step off, and silence means it is far. Using that, explain what the last result tells us, in plain words. Don't change anything."},
	{ID: "lid", Label: "Is it safe to force the lid?",
		Prompt: "Would it be safe to force the lid? Weigh the risk and the cost for me. Don't do it."},
	{ID: "dothat", Label: "Do that", Show: "after:next", Prompt: "Yes, do that."},
}

// SuggestionPrompt returns the prompt of the suggestion with this id.
func SuggestionPrompt(id string) string {
	for _, s := range Suggestions {
		if s.ID == id {
			return s.Prompt
		}
	}
	panic("no suggestion " + id)
}
