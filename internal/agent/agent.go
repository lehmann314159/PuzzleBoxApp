package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"puzzleboxapp/internal/box"
)

const maxToolText = 4000

// A model that makes the same call and gets the same answer again and again is told so, then stopped.
const (
	nudgeAfter  = 3
	giveUpAfter = 5
	// A model sometimes answers with nothing at all; it is asked again this many times before the user is told.
	emptyRetries = 2
)

func systemPrompt(boxID, notes string) string {
	s := "You can operate a brass puzzle box on a table using the tools you are given. " +
		"Find out how to open it. Say briefly what you try and why. " +
		"When the box is open, or you cannot make progress, say so plainly. " +
		"If the user tells you to do something to the box, do exactly that, straight away. " +
		"If the user asks a question, answer it, and do not change the box. " +
		"If they ask for a next step, suggest one and wait."
	s += notes // what has been found out so far in this chat, if anything
	if boxID != "" {
		s += " The box on the table is " + boxID + ". Pass that box_id to examine, query and act. " +
			"Do not call open_box unless the user asks for a new box."
	}
	return s
}

// modelReply asks the model, giving it CallTimeout; a model that does not answer in time is asked once more,
// because a call that takes more than a few seconds is a server that has stalled, not a model that is thinking.
func (m *Manager) modelReply(ctx context.Context, j *Job, model string, msgs []Message, tools []map[string]any) (Message, error) {
	for attempt := 1; ; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, m.CallTimeout)
		reply, err := m.Ollama.Chat(callCtx, model, msgs, tools)
		cancel()
		if err == nil || ctx.Err() != nil || attempt >= 2 || !errors.Is(err, context.DeadlineExceeded) {
			return reply, err
		}
		j.emit(Step{Kind: "note", Text: fmt.Sprintf("The model did not answer in %s; asking once more.", m.CallTimeout)})
	}
}

// ollamaTools converts MCP tools to Ollama's function-tool format.
func ollamaTools(list []any) (tools []map[string]any, names []string) {
	for _, t := range list {
		tm, _ := t.(map[string]any)
		name, _ := tm["name"].(string)
		if name == "" {
			continue
		}
		names = append(names, name)
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
			"name": name, "description": tm["description"], "parameters": tm["inputSchema"]}})
	}
	return
}

// objectParams lists, for each tool, the parameters its schema says are objects.
func objectParams(list []any) map[string][]string {
	out := map[string][]string{}
	for _, t := range list {
		tm, _ := t.(map[string]any)
		name, _ := tm["name"].(string)
		schema, _ := tm["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for param, p := range props {
			if pm, _ := p.(map[string]any); pm["type"] == "object" {
				out[name] = append(out[name], param)
			}
		}
	}
	return out
}

// asObjects repairs a mistake small models make often: an object argument sent as a string that holds
// JSON, such as "{\"to\": 4}". The server stays strict; a model client may be forgiving.
func asObjects(args map[string]any, params []string) map[string]any {
	for _, k := range params {
		str, ok := args[k].(string)
		if !ok {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(str)), &obj) == nil && obj != nil {
			args[k] = obj
		}
	}
	return args
}

// Chat runs the model loop: the model picks tools, each runs as a real MCP call, and the answer goes
// back to the model, until it replies without a tool call or MaxSteps is reached.
func (m *Manager) Chat(ctx context.Context, j *Job, model string, history []Message, boxID string) error {
	c := m.client("agent-pane")
	list, err := c.Call(ctx, "tools/list", nil)
	if err != nil {
		return fmt.Errorf("could not list tools: %w", err)
	}
	rawTools, _ := list["tools"].([]any)
	tools, names := ollamaTools(rawTools)
	objects := objectParams(rawTools)

	nb := newNotebook()
	seedDials := func() {
		if boxID == "" {
			return
		}
		if res, err := m.callTool(ctx, j, c, "query", map[string]any{"box_id": boxID, "select": "{ dials { id position } }"}); err == nil {
			nb.seed(res)
		}
	}
	seedDials()
	msgs := append([]Message{{Role: "system"}}, history...)
	var lastCall string // the previous call and its answer, to notice a model going round in a circle
	repeats, empties := 0, 0
	for turn := 0; turn < m.MaxSteps; turn++ {
		msgs[0].Content = systemPrompt(boxID, nb.prompt()) // small models forget handles and what they tried, so restate it every turn
		reply, err := m.modelReply(ctx, j, model, msgs, tools)
		if err != nil {
			return fmt.Errorf("model call failed: %w", err)
		}
		text := strings.TrimSpace(reply.Content)
		if len(reply.ToolCalls) == 0 {
			if text == "" {
				if empties++; empties <= emptyRetries {
					msgs = append(msgs, Message{Role: "user", Content: "You did not say anything. Answer me, or use a tool."})
					continue
				}
				j.emit(Step{Kind: "error", Text: "The model did not answer. Try asking again, in a smaller step."})
				return nil
			}
			j.emit(Step{Kind: "final", Text: text})
			return nil
		}
		if text != "" {
			j.emit(Step{Kind: "model", Text: text})
		}
		// Parse each call's arguments now, then send the calls back with object arguments whatever
		// form they arrived in. A parse error is reported to the model, not silently turned into {}.
		type pending struct {
			name string
			raw  any
			args map[string]any
			err  error
		}
		calls := make([]pending, len(reply.ToolCalls))
		sent := make([]ToolCall, len(reply.ToolCalls))
		for i, tc := range reply.ToolCalls {
			p := pending{name: tc.Function.Name, raw: tc.Function.Arguments}
			p.args, p.err = tc.Args()
			calls[i] = p
			sent[i] = tc
			sent[i].Function.Arguments = p.args
			if p.err != nil {
				sent[i].Function.Arguments = map[string]any{}
			}
		}
		msgs = append(msgs, Message{Role: "assistant", Content: reply.Content, ToolCalls: sent})

		for _, tc := range calls {
			name := tc.name
			out := func(text string, isErr bool, args any) {
				j.emit(Step{Kind: "tool", Name: name, Args: args, Text: text, IsError: isErr, BoxID: boxID})
				body, _ := json.Marshal(args)
				if sig := name + string(body) + text; sig == lastCall {
					repeats++
				} else {
					lastCall, repeats = sig, 1
				}
				content := truncate(text, maxToolText)
				if isErr { // Ollama has no error flag on a tool result, so say it in the words
					content = "That did not work: " + content
				}
				if repeats >= nudgeAfter {
					content += fmt.Sprintf("\n[You have made this exact call %d times and got this same answer. "+
						"Change the call, try something else, or say plainly that you cannot proceed.]", repeats)
				}
				msgs = append(msgs, Message{Role: "tool", ToolName: name, Content: content})
				if a, _ := args.(map[string]any); nb.observe(name, a, text) {
					j.emit(Step{Kind: "note", Text: "Notes handed to the model: " + strings.Join(nb.lines(), " ")})
				}
			}
			args, argErr := tc.args, tc.err
			if argErr == nil {
				args = asObjects(args, objects[name])
			}
			switch {
			case !contains(names, name):
				out(fmt.Sprintf("There is no tool called %q. The tools are: %s.", name, strings.Join(names, ", ")), true, tc.raw)
			case argErr != nil:
				out("Could not call the tool: "+argErr.Error(), true, tc.raw)
			default:
				res, err := m.callTool(ctx, j, c, name, args)
				switch {
				case ctx.Err() != nil:
					return ctx.Err()
				case err != nil:
					out("The call failed: "+err.Error(), true, args)
				default:
					if id, _ := structured(res)["box_id"].(string); box.IDPattern.MatchString(id) {
						if id != boxID {
							boxID = id
							seedDials()
						}
					}
					out(Text(res), IsError(res), args)
				}
			}
		}
		if repeats == nudgeAfter {
			// A tool message is the part a small model weighs least, so say it once as the user, too.
			msgs = append(msgs, Message{Role: "user", Content: "You keep making the same call and getting the same answer. " +
				"Stop. Say in one sentence what you know so far, then try something different."})
		}
		if repeats >= giveUpAfter {
			return fmt.Errorf("stopped: the model made the same call %d times and got the same answer each time", repeats)
		}
	}
	return fmt.Errorf("stopped after %d model turns without a final answer", m.MaxSteps)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
