package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"puzzleboxapp/internal/box"
	"puzzleboxapp/internal/mcp"
	"puzzleboxapp/internal/store"
)

func newServer(t *testing.T, tweak func(*mcp.Server)) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "box.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &mcp.Server{Box: &box.Service{Store: st, ValidateArgs: true}, Store: st, Instance: "t", Secret: []byte("k")}
	if tweak != nil {
		tweak(m)
	}
	mux := http.NewServeMux()
	m.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, st
}

type reply struct {
	Status int
	Header http.Header
	Body   map[string]any
}

func (r reply) result() map[string]any { m, _ := r.Body["result"].(map[string]any); return m }
func (r reply) errCode() float64 {
	e, _ := r.Body["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return c
}
func (r reply) text() string {
	c := r.result()["content"].([]any)[0].(map[string]any)
	return c["text"].(string)
}

func send(t *testing.T, url string, headers map[string]string, body any) reply {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url+"/mcp", strings.NewReader(string(b)))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := reply{Status: resp.StatusCode, Header: resp.Header}
	json.NewDecoder(resp.Body).Decode(&out.Body)
	return out
}

// modern sends one self-contained 2026-07-28 request, as demo.py's modern() does.
func modern(t *testing.T, url, method string, params map[string]any, elicit bool) reply {
	t.Helper()
	caps := map[string]any{}
	if elicit {
		caps["elicitation"] = map[string]any{}
	}
	params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": mcp.Spec,
		"io.modelcontextprotocol/clientCapabilities": caps}
	h := map[string]string{"MCP-Protocol-Version": mcp.Spec, "Mcp-Method": method}
	if n, ok := params["name"].(string); ok {
		h["Mcp-Name"] = n
	}
	if a, ok := params["arguments"].(map[string]any); ok {
		if id, ok := a["box_id"].(string); ok {
			h["Mcp-Param-BoxId"] = id
		}
	}
	return send(t, url, h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
}

func call(t *testing.T, url, name string, args map[string]any, elicit bool, extra map[string]any) reply {
	p := map[string]any{"name": name, "arguments": args}
	for k, v := range extra {
		p[k] = v
	}
	return modern(t, url, "tools/call", p, elicit)
}

func openBox(t *testing.T, url, seed string) string {
	r := call(t, url, "open_box", map[string]any{"seed": seed}, true, nil)
	return r.result()["structuredContent"].(map[string]any)["box_id"].(string)
}

func TestDiscoverAndListAreStateless(t *testing.T) {
	ts, _ := newServer(t, nil)
	r := modern(t, ts.URL, "server/discover", map[string]any{}, false)
	if r.Status != 200 || r.Header.Get("Mcp-Session-Id") != "" || r.result()["supportedVersions"].([]any)[0] != mcp.Spec {
		t.Fatalf("discover: %+v", r)
	}
	r = modern(t, ts.URL, "tools/list", map[string]any{}, false)
	if n := len(r.result()["tools"].([]any)); n != 4 {
		t.Fatalf("tools = %d, want 4", n)
	}
}

func TestHeaderMismatchesAreRejected(t *testing.T) {
	ts, _ := newServer(t, nil)
	id := openBox(t, ts.URL, "h")
	body := func(name string) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name": name, "arguments": map[string]any{"box_id": id, "path": "box"},
			"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": mcp.Spec}}}
	}
	good := map[string]string{"MCP-Protocol-Version": mcp.Spec, "Mcp-Method": "tools/call", "Mcp-Name": "examine", "Mcp-Param-BoxId": id}
	with := func(k, v string) map[string]string {
		h := map[string]string{}
		for a, b := range good {
			h[a] = b
		}
		h[k] = v
		return h
	}
	for name, h := range map[string]map[string]string{
		"method":  with("Mcp-Method", "tools/list"),
		"name":    with("Mcp-Name", "act"),
		"box":     with("Mcp-Param-BoxId", "box_ffffffffff"),
		"version": with("MCP-Protocol-Version", "2025-11-25"),
	} {
		if r := send(t, ts.URL, h, body("examine")); r.Status != 400 || r.errCode() != -32020 {
			t.Errorf("%s mismatch: %d %v", name, r.Status, r.Body)
		}
	}
	if r := send(t, ts.URL, good, body("examine")); r.Status != 200 {
		t.Errorf("good request: %d %v", r.Status, r.Body)
	}
}

func TestUnsupportedVersionListsSupported(t *testing.T) {
	ts, _ := newServer(t, nil)
	r := send(t, ts.URL, map[string]string{"MCP-Protocol-Version": "2031-01-01"},
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	data := r.Body["error"].(map[string]any)["data"].(map[string]any)["supported"].([]any)
	if r.Status != 400 || r.errCode() != -32022 || data[0] != mcp.Spec {
		t.Fatalf("got %d %v", r.Status, r.Body)
	}
}

func TestLegacyClientWorksWithoutASession(t *testing.T) {
	ts, _ := newServer(t, nil)
	h := map[string]string{"MCP-Protocol-Version": "2025-11-25"}
	r := send(t, ts.URL, h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	if r.Status != 200 || r.Header.Get("Mcp-Session-Id") != "" || r.result()["protocolVersion"] != "2025-11-25" {
		t.Fatalf("initialize: %+v", r)
	}
	if r = send(t, ts.URL, h, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); r.Status != 202 {
		t.Fatalf("notification: %d", r.Status)
	}
	r = send(t, ts.URL, h, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "open_box", "arguments": map[string]any{"seed": "old"}}})
	if _, has := r.result()["resultType"]; has || r.Status != 200 {
		t.Fatalf("legacy result should have no resultType: %+v", r.Body)
	}
	if r = modern(t, ts.URL, "initialize", map[string]any{}, false); r.Status != 404 {
		t.Fatalf("modern initialize should be refused: %d", r.Status)
	}
}

func TestMRTRRoundTrip(t *testing.T) {
	ts, _ := newServer(t, nil)
	id := openBox(t, ts.URL, "mrtr")
	args := map[string]any{"box_id": id, "target": "lid", "verb": "force"}
	r1 := call(t, ts.URL, "act", args, true, nil)
	if r1.result()["resultType"] != "input_required" {
		t.Fatalf("expected a pause: %v", r1.Body)
	}
	state := r1.result()["requestState"].(string)
	answer := func(action string, proceed bool) map[string]any {
		return map[string]any{"requestState": state, "inputResponses": map[string]any{
			"confirm_force": map[string]any{"action": action, "content": map[string]any{"proceed": proceed}}}}
	}
	moves := func() float64 {
		q := call(t, ts.URL, "query", map[string]any{"box_id": id, "select": "{ moves }"}, true, nil)
		return q.result()["structuredContent"].(map[string]any)["data"].(map[string]any)["moves"].(float64)
	}

	if r := call(t, ts.URL, "act", args, true, answer("decline", false)); !strings.Contains(r.text(), "stays shut") || moves() != 0 {
		t.Fatalf("decline must change nothing: %s", r.text())
	}
	forged := answer("accept", true)
	forged["requestState"] = state[:len(state)-4] + "AAAA"
	if r := call(t, ts.URL, "act", args, true, forged); !r.result()["isError"].(bool) || moves() != 0 {
		t.Fatalf("tampered state must be refused: %v", r.Body)
	}
	other := openBox(t, ts.URL, "mrtr-other")
	if r := call(t, ts.URL, "act", map[string]any{"box_id": other, "target": "lid", "verb": "force"}, true, answer("accept", true)); !r.result()["isError"].(bool) {
		t.Fatalf("state from another box must be refused: %v", r.Body)
	}
	if r := call(t, ts.URL, "act", args, true, answer("accept", true)); !strings.Contains(r.text(), "cracks") {
		t.Fatalf("accepted retry should force the lid: %s", r.text())
	}
}

func TestForceWithoutElicitationNeedsExplicitConfirm(t *testing.T) {
	ts, _ := newServer(t, nil)
	id := openBox(t, ts.URL, "noelicit")
	args := map[string]any{"box_id": id, "target": "lid", "verb": "force"}
	if r := call(t, ts.URL, "act", args, false, nil); !r.result()["isError"].(bool) || !strings.Contains(r.text(), "confirm: true") {
		t.Fatalf("got %v", r.Body)
	}
	args["args"] = map[string]any{"confirm": true}
	if r := call(t, ts.URL, "act", args, false, nil); !strings.Contains(r.text(), "cracks") {
		t.Fatalf("got %v", r.Body)
	}
}

func TestBearerAndOrigin(t *testing.T) {
	ts, _ := newServer(t, func(m *mcp.Server) { m.Bearer = "tok"; m.AllowOrigins = []string{"https://ok.example"} })
	ping := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}
	if r := send(t, ts.URL, nil, ping); r.Status != 401 || r.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("no bearer: %d", r.Status)
	}
	if r := send(t, ts.URL, map[string]string{"Authorization": "Bearer nope"}, ping); r.Status != 401 {
		t.Errorf("wrong bearer: %d", r.Status)
	}
	auth := map[string]string{"Authorization": "Bearer tok"}
	if r := send(t, ts.URL, auth, ping); r.Status != 200 {
		t.Errorf("right bearer: %d", r.Status)
	}
	for origin, want := range map[string]int{"https://evil.example": 403, "https://ok.example": 200, "http://localhost:3000": 200} {
		if r := send(t, ts.URL, map[string]string{"Authorization": "Bearer tok", "Origin": origin}, ping); r.Status != want {
			t.Errorf("origin %s: %d, want %d", origin, r.Status, want)
		}
	}
}

func TestStrictParamHeaders(t *testing.T) {
	ts, _ := newServer(t, func(m *mcp.Server) { m.StrictParamHeaders = true })
	id := openBox(t, ts.URL, "strict") // open_box has no box_id, so it needs no header
	h := map[string]string{"MCP-Protocol-Version": mcp.Spec, "Mcp-Method": "tools/call", "Mcp-Name": "examine"}
	r := send(t, ts.URL, h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": "examine", "arguments": map[string]any{"box_id": id},
		"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": mcp.Spec}}})
	if r.Status != 400 || r.errCode() != -32020 {
		t.Fatalf("got %d %v", r.Status, r.Body)
	}
}

func TestEveryCallIsTraced(t *testing.T) {
	ts, st := newServer(t, nil)
	id := openBox(t, ts.URL, "traced")
	call(t, ts.URL, "examine", map[string]any{"box_id": id}, false, nil)
	send(t, ts.URL, nil, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nope"}) // an error is traced too
	rows, _, _ := st.Trace(0, "")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].Box != id || rows[1].Box != id {
		t.Errorf("box ids not recorded: %q %q", rows[0].Box, rows[1].Box)
	}
	if b, _ := json.Marshal(rows[1]); !strings.Contains(string(b), `"Mcp-Method":"tools/call"`) {
		t.Errorf("headers not recorded: %s", b)
	}
}

// A client that can ask a human must always be asked. Passing confirm:true in the arguments is only a
// fallback for clients that cannot, otherwise a model could walk straight through the gate.
func TestConfirmArgDoesNotBypassTheGateForClientsThatCanAsk(t *testing.T) {
	ts, _ := newServer(t, nil)
	id := openBox(t, ts.URL, "bypass")
	args := map[string]any{"box_id": id, "target": "lid", "verb": "force", "args": map[string]any{"confirm": true}}
	r := call(t, ts.URL, "act", args, true, nil)
	if r.result()["resultType"] != "input_required" {
		t.Fatalf("a client that can elicit must be asked, got: %v", r.Body)
	}
	q := call(t, ts.URL, "query", map[string]any{"box_id": id, "select": "{ moves damage }"}, true, nil)
	if d := q.result()["structuredContent"].(map[string]any)["data"].(map[string]any); d["moves"] != float64(0) || d["damage"] != float64(0) {
		t.Fatalf("the lid was forced without asking: %v", d)
	}
}
