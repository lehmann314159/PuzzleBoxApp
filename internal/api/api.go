// Package api is the REST/JSON layer. The browser UI uses only this; it works with no MCP involved.
package api

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"puzzleboxapp/internal/box"
	"puzzleboxapp/internal/store"
)

const maxBody = 1 << 16

type API struct {
	Box      *box.Service
	Store    *store.Store
	Instance string
	// UIToken, when set, is required (as ?t=TOKEN once, then a cookie) on everything but /healthz.
	UIToken string
}

// Routes registers the API on mux.
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"ok": true, "instance": a.Instance})
	})
	mux.HandleFunc("POST /api/boxes", a.open)
	mux.HandleFunc("GET /api/boxes", a.list)
	mux.HandleFunc("GET /api/boxes/{id}", a.view)
	mux.HandleFunc("GET /api/boxes/{id}/examine", a.examine)
	mux.HandleFunc("POST /api/boxes/{id}/query", a.query)
	mux.HandleFunc("POST /api/boxes/{id}/act", a.act)
	mux.HandleFunc("GET /api/trace", a.trace)
}

// Authorized reports whether the request carries the UI token (always true when none is configured).
func (a *API) Authorized(r *http.Request) bool {
	if a.UIToken == "" {
		return true
	}
	if t := r.URL.Query().Get("t"); t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(a.UIToken)) == 1 {
		return true
	}
	c, err := r.Cookie("pbt")
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.UIToken)) == 1
}

// Guard wraps a handler with the token check, sets the cookie, and traces box-related API calls.
func (a *API) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Instance", a.Instance)
		if r.URL.Path == "/mcp" { // MCP clients authenticate with the bearer token, not the UI token
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/healthz" {
			if !a.Authorized(r) {
				write(w, 401, map[string]any{"error": "this needs ?t=<token>"})
				return
			}
			if a.UIToken != "" && r.URL.Query().Get("t") != "" {
				http.SetCookie(w, &http.Cookie{Name: "pbt", Value: a.UIToken, Path: "/", HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
					Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
			}
		}
		if !traced(r) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		rec := &recorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		a.record(r, body, rec, start)
	})
}

var boxInPath = regexp.MustCompile(`^/api/boxes/(box_[0-9a-f]{10})`)

// traced: every POST, and examine. Polling endpoints (trace, box view, list) are not recorded.
func traced(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/boxes") ||
		boxInPath.MatchString(r.URL.Path) && strings.HasSuffix(r.URL.Path, "/examine")
}

type recorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (rc *recorder) WriteHeader(s int) { rc.status = s; rc.ResponseWriter.WriteHeader(s) }
func (rc *recorder) Write(b []byte) (int, error) {
	if rc.buf.Len() < maxBody {
		rc.buf.Write(b)
	}
	return rc.ResponseWriter.Write(b)
}

func (a *API) record(r *http.Request, body []byte, rec *recorder, start time.Time) {
	var req, resp any
	_ = json.Unmarshal(body, &req)
	_ = json.Unmarshal(rec.buf.Bytes(), &resp)
	id := ""
	if m := boxInPath.FindStringSubmatch(r.URL.Path); m != nil {
		id = m[1]
	} else if o, ok := resp.(map[string]any); ok {
		if d, ok := o["data"].(map[string]any); ok {
			id, _ = d["box_id"].(string)
		}
	}
	if req == nil && r.URL.RawQuery != "" {
		req = map[string]any{"query": r.URL.Query()}
	}
	doc := map[string]any{"method": "api", "name": r.Method + " " + r.URL.Path, "req_headers": map[string]string{},
		"req": req, "resp": resp, "mode": "api"}
	_ = a.Store.RecordTrace(a.Instance, id, rec.status, float64(time.Since(start).Microseconds())/1000, doc)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]any{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil && err != io.EOF {
		fail(w, 400, "bad JSON: "+err.Error())
		return false
	}
	return true
}

// boxID validates the handle in the path.
func boxID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !box.IDPattern.MatchString(id) {
		fail(w, 400, "box ids look like box_0123456789")
		return "", false
	}
	return id, true
}

// reply turns a domain Result into HTTP: a box saying no is a 200 (or 404 for a missing box),
// a pending confirmation is 409, and the Result body is always the same shape.
func reply(w http.ResponseWriter, res box.Result, err error) {
	switch {
	case err != nil:
		fail(w, 500, "storage error")
	case res.NeedsConfirm:
		write(w, 409, res)
	case res.Missing:
		write(w, 404, res)
	default:
		write(w, 200, res)
	}
}

func (a *API) open(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Seed  string `json:"seed"`
		Reset bool   `json:"reset"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Seed) > 100 {
		fail(w, 400, "seed too long")
		return
	}
	res, err := a.Box.Open(in.Seed, in.Reset)
	reply(w, res, err)
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	ids, err := a.Store.RecentBoxes()
	if err != nil {
		fail(w, 500, "storage error")
		return
	}
	write(w, 200, ids)
}

func (a *API) view(w http.ResponseWriter, r *http.Request) {
	id, ok := boxID(w, r)
	if !ok {
		return
	}
	v, err := a.Box.View(id, r.URL.Query().Get("reveal") == "1")
	switch {
	case err != nil:
		fail(w, 500, "storage error")
	case v == nil:
		fail(w, 404, "no such box")
	default:
		write(w, 200, v)
	}
}

func (a *API) examine(w http.ResponseWriter, r *http.Request) {
	if id, ok := boxID(w, r); ok {
		res, err := a.Box.Examine(id, r.URL.Query().Get("path"))
		reply(w, res, err)
	}
}

func (a *API) query(w http.ResponseWriter, r *http.Request) {
	id, ok := boxID(w, r)
	if !ok {
		return
	}
	var in struct {
		Select string `json:"select"`
	}
	if decode(w, r, &in) {
		res, err := a.Box.Query(id, in.Select)
		reply(w, res, err)
	}
}

func (a *API) act(w http.ResponseWriter, r *http.Request) {
	id, ok := boxID(w, r)
	if !ok {
		return
	}
	var in struct {
		Target  string         `json:"target"`
		Verb    string         `json:"verb"`
		Args    map[string]any `json:"args"`
		Confirm bool           `json:"confirm"`
	}
	if decode(w, r, &in) {
		res, err := a.Box.Act(box.ActRequest{BoxID: id, Target: in.Target, Verb: in.Verb, Args: in.Args,
			Confirmed: in.Confirm})
		reply(w, res, err)
	}
}

func (a *API) trace(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	rows, last, err := a.Store.Trace(since, r.URL.Query().Get("box"))
	if err != nil {
		fail(w, 500, "storage error")
		return
	}
	write(w, 200, map[string]any{"rows": rows, "last": last})
}
