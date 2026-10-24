package mcp

import (
	"strings"
	"time"

	"puzzleboxapp/internal/box"
)

func result(text string, data any, isErr bool) map[string]any {
	r := map[string]any{"resultType": "complete", "content": []any{map[string]any{"type": "text", "text": text}},
		"isError": isErr}
	if data != nil {
		r["structuredContent"] = data
	}
	return r
}

func fromBox(r box.Result) map[string]any { return result(r.Text, r.Data, r.IsError) }

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func (s *Server) callTool(name string, args, params, caps map[string]any) (map[string]any, *rpcError) {
	fail := func(err error) (map[string]any, *rpcError) {
		return nil, rpcErr(-32603, 500, "Internal error: %v", err)
	}
	switch name {
	case "open_box":
		reset, _ := args["reset"].(bool)
		res, err := s.Box.Open(str(args, "seed"), reset)
		if err != nil {
			return fail(err)
		}
		return fromBox(res), nil
	case "examine":
		res, err := s.Box.Examine(str(args, "box_id"), str(args, "path"))
		if err != nil {
			return fail(err)
		}
		return fromBox(res), nil
	case "query":
		res, err := s.Box.Query(str(args, "box_id"), str(args, "select"))
		if err != nil {
			return fail(err)
		}
		return fromBox(res), nil
	case "act":
		res, err := s.act(args, params, caps)
		if err != nil {
			return fail(err)
		}
		return res, nil
	}
	return nil, rpcErr(-32602, 200, "Unknown tool: %s", name)
}

// act runs the risky "force" action through MRTR: the first call pauses with input_required and a sealed
// requestState; the retry carries the human's answer and the state back, possibly to another instance.
func (s *Server) act(args, params, caps map[string]any) (map[string]any, error) {
	req := box.ActRequest{BoxID: str(args, "box_id"), Target: str(args, "target"), Verb: str(args, "verb")}
	req.Args, _ = args["args"].(map[string]any)
	target, ok := box.ParseTarget(req.Target)
	isForce := ok && target.Kind == "lid" && strings.EqualFold(req.Verb, "force")

	if isForce {
		answers, _ := params["inputResponses"].(map[string]any)
		if answer, retry := answers["confirm_force"]; retry { // the retry half of an MRTR round trip
			st := s.unseal(str(params, "requestState"))
			if st == nil || st.Box != req.BoxID || st.Target != "lid" || st.Verb != "force" {
				return result("That confirmation expired or does not belong to this request. Start the action again.",
					nil, true), nil
			}
			a, _ := answer.(map[string]any)
			content, _ := a["content"].(map[string]any)
			if a["action"] != "accept" || content["proceed"] != true {
				return result("You think better of it. The lid stays shut.", nil, false), nil
			}
			req.Confirmed = true
		} else if _, canAsk := caps["elicitation"]; !canAsk && req.Args["confirm"] == true {
			// Only a client that cannot ask a human may confirm in its own arguments. One that can ask
			// must be asked, or a model could pass confirm:true and walk through the gate.
			req.Confirmed = true
		}
	}
	res, err := s.Box.Act(req)
	if err != nil || !res.NeedsConfirm {
		return fromBox(res), err
	}
	if _, canAsk := caps["elicitation"]; !canAsk {
		return result("Forcing the lid needs confirmation, and this client cannot ask. "+
			"Retry with args {confirm: true} if you really mean it.", nil, true), nil
	}
	return map[string]any{"resultType": "input_required",
		"inputRequests": map[string]any{"confirm_force": map[string]any{
			"method": "elicitation/create",
			"params": map[string]any{"mode": "form", "message": res.ConfirmPrompt,
				"requestedSchema": map[string]any{"type": "object", "required": []string{"proceed"},
					"properties": map[string]any{"proceed": map[string]any{"type": "boolean", "title": "Force the lid"}}}}}},
		"requestState": s.seal(state{Box: req.BoxID, Target: "lid", Verb: "force",
			Exp: float64(time.Now().Add(5*time.Minute).UnixNano()) / 1e9})}, nil
}
