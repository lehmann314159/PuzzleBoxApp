package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"puzzleboxapp/internal/api"
	"puzzleboxapp/internal/box"
	"puzzleboxapp/internal/store"
)

func server(t *testing.T, token string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "box.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &api.API{Box: &box.Service{Store: st, ValidateArgs: true}, Store: st, Instance: "t", UIToken: token}
	mux := http.NewServeMux()
	a.Routes(mux)
	ts := httptest.NewServer(a.Guard(mux))
	t.Cleanup(ts.Close)
	return ts, st
}

func do(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestFlowAndTrace(t *testing.T) {
	ts, st := server(t, "")
	code, out := do(t, "POST", ts.URL+"/api/boxes", `{"seed":"solve"}`)
	if code != 200 {
		t.Fatalf("open: %d %v", code, out)
	}
	id := out["data"].(map[string]any)["box_id"].(string)

	if code, out = do(t, "POST", ts.URL+"/api/boxes/"+id+"/act", `{"target":"dials.A","verb":"turn","args":{"to":4}}`); code != 200 || out["is_error"] != false {
		t.Fatalf("act: %d %v", code, out)
	}
	code, out = do(t, "POST", ts.URL+"/api/boxes/"+id+"/act", `{"target":"lid","verb":"force"}`)
	if code != 409 || out["needs_confirmation"] != true {
		t.Fatalf("force without confirm: %d %v", code, out)
	}
	if code, _ = do(t, "POST", ts.URL+"/api/boxes/"+id+"/act", `{"target":"lid","verb":"force","confirm":true}`); code != 200 {
		t.Fatalf("force with confirm: %d", code)
	}
	if code, out = do(t, "GET", ts.URL+"/api/boxes/"+id, ""); code != 200 || out["dials"].([]any)[0] != float64(4) {
		t.Fatalf("view: %d %v", code, out)
	}
	if code, _ = do(t, "GET", ts.URL+"/api/boxes/"+id+"/examine?path=lid", ""); code != 200 {
		t.Fatalf("examine: %d", code)
	}

	rows, _, _ := st.Trace(0, id)
	if len(rows) != 5 { // open is recorded too but has no box in the path; its box comes from the response
		t.Fatalf("trace rows for box = %d, want 5", len(rows))
	}
}

func TestErrors(t *testing.T) {
	ts, _ := server(t, "")
	if code, _ := do(t, "POST", ts.URL+"/api/boxes/nonsense/act", `{}`); code != 400 {
		t.Errorf("bad id: %d", code)
	}
	if code, _ := do(t, "POST", ts.URL+"/api/boxes/box_0000000000/act", `{"target":"lid","verb":"open"}`); code != 404 {
		t.Errorf("missing box: %d", code)
	}
	if code, _ := do(t, "POST", ts.URL+"/api/boxes", `{not json`); code != 400 {
		t.Errorf("bad json: %d", code)
	}
}

func TestToken(t *testing.T) {
	ts, _ := server(t, "s3cret")
	if code, _ := do(t, "GET", ts.URL+"/api/boxes", ""); code != 401 {
		t.Errorf("no token: %d", code)
	}
	if code, _ := do(t, "GET", ts.URL+"/api/boxes?t=wrong", ""); code != 401 {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := do(t, "GET", ts.URL+"/api/boxes?t=s3cret", ""); code != 200 {
		t.Errorf("right token: %d", code)
	}
	if code, _ := do(t, "GET", ts.URL+"/healthz", ""); code != 200 {
		t.Errorf("healthz should be open: %d", code)
	}
}

func TestTraceRowsAreFlat(t *testing.T) {
	ts, _ := server(t, "")
	do(t, "POST", ts.URL+"/api/boxes", `{"seed":"flat"}`)
	_, out := do(t, "GET", ts.URL+"/api/trace", "")
	row := out["rows"].([]any)[0].(map[string]any)
	if row["method"] != "api" || row["name"] != "POST /api/boxes" || row["doc"] != nil || row["instance"] != "t" {
		t.Fatalf("row not flat: %v", row)
	}
}

func TestCookieIsSecureBehindTLS(t *testing.T) {
	ts, _ := server(t, "s3cret")
	for proto, want := range map[string]bool{"https": true, "": false} {
		req, _ := http.NewRequest("GET", ts.URL+"/api/boxes?t=s3cret", nil)
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		cs := resp.Cookies()
		if len(cs) != 1 || cs[0].Secure != want || !cs[0].HttpOnly {
			t.Errorf("proto %q: cookies %+v, want Secure=%v", proto, cs, want)
		}
	}
}
