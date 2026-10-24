// Package mcp is the MCP adapter: JSON-RPC over one POST endpoint, spec 2026-07-28 natively,
// plus a stateless compatibility layer for 2025-era clients. Every tool is a thin call into box.Service.
package mcp

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"puzzleboxapp/internal/box"
	"puzzleboxapp/internal/store"
)

const (
	Spec         = "2026-07-28"
	metaVersion  = "io.modelcontextprotocol/protocolVersion"
	metaCaps     = "io.modelcontextprotocol/clientCapabilities"
	metaServer   = "io.modelcontextprotocol/serverInfo"
	maxBody      = 1 << 20
	instructions = "A brass puzzle box sits on the table. Nothing on it is labelled. " +
		"Call open_box to get a box_id handle, then explore with examine, query and act. " +
		"Ask the box what it can do before you try to do it."
)

var legacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

var localOrigin = regexp.MustCompile(`^https?://(localhost|127\.0\.0\.1|\[::1\])(:\d+)?$`)

type Server struct {
	Box      *box.Service
	Store    *store.Store
	Instance string
	// Secret seals requestState. All instances behind one balancer must share it.
	Secret []byte
	// Bearer, when set, is required as "Authorization: Bearer <value>".
	Bearer string
	// AllowOrigins are browser origins accepted besides localhost.
	AllowOrigins []string
	// StrictParamHeaders makes Mcp-Param-BoxId mandatory on calls that carry a box_id.
	StrictParamHeaders bool
}

// rpcError is a JSON-RPC error plus the HTTP status it travels with.
type rpcError struct {
	Code int
	Msg  string
	HTTP int
	Data any
}

func (e *rpcError) Error() string { return e.Msg }

func rpcErr(code int, http int, format string, a ...any) *rpcError {
	return &rpcError{Code: code, Msg: fmt.Sprintf(format, a...), HTTP: http}
}

// Routes registers the endpoint. Anything but POST is refused.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /mcp", s.post)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
}

// call is one request in flight; its fields feed the trace.
type call struct {
	w      http.ResponseWriter
	start  time.Time
	body   map[string]any
	status int
	resp   any
}

func (c *call) reply(status int, obj any) {
	c.status, c.resp = status, obj
	if obj == nil {
		c.w.WriteHeader(status)
		return
	}
	c.w.Header().Set("Content-Type", "application/json")
	c.w.WriteHeader(status)
	json.NewEncoder(c.w).Encode(obj)
}

func envelope(id any, err *rpcError) map[string]any {
	e := map[string]any{"code": err.Code, "message": err.Msg}
	if err.Data != nil {
		e["data"] = err.Data
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": e}
}

func (s *Server) post(w http.ResponseWriter, r *http.Request) {
	c := &call{w: w, start: time.Now()}
	defer func() {
		if err := s.trace(r, c); err != nil { // tracing must never break a tool call
			fmt.Println("trace failed:", err)
		}
	}()

	if o := r.Header.Get("Origin"); o != "" && !slices.Contains(s.AllowOrigins, o) && !localOrigin.MatchString(o) {
		c.reply(403, envelope(nil, rpcErr(-32000, 403, "Bad Origin")))
		return
	}
	if s.Bearer != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.Bearer)) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		c.reply(401, envelope(nil, rpcErr(-32001, 401, "Missing or wrong bearer token")))
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	var body map[string]any
	if err != nil || json.Unmarshal(raw, &body) != nil || body == nil {
		c.reply(400, envelope(nil, rpcErr(-32700, 400, "Parse error")))
		return
	}
	c.body = body
	id := body["id"]
	method, _ := body["method"].(string)
	params, _ := body["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	meta, _ := params["_meta"].(map[string]any)

	res, rerr := s.handle(r, method, params, meta)
	switch {
	case rerr != nil:
		c.reply(rerr.HTTP, envelope(id, rerr))
	case res == nil:
		c.reply(202, nil)
	default:
		c.reply(200, map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
	}
}

func (s *Server) handle(r *http.Request, method string, params, meta map[string]any) (map[string]any, *rpcError) {
	hv := r.Header.Get("MCP-Protocol-Version")
	if hv != "" && hv != Spec && !slices.Contains(legacyVersions, hv) {
		e := rpcErr(-32022, 400, "Unsupported protocol version")
		e.Data = map[string]any{"supported": append([]string{Spec}, legacyVersions...)}
		return nil, e
	}
	modern := hv == Spec || meta[metaVersion] == Spec
	if modern {
		if err := s.validateModern(r, method, params, meta); err != nil {
			return nil, err
		}
	}
	caps, _ := meta[metaCaps].(map[string]any)
	return s.dispatch(method, params, modern, caps)
}

func (s *Server) validateModern(r *http.Request, method string, params, meta map[string]any) *rpcError {
	h := r.Header.Get
	if h("MCP-Protocol-Version") != Spec || meta[metaVersion] != Spec {
		return rpcErr(-32020, 400, "Header mismatch: MCP-Protocol-Version header and _meta protocolVersion must both be %s", Spec)
	}
	if h("Mcp-Method") != method {
		return rpcErr(-32020, 400, "Header mismatch: Mcp-Method '%s' does not match body method '%s'", h("Mcp-Method"), method)
	}
	if method != "tools/call" {
		return nil
	}
	name, _ := params["name"].(string)
	if h("Mcp-Name") != name {
		return rpcErr(-32020, 400, "Header mismatch: Mcp-Name '%s' does not match body name '%s'", h("Mcp-Name"), name)
	}
	args, _ := params["arguments"].(map[string]any)
	if boxID, ok := args["box_id"].(string); ok {
		got, present := r.Header["Mcp-Param-Boxid"]
		switch {
		case present && got[0] != boxID:
			return rpcErr(-32020, 400, "Header mismatch: Mcp-Param-BoxId does not match box_id")
		case !present && s.StrictParamHeaders:
			return rpcErr(-32020, 400, "Header mismatch: Mcp-Param-BoxId is required for this tool")
		}
	}
	return nil
}

func (s *Server) serverMeta() map[string]any {
	return map[string]any{metaServer: map[string]any{"name": "puzzlebox", "version": "1.0.0", "instance": s.Instance}}
}

func (s *Server) dispatch(method string, params map[string]any, modern bool, caps map[string]any) (map[string]any, *rpcError) {
	switch {
	case method == "initialize": // legacy era only
		if modern {
			return nil, rpcErr(-32601, 404, "Method not found: initialize is not part of %s", Spec)
		}
		return map[string]any{"protocolVersion": legacyVersions[0], "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo":   map[string]any{"name": "puzzlebox", "version": "1.0.0", "instance": s.Instance},
			"instructions": instructions}, nil
	case strings.HasPrefix(method, "notifications/"):
		return nil, nil
	case method == "ping" && !modern:
		return map[string]any{}, nil
	case method == "server/discover":
		return map[string]any{"resultType": "complete", "supportedVersions": append([]string{Spec}, legacyVersions...),
			"capabilities": map[string]any{"tools": map[string]any{}}, "_meta": s.serverMeta(),
			"instructions": instructions, "ttlMs": 3600000, "cacheScope": "public"}, nil
	case method == "tools/list":
		res := map[string]any{"tools": tools, "ttlMs": 300000, "cacheScope": "public"}
		if modern {
			res["resultType"], res["_meta"] = "complete", s.serverMeta()
		}
		return res, nil
	case method == "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		res, err := s.callTool(name, args, params, caps)
		if err != nil {
			return nil, err
		}
		if modern {
			res["_meta"] = s.serverMeta()
		} else {
			delete(res, "resultType")
		}
		return res, nil
	}
	status := 200
	if modern {
		status = 404
	}
	return nil, rpcErr(-32601, status, "Method not found: %s", method)
}

func (s *Server) trace(r *http.Request, c *call) error {
	if c.body == nil {
		return nil
	}
	params, _ := c.body["params"].(map[string]any)
	args, _ := params["arguments"].(map[string]any)
	boxID, _ := args["box_id"].(string)
	if boxID == "" {
		if resp, ok := c.resp.(map[string]any); ok {
			res, _ := resp["result"].(map[string]any)
			sc, _ := res["structuredContent"].(map[string]any)
			boxID, _ = sc["box_id"].(string)
		}
	}
	hdrs := map[string]string{}
	for k, v := range r.Header {
		if l := strings.ToLower(k); strings.HasPrefix(l, "mcp-") || strings.HasPrefix(l, "x-forwarded") {
			hdrs[k] = v[0]
		}
	}
	method, _ := c.body["method"].(string)
	name, _ := params["name"].(string)
	return s.Store.RecordTrace(s.Instance, boxID, c.status, float64(time.Since(c.start).Microseconds())/1000,
		map[string]any{"method": method, "name": name, "req_headers": hdrs, "req": c.body, "resp": c.resp, "mode": "stateless"})
}
